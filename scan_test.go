package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func trip(code string, pkg int) Transport {
	return Transport{PartnerTransportationCode: code, WillDropOff: WillDropOff{NumOfPkg: pkg}}
}

func TestDiffScanLifecycle(t *testing.T) {
	s := newSeen()
	now := time.Now()

	// lần gốc: chỉ ghi nhớ, không báo gì
	d := diffScan(s, []Transport{trip("IN", 3)}, []Transport{trip("A", 0)}, now, true, 0)
	if len(d.Arrived)+len(d.NewTrips)+len(d.Changes) != 0 {
		t.Fatalf("lần gốc không được báo: %+v", d)
	}

	// xe mới B xuất hiện, A chưa đổi
	d = diffScan(s, []Transport{trip("IN", 3)}, []Transport{trip("A", 0), trip("B", 0)}, now, false, 0)
	if len(d.NewTrips) != 1 || d.NewTrips[0].PartnerTransportationCode != "B" || len(d.Changes) != 0 || len(d.Arrived) != 0 {
		t.Fatalf("chỉ B là mới: %+v", d)
	}

	// A đổi kiện 0 → 5
	d = diffScan(s, []Transport{trip("IN", 3)}, []Transport{trip("A", 5), trip("B", 0)}, now, false, 0)
	if len(d.Changes) != 1 || d.Changes[0].Old != "0 0 0 0" || d.Changes[0].New != "5 0 0 0" {
		t.Fatalf("A phải đổi kiện: %+v", d)
	}

	// A vào kho → báo đến 1 lần, không còn chữ ký
	d = diffScan(s, []Transport{trip("IN", 3), trip("A", 5)}, []Transport{trip("B", 0)}, now, false, 0)
	if len(d.Arrived) != 1 || d.Arrived[0].PartnerTransportationCode != "A" {
		t.Fatalf("A phải báo đến: %+v", d)
	}
	if _, ok := s.Sig["A"]; ok {
		t.Error("A vào kho rồi phải xoá chữ ký")
	}

	// vòng sau: A vẫn ở Đã đến → không báo lại
	d = diffScan(s, []Transport{trip("IN", 3), trip("A", 5)}, nil, now, false, 0)
	if len(d.Arrived) != 0 {
		t.Errorf("không được báo đến 2 lần: %+v", d)
	}
}

func TestDiffScanArrivedNotRenotified(t *testing.T) {
	s := newSeen()
	now := time.Now()
	diffScan(s, []Transport{trip("A", 2)}, nil, now, true, 0)
	// A rơi khỏi danh sách Đã đến (vd. quá 300 dòng) nhưng GHN còn để ở Sắp đến
	d := diffScan(s, nil, []Transport{trip("A", 2)}, now, false, 0)
	if len(d.NewTrips) != 0 {
		t.Errorf("chuyến đã báo vào kho không được báo 🔵 lại: %+v", d)
	}
}

func TestCleanSeenDropsVanishedTrips(t *testing.T) {
	s := newSeen()
	old := time.Now().AddDate(0, 0, -5).UnixMilli()
	s.Sig["HUY"] = "1 0 0 0"
	s.SigAt["HUY"] = old
	s.Sig["CON"] = "1 0 0 0"
	s.SigAt["CON"] = time.Now().UnixMilli()
	cleanSeen(s)
	if _, ok := s.Sig["HUY"]; ok {
		t.Error("chuyến biến mất quá 3 ngày phải bị dọn")
	}
	if _, ok := s.Sig["CON"]; !ok {
		t.Error("chuyến còn thấy không được dọn")
	}
}

func TestLoadSeenLegacy(t *testing.T) {
	old := exeDir
	exeDir = t.TempDir()
	defer func() { exeDir = old }()
	_ = os.WriteFile(filepath.Join(exeDir, "seen.json"), []byte(`{"arrived":{},"sig":{"X":"1 0 0 0"}}`), 0600)
	s := loadSeen()
	if _, ok := s.SigAt["X"]; !ok {
		t.Error("seen.json bản cũ phải được gán sigAt")
	}
	cleanSeen(s)
	if _, ok := s.Sig["X"]; !ok {
		t.Error("chữ ký cũ không được xoá ngay khi nâng cấp")
	}
}

func TestDiffMessagesGrouping(t *testing.T) {
	now := time.Now()
	d := scanDiff{
		NewTrips: []Transport{trip("A", 0), trip("B", 1)},
		Changes: []pkgChange{
			{It: trip("C", 4), Old: "1 0 0 0", New: "4 0 0 0"},
			{It: trip("D", 0), Old: "3 0 0 0", New: "0 0 0 0"},
		},
	}
	msgs := diffMessages(d, now)
	if len(msgs) != 2 {
		t.Fatalf("2 xe mới + 2 đổi kiện phải là 2 tin, được %d", len(msgs))
	}
	up := plain(msgs[1].Text)
	for _, want := range []string{"CẬP NHẬT SỐ KIỆN — 2 CHUYẾN", "• C\n   Cần nhận: 1 → 4 kiện (+3)", "• D\n   Cần nhận: 3 → 0 kiện (-3)"} {
		if !strings.Contains(up, want) {
			t.Errorf("thiếu %q:\n%s", want, up)
		}
	}
	one := diffMessages(scanDiff{Changes: d.Changes[:1]}, now)
	if len(one) != 1 || !strings.Contains(plain(one[0].Text), "Bao cần rã: 1 → 4") {
		t.Errorf("một chuyến đổi thì dùng tin chi tiết: %v", one)
	}
}

func TestEtaLeft(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.Local)
	at := func(m int) string { return now.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	cases := map[string]string{
		at(40):  "còn 40 phút",
		at(65):  "còn 1 giờ 5 phút",
		at(120): "còn 2 giờ",
		at(-15): "trễ 15 phút",
		at(0):   "sắp tới nơi",
		"":      "",
	}
	for in, want := range cases {
		if got := etaLeft(in, now); got != want {
			t.Errorf("etaLeft(%q) = %q, muốn %q", in, got, want)
		}
	}
	if got := etaShort(at(65), now); got != "1h05" {
		t.Errorf("etaShort = %q", got)
	}
	if got := etaShort(at(-7), now); got != "trễ 7'" {
		t.Errorf("etaShort trễ = %q", got)
	}
	it := sampleUpcoming()
	it.ExpectedArrivalTime = at(40)
	if !strings.Contains(plain(firstMsgAt(it, now)), "⏳ Còn 40 phút nữa") {
		t.Errorf("tin 🔵 thiếu thời gian còn lại:\n%s", firstMsgAt(it, now))
	}
}

func TestAlertThrottle(t *testing.T) {
	alertsClear()
	now := time.Now()
	if !alertDue("session", now) {
		t.Fatal("lần đầu phải gửi")
	}
	if alertDue("session", now.Add(10*time.Minute)) {
		t.Error("trong 1 giờ không gửi lại")
	}
	if !alertDue("ghn", now) {
		t.Error("loại khác vẫn gửi")
	}
	if !alertDue("session", now.Add(61*time.Minute)) {
		t.Error("quá 1 giờ phải gửi lại")
	}
	if !alertsClear() {
		t.Error("có lỗi trước đó → clear phải trả true")
	}
	if alertsClear() {
		t.Error("không còn lỗi → false")
	}
}
