package main

import (
	"strings"
	"testing"
	"time"
)

func tripAt(code string, eta time.Time) Transport {
	t := trip(code, 3)
	t.ExpectedArrivalTime = eta.Format(time.RFC3339)
	t.DriverPhone = "0900000000"
	return t
}

func TestLateAlertOnce(t *testing.T) {
	s := newSeen()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.Local)
	a := tripAt("A", now.Add(10*time.Minute)) // chưa trễ
	diffScan(s, nil, []Transport{a}, now, true, 30)

	// 40 phút sau giờ dự kiến → báo 1 lần
	later := now.Add(50 * time.Minute)
	d := diffScan(s, nil, []Transport{a}, later, false, 30)
	if len(d.Late) != 1 {
		t.Fatalf("phải báo trễ A: %+v", d)
	}
	if s.Day.Late != 1 || s.Day.LateNow != 1 {
		t.Errorf("đếm trễ sai: %+v", s.Day)
	}
	d = diffScan(s, nil, []Transport{a}, later.Add(10*time.Minute), false, 30)
	if len(d.Late) != 0 {
		t.Error("không báo trễ 2 lần")
	}
	// vào kho → xoá cờ trễ
	diffScan(s, []Transport{a}, nil, later, false, 30)
	if _, ok := s.Late["A"]; ok {
		t.Error("vào kho phải xoá cờ trễ")
	}
}

func TestLateNewTripNoDoubleMsg(t *testing.T) {
	s := newSeen()
	now := time.Now()
	diffScan(s, nil, nil, now, true, 30)
	// xe xuất hiện lần đầu đã trễ 1 giờ: chỉ 🔵 (tin 🔵 có ghi "trễ"), không thêm ⏰
	d := diffScan(s, nil, []Transport{tripAt("B", now.Add(-time.Hour))}, now, false, 30)
	if len(d.NewTrips) != 1 || len(d.Late) != 0 {
		t.Errorf("xe mới đã trễ: chỉ 1 tin 🔵: %+v", d)
	}
	if !strings.Contains(plain(firstMsgAt(d.NewTrips[0], now)), "Đã trễ 1 giờ so với dự kiến") {
		t.Error("tin 🔵 phải ghi đang trễ")
	}
}

func TestLateOff(t *testing.T) {
	if isLate(tripAt("A", time.Now().Add(-5*time.Hour)), time.Now(), 0) {
		t.Error("lateMin=0 là tắt")
	}
}

func TestArrivedGrouping(t *testing.T) {
	now := time.Now()
	three := diffMessages(scanDiff{Arrived: []Transport{trip("A", 1), trip("B", 2), trip("C", 3)}}, now)
	if len(three) != 3 {
		t.Errorf("3 xe vẫn báo chi tiết từng xe, được %d tin", len(three))
	}
	many := []Transport{trip("A", 1), trip("B", 2), trip("C", 3), trip("D", 4)}
	msgs := diffMessages(scanDiff{Arrived: many}, now)
	if len(msgs) != 1 {
		t.Fatalf("4 xe phải gộp 1 tin, được %d", len(msgs))
	}
	m := plain(msgs[0].Text)
	if !strings.Contains(m, "4 XE ĐÃ ĐẾN KHO") || !strings.Contains(m, "Tổng cần nhận: 10 kiện") {
		t.Errorf("tin gộp sai:\n%s", m)
	}
}

func TestCanResume(t *testing.T) {
	now := time.Now()
	s := newSeen()
	if canResume(s, now.Add(-time.Hour), now) {
		t.Error("seen rỗng → không tiếp tục")
	}
	s.Sig["A"] = "1 0 0 0"
	if !canResume(s, now.Add(-10*time.Hour), now) {
		t.Error("quét cách 10 giờ (qua đêm) → tiếp tục, báo bù")
	}
	if canResume(s, now.Add(-30*time.Hour), now) {
		t.Error("quá 24 giờ → ghi nhớ lại từ đầu")
	}
	if canResume(s, time.Time{}, now) {
		t.Error("chưa quét lần nào → không tiếp tục")
	}
}

func TestSummaryDue(t *testing.T) {
	day := Config{WatchFrom: "05:00", WatchTo: "21:00"}
	at := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.Local) }
	st := DayStats{Pending: true, From: at(22, 5).UnixMilli()}

	if summaryDue(day, st, at(22, 12), true) {
		t.Error("giữa khung không tổng kết")
	}
	if !summaryDue(day, st, at(22, 21), false) {
		t.Error("hết khung phải tổng kết")
	}
	if summaryDue(day, DayStats{}, at(22, 21), false) {
		t.Error("chưa quét gì thì không tổng kết")
	}
	if !summaryDue(day, st, at(23, 5), true) {
		t.Error("máy tắt qua đêm, sáng bật lại trong khung → gửi tổng kết hôm qua")
	}

	full := Config{WatchFrom: "00:00", WatchTo: "23:59"}
	if !fullDay(full) || fullDay(day) {
		t.Fatal("fullDay sai")
	}
	if summaryDue(full, st, at(22, 23), true) {
		t.Error("cả ngày: chưa qua ngày thì chưa tổng kết")
	}
	if !summaryDue(full, st, at(23, 0), true) {
		t.Error("cả ngày: qua nửa đêm thì tổng kết")
	}
}

func TestSummaryMsg(t *testing.T) {
	d := DayStats{Pending: true, From: time.Now().Add(-8 * time.Hour).UnixMilli(),
		Arrived: map[string]int{"A": 10, "B": 5}, Late: 2, LastUp: 3, LateNow: 1}
	m := plain(summaryMsg(Config{LocationID: "1"}, d, time.Now()))
	for _, want := range []string{"TỔNG KẾT", "Xe vào kho: 2", "📦 15 kiện", "Đã báo trễ: 2", "Còn ở Sắp đến: 3 (trễ: 1)", "quét lại lúc 05:00"} {
		if !strings.Contains(m, want) {
			t.Errorf("thiếu %q:\n%s", want, m)
		}
	}
	off := Config{LocationID: "1", LateMin: intPtr(0)}
	if strings.Contains(plain(summaryMsg(off, d, time.Now())), "báo trễ") {
		t.Error("tắt báo trễ thì không in dòng trễ")
	}
}

func TestNextSleep(t *testing.T) {
	now := time.Now()
	cfg := Config{LiveMin: intPtr(5)}
	if got := nextSleep(cfg, []Transport{tripAt("A", now.Add(2*time.Hour))}, now); got != 5*time.Minute {
		t.Errorf("không có xe gần → 5 phút, được %v", got)
	}
	if got := nextSleep(cfg, []Transport{tripAt("A", now.Add(20*time.Minute))}, now); got != 2*time.Minute {
		t.Errorf("xe tới trong 20 phút → quét nhanh 2 phút, được %v", got)
	}
	if got := nextSleep(cfg, []Transport{tripAt("A", now.Add(-3*time.Hour))}, now); got != 5*time.Minute {
		t.Errorf("xe trễ quá 1 giờ không giữ quét nhanh mãi, được %v", got)
	}
	off := Config{LiveMin: intPtr(5), FastMin: intPtr(0)}
	if got := nextSleep(off, []Transport{tripAt("A", now.Add(5*time.Minute))}, now); got != 5*time.Minute {
		t.Errorf("fastMin=0 là tắt, được %v", got)
	}
	slow := Config{LiveMin: intPtr(2), FastMin: intPtr(3)}
	if slow.fastMin() != 0 {
		t.Error("fastMin không được chậm hơn liveMin")
	}
}

func TestShiftsOff(t *testing.T) {
	cfg := Config{Shifts: "tắt"}
	if !cfg.shiftsOff() || cfg.shiftsRaw() != "tắt" {
		t.Error("'tắt' phải tắt ca")
	}
	if _, ok := currentShift(time.Date(2026, 11, 22, 6, 0, 0, 0, time.Local), cfg); ok {
		t.Error("tắt ca thì không có ca nào")
	}
	if (Config{}).shiftsOff() {
		t.Error("trống = ca mặc định, không phải tắt")
	}
}

func TestLateMsgs(t *testing.T) {
	now := time.Now()
	a := tripAt("A", now.Add(-45*time.Minute))
	if m := plain(lateMsg(a, now, "")); !strings.Contains(m, "XE TRỄ 45 PHÚT") || !strings.Contains(m, "0900000000") {
		t.Errorf("tin trễ sai:\n%s", m)
	}
	m := plain(lateListMsg([]Transport{a, tripAt("B", now.Add(-time.Hour))}, now, nil))
	if !strings.Contains(m, "2 XE TRỄ") || !strings.Contains(m, "trễ 1 giờ") {
		t.Errorf("tin trễ gộp sai:\n%s", m)
	}
}

func TestBlankDriverAndPkgDetail(t *testing.T) {
	it := Transport{PartnerTransportationCode: "E1", DriverName: "-", DriverPhone: "-", VehiclePlate: "-", Capacity: "-"}
	if got := driverLine(it); got != "👤 Tài xế, xe: chưa gán" {
		t.Errorf("chưa gán phải ghi rõ, được %q", got)
	}
	msg := firstMsg(it)
	if strings.Contains(msg, "👤 -") || strings.Contains(msg, "🚛 -") || strings.Contains(msg, "- kg") {
		t.Errorf("không được in dấu '-' của GHN:\n%s", msg)
	}
	it.VehiclePlate = "43H-051.88"
	if got := driverLine(it); got != "👤 Tài xế: chưa gán\n🚛 Xe: 43H-051.88" {
		t.Errorf("chỉ có xe: %q", got)
	}
	c := plain(cargoLine(Transport{WillDropOff: WillDropOff{NumOfPkg: 8, NumOfPkgBulky: 1, NumOfPkgFwd: 5}}))
	if c != "📦 Cần nhận: 14 kiện\n  • Bao cần rã: 8\n  • Kiện cồng kềnh: 1\n  • Bao chuyển tiếp: 5" {
		t.Errorf("chi tiết kiện: %q", c)
	}
}

func TestListsShowPkgBreakdown(t *testing.T) {
	now := time.Now()
	a := tripAt("E261126WRE4VF6I", now.Add(time.Hour))
	a.RouteScheduleName = "QB_HU_DN_01"
	a.WillDropOff = WillDropOff{NumOfPkg: 2, NumOfPkgBulky: 1}
	z := tripAt("E261127P0703U36", now.Add(8*time.Hour))
	z.WillDropOff = WillDropOff{}
	in := tripAt("E261128IN", now)
	in.ArrivalTime = now.Add(-20 * time.Minute).Format(time.RFC3339)
	in.StopSessionStatus = "TRANSFERRING"
	in.WillDropOff = WillDropOff{NumOfPkg: 198, NumOfPkgBulky: 19}
	m := plain(shiftMsg(Config{LocationID: "1"}, "05:00-08:00", []Transport{in}, []Transport{a, z}))
	for _, want := range []string{
		"Đang trong kho: 1 xe · 217 kiện\n   📦 Bao cần rã 198 · Kiện cồng kềnh 19",
		"Sắp đến kho: 2 xe · 3 kiện\n   📦 Bao cần rã 2 · Kiện cồng kềnh 1",
		"• E261126WRE4VF6I — QB_HU_DN_01\n   Dự kiến đến",
		"3 kiện\n   📦 Bao cần rã 2 · Kiện cồng kềnh 1",
		"Đang bàn giao",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("tin đầu ca thiếu %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "E261127P0703U36 — ") && strings.Count(m, "📦") != 3 {
		t.Errorf("xe 0 kiện không có dòng 📦:\n%s", m)
	}
	if n := plain(newTripsMsgAt([]Transport{a, z}, now)); !strings.Contains(n, "📦 Bao cần rã 2 · Kiện cồng kềnh 1") {
		t.Errorf("tin gộp xe mới thiếu chi tiết:\n%s", n)
	}
}
