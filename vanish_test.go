package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFindVanishedNeedsTwoScans(t *testing.T) {
	s := newSeen()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.Local)
	a := tripAt("A", now.Add(time.Hour))
	diffScan(s, nil, []Transport{a}, now, true, 30)

	if g := findVanished(s, nil, nil, now); len(g) != 0 {
		t.Fatal("mất 1 lần chưa được tính là biến mất")
	}
	// hiện lại → đếm lại từ đầu
	diffScan(s, nil, []Transport{a}, now, false, 30)
	if g := findVanished(s, nil, []Transport{a}, now); len(g) != 0 || s.Missing["A"] != 0 {
		t.Fatal("hiện lại phải xoá bộ đếm")
	}
	findVanished(s, nil, nil, now)
	g := findVanished(s, nil, nil, now)
	if len(g) != 1 || g[0].PartnerTransportationCode != "A" {
		t.Fatalf("mất 2 lần liên tiếp → biến mất: %+v", g)
	}
	if _, ok := s.Arrived["A"]; !ok {
		t.Error("đã xử lý phải chốt, không báo lại")
	}
	// GHN hiện lại chuyến → không báo 🔵 lại
	if d := diffScan(s, nil, []Transport{a}, now, false, 30); len(d.NewTrips) != 0 {
		t.Error("chuyến đã chốt không được báo 🔵 lại")
	}
}

func TestFindVanishedSkipsOtherDays(t *testing.T) {
	s := newSeen()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.Local)
	tomorrow := tripAt("T", now.Add(20*time.Hour))
	diffScan(s, nil, []Transport{tomorrow}, now, true, 30)
	findVanished(s, nil, nil, now)
	if g := findVanished(s, nil, nil, now); len(g) != 0 {
		t.Error("chuyến dự kiến ngày khác (đổi lịch) không tính là biến mất")
	}
}

func TestVanishOverHTTP(t *testing.T) {
	old := exeDir
	exeDir = t.TempDir()
	logPath = exeDir + "/test.log"
	defer func() { exeDir = old }()

	now := time.Now()
	eta := time.Date(now.Year(), now.Month(), now.Day(), 23, 0, 0, 0, time.Local) // trong hôm nay
	mk := func(code string) Transport {
		t := tripAt(code, eta)
		t.RouteScheduleName = "QB_HU_01"
		return t
	}
	var mu sync.Mutex
	up := []Transport{mk("FAST"), mk("HUY")}
	var depQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "transportation-transporting-list"):
			depQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"status":"OK","total":1,"data":[{"partnerTransportationCode":"FAST","arrivalTime":"2026-11-22T06:44:00+07:00","departureTime":1795302000000}]}`))
		case strings.Contains(r.URL.Path, "upcoming"):
			_ = json.NewEncoder(w).Encode(ArrivedResp{Status: "OK", Data: []ArrivedData{{List: up}}})
		default:
			_ = json.NewEncoder(w).Encode(ArrivedResp{Status: "OK", Data: []ArrivedData{{List: nil}}})
		}
	}))
	defer srv.Close()
	oa := apiBase
	apiBase = srv.URL
	defer func() { apiBase = oa }()

	exp := `{"expired":"` + now.Add(48*time.Hour).Format(time.RFC3339) + `"}`
	cfg := Config{Session: "h." + base64.RawURLEncoding.EncodeToString([]byte(exp)) + ".s",
		LocationID: "22127000", TelegramToken: "t", TelegramChat: "-1", AutoSession: boolPtr(false)}
	seen := newSeen()
	alertsClear()

	if _, _, _, ok := pollLive(&cfg, seen, true); !ok {
		t.Fatal("lần gốc lỗi")
	}
	mu.Lock()
	up = nil // cả 2 chuyến biến khỏi Sắp đến, không qua Đã đến
	mu.Unlock()
	_, _, msgs, _ := pollLive(&cfg, seen, false)
	if len(msgs) != 0 {
		t.Fatalf("mất 1 lần chưa báo: %v", texts(msgs))
	}
	_, _, msgs, _ = pollLive(&cfg, seen, false)
	all := strings.Join(texts(msgs), "\n---\n")
	if len(msgs) != 2 {
		t.Fatalf("phải 1 tin 🟠 + 1 tin ❔, được %d:\n%s", len(msgs), all)
	}
	if !strings.Contains(msgs[0].Text, "XE ĐÃ ĐẾN KHO") || msgs[0].Quiet ||
		!strings.Contains(msgs[0].Text, "Đến kho lúc: <b>22/11/2026 - 06:44") || !strings.Contains(msgs[0].Text, "Rời kho lúc:") {
		t.Errorf("tin 🟠 cho chuyến sót sai:\n%s", msgs[0].Text)
	}
	if !strings.Contains(msgs[1].Text, "HUY") || !msgs[1].Quiet {
		t.Errorf("tin ❔ phải im lặng và có mã HUY:\n%s", msgs[1].Text)
	}
	for _, want := range []string{"locationId=22127000", "locationType=GHN_HUB", "getTotal=true", "source=INSIDE_WEB_APP"} {
		if !strings.Contains(depQuery, want) {
			t.Errorf("tham số tab Đã rời kho thiếu %q: %s", want, depQuery)
		}
	}
	if seen.Day.Arrived["FAST"] == 0 && len(seen.Day.Arrived) != 1 {
		t.Errorf("tổng kết phải đếm chuyến sót: %+v", seen.Day.Arrived)
	}
	// vòng sau không báo lại
	_, _, msgs, _ = pollLive(&cfg, seen, false)
	if len(msgs) != 0 {
		t.Errorf("không được báo lại: %v", texts(msgs))
	}
}
