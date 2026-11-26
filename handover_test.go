package main

import (
	"strings"
	"testing"
	"time"
)

func inWarehouse(code string, arrived time.Time, status string) Transport {
	t := trip(code, 10)
	t.ArrivalTime = arrived.Format(time.RFC3339)
	t.StopSessionStatus = status
	t.DriverPhone = "0966123132"
	return t
}

func TestHandoverStuckOnce(t *testing.T) {
	s := newSeen()
	t0 := time.Date(2026, 11, 22, 12, 55, 0, 0, time.Local)
	a := inWarehouse("A", t0, "ARRIVED")
	s.Day.Arrived["A"] = 10

	if st := trackHandover(s, []Transport{a}, t0.Add(30*time.Minute), false, 60); len(st) != 0 {
		t.Fatal("30 phút chưa kẹt")
	}
	a.StopSessionStatus = "TRANSFERRING"
	st := trackHandover(s, []Transport{a}, t0.Add(61*time.Minute), false, 60)
	if len(st) != 1 || s.Day.Stuck != 1 {
		t.Fatalf("quá 60 phút đang bàn giao → báo kẹt: %v", st)
	}
	if st := trackHandover(s, []Transport{a}, t0.Add(80*time.Minute), false, 60); len(st) != 0 {
		t.Error("chỉ báo kẹt 1 lần")
	}
	a.StopSessionStatus = "TRANSFERRED"
	trackHandover(s, []Transport{a}, t0.Add(85*time.Minute), false, 60)
	if s.Day.Done["A"] != 85 {
		t.Errorf("thời gian bàn giao phải 85 phút, được %d", s.Day.Done["A"])
	}
	trackHandover(s, nil, t0.Add(100*time.Minute), false, 60) // rời kho
	if _, ok := s.InKho["A"]; ok || s.Day.Done["A"] != 85 {
		t.Error("rời kho: xoá theo dõi, giữ thời gian đã ghi")
	}
}

func TestHandoverLeaveCountsAsDone(t *testing.T) {
	s := newSeen()
	t0 := time.Date(2026, 11, 22, 8, 0, 0, 0, time.Local)
	s.Day.Arrived["B"] = 5
	trackHandover(s, []Transport{inWarehouse("B", t0, "TRANSFERRING")}, t0.Add(20*time.Minute), false, 60)
	trackHandover(s, nil, t0.Add(40*time.Minute), false, 60) // biến khỏi Đã đến kho
	if s.Day.Done["B"] != 40 {
		t.Errorf("rời tab Đã đến = bàn giao xong (40 phút), được %d", s.Day.Done["B"])
	}
}

func TestHandoverSilentAndOff(t *testing.T) {
	s := newSeen()
	t0 := time.Now().Add(-3 * time.Hour)
	if st := trackHandover(s, []Transport{inWarehouse("C", t0, "ARRIVED")}, time.Now(), true, 60); len(st) != 0 {
		t.Error("lần gốc không báo")
	}
	s2 := newSeen()
	if st := trackHandover(s2, []Transport{inWarehouse("C", t0, "ARRIVED")}, time.Now(), false, 0); len(st) != 0 {
		t.Error("handoverMin=0 là tắt")
	}
	// đã bàn giao xong trước khi app thấy → không tính thời gian
	s3 := newSeen()
	s3.Day.Arrived["D"] = 1
	trackHandover(s3, []Transport{inWarehouse("D", t0, "TRANSFERRED")}, time.Now(), false, 60)
	if s3.Day.Done["D"] != -1 {
		t.Errorf("không rõ thời gian → -1, được %d", s3.Day.Done["D"])
	}
}

func TestHandoverSummaryLines(t *testing.T) {
	d := newDay()
	for _, c := range []string{"E1", "E2", "E3", "E261122XYZ99", "E261122ABC12"} {
		d.Arrived[c] = 10
	}
	d.Done["E1"], d.Done["E2"], d.Done["E3"] = 30, 45, -1
	h := plain(handoverSummary(d))
	want := "✅ Bàn giao xong: 3/5 xe · trung bình 38 phút/xe\n⏳ Chưa bàn giao xong: E261122ABC12, E261122XYZ99"
	if h != want {
		t.Errorf("tổng kết bàn giao:\n%s\nmuốn:\n%s", h, want)
	}
	all := newDay()
	all.Arrived["A"], all.Done["A"] = 1, 20
	if strings.Contains(handoverSummary(all), "Chưa bàn giao") {
		t.Error("xong hết thì không có dòng chưa bàn giao")
	}
	if handoverSummary(newDay()) != "" {
		t.Error("chưa có xe thì không có dòng bàn giao")
	}
	m := plain(summaryMsg(Config{LocationID: "1"}, DayStats{Pending: true, From: time.Now().Add(-8 * time.Hour).UnixMilli(),
		Arrived: d.Arrived, Done: d.Done, Late: 3, LastUp: 1}, time.Now()))
	if !strings.Contains(m, "Xe vào kho: 5") || !strings.Contains(m, "Bàn giao xong: 3/5 xe") || !strings.Contains(m, "Chưa bàn giao xong:") {
		t.Errorf("📊 vừa phải:\n%s", m)
	}
}

func TestStuckMessages(t *testing.T) {
	now := time.Date(2026, 11, 22, 14, 20, 0, 0, time.Local)
	a := inWarehouse("E261122BWA53MVY", now.Add(-85*time.Minute), "TRANSFERRING")
	m := plain(stuckMsg(a, now, 60))
	for _, want := range []string{"XE TRONG KHO QUÁ 60 PHÚT CHƯA BÀN GIAO XONG", "(đã 1 giờ 25 phút)", "Bàn giao: Đang bàn giao", "SĐT: 0966123132"} {
		if !strings.Contains(m, want) {
			t.Errorf("thiếu %q:\n%s", want, m)
		}
	}
	msgs := diffMessages(scanDiff{Stuck: []Transport{a, a}, StuckMin: 60}, now)
	if len(msgs) != 1 || msgs[0].Quiet || !strings.Contains(msgs[0].Text, "2 XE TRONG KHO QUÁ 60 PHÚT") {
		t.Errorf("nhiều xe kẹt → 1 tin có chuông: %v", msgs)
	}
}

func TestHandoverDefault90(t *testing.T) {
	if (Config{}).handoverMin() != 90 {
		t.Error("mặc định 90 phút")
	}
	if (Config{HandoverMin: intPtr(0)}).handoverMin() != 0 {
		t.Error("0 = tắt")
	}
}
