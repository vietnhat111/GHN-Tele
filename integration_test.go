package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGHN: server giả trả 2 tab theo dữ liệu test đặt sẵn.
type fakeGHN struct {
	mu      sync.Mutex
	arrived []Transport
	upcom   []Transport
	fail    bool
}

func (f *fakeGHN) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("bad gateway"))
		return
	}
	var body struct {
		Offset int `json:"offset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	list := f.arrived
	if strings.Contains(r.URL.Path, "upcoming") {
		list = f.upcom
	}
	if body.Offset > 0 {
		list = nil
	}
	_ = json.NewEncoder(w).Encode(ArrivedResp{Status: "OK", Data: []ArrivedData{{List: list}}})
}

func (f *fakeGHN) set(arr, up []Transport) {
	f.mu.Lock()
	f.arrived, f.upcom, f.fail = arr, up, false
	f.mu.Unlock()
}

func TestPollLiveOverHTTP(t *testing.T) {
	old := exeDir
	exeDir = t.TempDir()
	logPath = exeDir + "/test.log"
	defer func() { exeDir = old }()

	ghn := &fakeGHN{}
	srv := httptest.NewServer(ghn)
	defer srv.Close()

	var tgMu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		_ = json.Unmarshal(b, &p)
		tgMu.Lock()
		sent = append(sent, plain(p["text"].(string)))
		tgMu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()

	oa, ot := apiBase, tgBase
	apiBase, tgBase = srv.URL, tg.URL
	defer func() { apiBase, tgBase = oa, ot }()

	exp := `{"expired":"` + time.Now().Add(48*time.Hour).Format(time.RFC3339) + `"}`
	cfg := Config{
		Session:    "h." + base64.RawURLEncoding.EncodeToString([]byte(exp)) + ".s",
		LocationID: "1", TelegramToken: "t", TelegramChat: "-1",
		AutoSession: boolPtr(false),
	}
	seen := newSeen()
	alertsClear()
	netRecovered(time.Now())
	now := time.Now()

	// 1. gốc: im lặng
	ghn.set([]Transport{trip("OLD", 1)}, []Transport{tripAt("A", now.Add(time.Hour))})
	_, _, msgs, ok := pollLive(&cfg, seen, true)
	if !ok || len(msgs) != 0 {
		t.Fatalf("lần gốc: ok=%v msgs=%v", ok, msgs)
	}

	// 2. xe B mới + A đổi kiện
	a2 := tripAt("A", now.Add(time.Hour))
	a2.WillDropOff.NumOfPkg = 9
	ghn.set([]Transport{trip("OLD", 1)}, []Transport{a2, tripAt("B", now.Add(2*time.Hour))})
	_, _, msgs, ok = pollLive(&cfg, seen, false)
	joined := strings.Join(texts(msgs), "\n---\n")
	if !ok || len(msgs) != 2 || !strings.Contains(joined, "XE SẮP ĐẾN KHO") || !strings.Contains(joined, "CẬP NHẬT SỐ KIỆN") {
		t.Fatalf("vòng 2 sai:\n%s", joined)
	}

	// 3. GHN lỗi → 1 cảnh báo, vòng sau lỗi tiếp → không cảnh báo nữa
	ghn.mu.Lock()
	ghn.fail = true
	ghn.mu.Unlock()
	count := func() int {
		tgMu.Lock()
		defer tgMu.Unlock()
		n := 0
		for _, m := range sent {
			if strings.Contains(m, "Không kết nối được GHN") {
				n++
			}
		}
		return n
	}
	_, _, _, ok = pollLive(&cfg, seen, false)
	_, _, _, ok2 := pollLive(&cfg, seen, false)
	if ok || ok2 || count() != 0 {
		t.Fatalf("lỗi 2 lần (chớp nhoáng) chưa được báo: ok=%v,%v báo=%d", ok, ok2, count())
	}
	pollLive(&cfg, seen, false) // lần 3 → báo
	pollLive(&cfg, seen, false) // lần 4 → không báo lại (1 lần/giờ)
	state, _ := outboxAccess(cfg, nil)
	if len(state.Pending) != 1 || count() != 0 {
		t.Fatalf("cảnh báo phải vào hàng đợi trước khi gửi: pending=%d sent=%d", len(state.Pending), count())
	}
	retryOutbox(cfg, time.Now())
	if count() != 1 {
		t.Fatalf("lỗi 3 lần liên tiếp phải báo đúng 1 lần, được %d", count())
	}
	tgMu.Lock()
	last := sent[len(sent)-1]
	tgMu.Unlock()
	if !strings.Contains(last, "máy chủ GHN đang lỗi") || strings.Contains(last, "http://") {
		t.Errorf("tin lỗi phải dễ hiểu, không có link kỹ thuật:\n%s", last)
	}

	// 4. khỏi lỗi: A vào kho → tin "kết nối lại" + 🟠
	ghn.set([]Transport{trip("OLD", 1), a2}, []Transport{tripAt("B", now.Add(2*time.Hour))})
	_, _, msgs, ok = pollLive(&cfg, seen, false)
	joined = strings.Join(texts(msgs), "\n---\n")
	if !ok || !strings.Contains(joined, "Đã kết nối lại") || !strings.Contains(joined, "ĐÃ ĐẾN KHO") {
		t.Fatalf("vòng 4 sai:\n%s", joined)
	}
	if seen.Day.Arrived["A"] != 9 || !seen.Day.Pending {
		t.Errorf("số liệu tổng kết sai: %+v", seen.Day)
	}

	// 5. Chốt như vòng theo dõi thật và đọc lại được ngay cả trước khi ghi lastscan.json.
	seen.ScanAt = time.Now().Unix()
	if err := commitScan(cfg, seen, msgs, time.Now()); err != nil {
		t.Fatal(err)
	}
	re := loadCommittedSeen(cfg)
	if _, ok := re.Arrived["A"]; !ok || re.Sig["B"] == "" || !re.Day.Pending {
		t.Errorf("trạng thái quét đọc lại thiếu dữ liệu: %+v", re)
	}
	if !canResume(re, time.Unix(re.ScanAt, 0), time.Now()) {
		t.Error("vừa quét xong thì bật lại phải được tiếp tục")
	}
}

func texts(m []outMsg) []string {
	var out []string
	for _, x := range m {
		out = append(out, x.Text)
	}
	return out
}
