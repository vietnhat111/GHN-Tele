package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeliverNoDuplicateOnPartialFailure(t *testing.T) {
	defer botTestDir(t)()
	var mu sync.Mutex
	got := map[string]int{}
	quietSeen := false
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		_ = json.Unmarshal(b, &p)
		chat := p["chat_id"].(string)
		mu.Lock()
		defer mu.Unlock()
		if p["disable_notification"] == true {
			quietSeen = true
		}
		if chat == "-100" { // bot bị mời khỏi nhóm
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
			return
		}
		got[chat]++
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	old := tgBase
	tgBase = tg.URL
	defer func() { tgBase = old }()
	logPath = t.TempDir() + "/log"

	cfg := Config{TelegramToken: "t", TelegramChat: "-100", TelegramPersonal: "42", NotifyMode: "both"}
	deliver(cfg, loud("🟠 xe đến"))
	if got["42"] != 1 {
		t.Errorf("chat riêng phải nhận đúng 1 lần dù nhóm lỗi, được %d", got["42"])
	}
	deliver(cfg, quiet("🔄 đổi kiện"))
	if !quietSeen {
		t.Error("tin phụ phải gửi disable_notification")
	}
	if !isPermanent(tgPermanent{io.EOF}) || isPermanent(io.EOF) {
		t.Error("phân loại lỗi vĩnh viễn sai")
	}
}

func TestMessagePriority(t *testing.T) {
	d := scanDiff{
		Arrived:  []Transport{trip("A", 1)},
		NewTrips: []Transport{trip("B", 0)},
		Changes:  []pkgChange{{It: trip("C", 2), Old: "1 0 0 0", New: "2 0 0 0"}},
	}
	for _, m := range diffMessages(d, timeNow()) {
		isUpd := m.Text[:len("🔄")] == "🔄"
		if isUpd != m.Quiet {
			t.Errorf("chỉ 🔄 im lặng: quiet=%v\n%s", m.Quiet, m.Text)
		}
	}
}

func timeNow() time.Time { return time.Now() }

func TestVehicleMapAndExtras(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/tms/v1/transportation/get-vehicle-location" || b["vehiclePlate"] != "43H-051.88" {
			_, _ = w.Write([]byte(`{"status":"ERROR"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"OK","data":[{"latitude":16.4637,"longitude":107.5909}]}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	u := fetchVehicleMap(Config{Session: "s"}, "43H-051.88")
	if u != "https://maps.google.com/?q=16.463700,107.590900" {
		t.Fatalf("link vị trí: %q", u)
	}
	if fetchVehicleMap(Config{}, "-") != "" || fetchVehicleMap(Config{}, "99X") != "" {
		t.Error("không có biển số / GHN lỗi thì không có link")
	}

	it := sampleUpcoming()
	it.ExpectedArrivalTime = time.Now().Add(-40 * time.Minute).Format(time.RFC3339)
	it.WillDropOff.NumOfPkgFragile = 2
	it.IncludeSortGroupCodes = []string{"HUE-01", "-", "HUE-02"}
	m := lateMsg(it, time.Now(), u)
	for _, want := range []string{`<a href="https://maps.google.com/?q=16.463700,107.590900">`, "Có 2 kiện dễ vỡ", "Thuộc nhóm: HUE-01, HUE-02"} {
		if !strings.Contains(m, want) {
			t.Errorf("thiếu %q:\n%s", want, m)
		}
	}
	if strings.Count(m, "<a ") != strings.Count(m, "</a>") || strings.Count(m, "<b>") != strings.Count(m, "</b>") {
		t.Errorf("thẻ HTML không cân:\n%s", m)
	}
	empty := it
	empty.WillDropOff = WillDropOff{NumOfPkgFragile: 0}
	empty.ExcludeSortGroupCodes = []string{"DN-03"}
	if f := plain(firstMsg(empty)); !strings.Contains(f, "Cần nhận: chưa có kiện\n🏷 Ngoại trừ nhóm: DN-03") {
		t.Errorf("xe chưa có kiện vẫn phải hiện nhóm:\n%s", f)
	}
}

func TestNetReasonAndCounter(t *testing.T) {
	cases := map[string]string{
		`Post "https://inside-prd-api.ghn.vn/x": net/http: TLS handshake timeout`: "mạng chậm hoặc máy chủ GHN không phản hồi",
		`dial tcp: lookup inside-prd-api.ghn.vn: no such host`:                    "máy kho mất mạng (không kết nối được Internet)",
		`GHN trả lỗi (http 502)`:                                                  "máy chủ GHN đang lỗi",
	}
	for in, want := range cases {
		if got := netReason(errors.New(in)); got != want {
			t.Errorf("netReason(%q) = %q, muốn %q", in, got, want)
		}
	}
	netRecovered(time.Now())
	t0 := time.Now()
	if _, due := netFailed(t0); due {
		t.Error("lần 1 chưa báo")
	}
	netFailed(t0.Add(5 * time.Minute))
	d, due := netFailed(t0.Add(10 * time.Minute))
	if !due || d != 10*time.Minute {
		t.Errorf("lần 3 phải báo, đã lỗi 10 phút: %v %v", d, due)
	}
	if got := netRecovered(t0.Add(15 * time.Minute)); got != 15*time.Minute {
		t.Errorf("gián đoạn 15 phút, được %v", got)
	}
	if netRecovered(t0) != 0 {
		t.Error("không lỗi thì 0")
	}
	m := plain(netAlertMsg(errors.New("net/http: TLS handshake timeout"), 10*time.Minute))
	if !strings.Contains(m, "Không kết nối được GHN 10 phút") || strings.Contains(m, "https://") {
		t.Errorf("tin lỗi mạng:\n%s", m)
	}
}

func TestSessionKeptWhenExtendFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(504) // mạng/GHN lỗi đúng lúc gia hạn
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()
	logPath = t.TempDir() + "/log"

	mk := func(left time.Duration) Config {
		exp := `{"expired":"` + time.Now().Add(left).Format(time.RFC3339) + `"}`
		return Config{Session: "h." + base64.RawURLEncoding.EncodeToString([]byte(exp)) + ".s", AutoSession: boolPtr(false)}
	}
	c := mk(2 * time.Hour)
	if err := ensureSession(&c); err != nil {
		t.Errorf("SESSION còn 2 giờ, gia hạn lỗi do mạng → dùng tiếp, không báo lỗi: %v", err)
	}
	c = mk(-time.Minute)
	if err := ensureSession(&c); err == nil {
		t.Error("SESSION đã hết hạn mà không gia hạn được → phải báo")
	}
}
