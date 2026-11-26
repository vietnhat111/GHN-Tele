package main

import (
	"fmt"
	"strings"
	"time"
)

func sampleArrived() Transport {
	now := time.Now()
	return Transport{
		PartnerTransportationCode: "E26022805RYBMR",
		RouteScheduleName:         "MT_HU_01",
		TypeOps:                   "delivery",
		VehiclePlate:              "29H-087.45",
		Capacity:                  3500.0,
		DriverName:                "Quảng Ngọc Tâm Thành",
		DriverPhone:               "0965762888",
		ArrivalTime:               now.Format(time.RFC3339),
		ExpectedArrivalTime:       now.Add(-15 * time.Minute).Format(time.RFC3339),
		StopSessionStatus:         "ARRIVED",
		WillDropOff:               WillDropOff{NumOfPkg: 12, NumOfPkgBulky: 3, NumOfPkgFragile: 1},
		IncludeSortGroupCodes:     []string{"HUE-01"},
	}
}

func sampleUpcoming() Transport {
	now := time.Now()
	return Transport{
		PartnerTransportationCode: "E26022809KLPQ2",
		RouteScheduleName:         "QB_HU_01",
		TypeOps:                   "delivery_and_pickup",
		VehiclePlate:              "43H-051.88",
		Capacity:                  8000.0,
		DriverName:                "Trần Công Tấn",
		DriverPhone:               "0913020220",
		ExpectedArrivalTime:       now.Add(40 * time.Minute).Format(time.RFC3339),
		WillDropOff:               WillDropOff{NumOfPkg: 8, NumOfPkgBulky: 1, NumOfPkgFwd: 5},
		IncludeSortGroupCodes:     []string{"HUE-01", "HUE-02"},
	}
}

func printTg(title, body string) {
	fmt.Println()
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("📱 " + title)
	fmt.Println("──────────────────────────────────────────────")
	b := strings.ReplaceAll(body, "<b>", "𝗕 ")
	b = strings.ReplaceAll(b, "</b>", "")
	b = strings.ReplaceAll(b, "<code>", "")
	b = strings.ReplaceAll(b, "</code>", "")
	b = strings.ReplaceAll(b, "<i>", "")
	b = strings.ReplaceAll(b, "</i>", "")
	// link: <a href="url">chữ</a> → chữ [url]
	for {
		i := strings.Index(b, "<a href=\"")
		if i < 0 {
			break
		}
		j := strings.Index(b[i:], "\">")
		k := strings.Index(b[i:], "</a>")
		if j < 0 || k < 0 {
			break
		}
		url := b[i+9 : i+j]
		text := b[i+j+2 : i+k]
		b = b[:i] + text + " [" + url + "]" + b[i+k+4:]
	}
	fmt.Println(b)
}

func runDemo() {
	fmt.Println("==============================================")
	fmt.Println("  DEMO — " + appTitle + " v" + version)
	fmt.Println("==============================================")
	fmt.Println("Đúng những tin sẽ gửi vào nhóm.")

	up := sampleUpcoming()
	arr := sampleArrived()
	empty := up
	empty.WillDropOff = WillDropOff{}
	empty.DriverName, empty.DriverPhone, empty.VehiclePlate, empty.Capacity = "-", "-", "-", nil
	cfg := Config{LocationID: "2212700"}

	printTg("Bật theo dõi", startMsg(cfg, []Transport{arr}, []Transport{up}))

	printTg("Lần 1 — một xe xuất hiện (chưa có kiện)", firstMsg(empty))

	u2 := up
	u2.PartnerTransportationCode = "E26022901AB12"
	u3 := up
	u3.PartnerTransportationCode = "E26022907CD34"
	printTg("Nhiều xe xuất hiện cùng lúc → gộp 1 tin", newTripsMsg([]Transport{empty, u2, u3}))

	up1 := up
	printTg("Lần 2 — số kiện thay đổi (0 → 14)", updateMsg(up1, pkgSig(WillDropOff{}), pkgSig(up1.WillDropOff)))

	up2 := up1
	up2.PartnerTransportationCode = "E26022901AB12"
	up2.WillDropOff = WillDropOff{NumOfPkg: 14, NumOfPkgBulky: 2, NumOfPkgFwd: 5, NumOfPkgFragile: 1}
	printTg("Đổi tiếp (14 → 21)", updateMsg(up2, pkgSig(up1.WillDropOff), pkgSig(up2.WillDropOff)))

	u4 := up
	u4.PartnerTransportationCode = "E26022907CD34"
	printTg("Nhiều xe đổi kiện cùng lúc → gộp 1 tin", updatesMsg([]pkgChange{
		{It: up2, Old: pkgSig(up1.WillDropOff), New: pkgSig(up2.WillDropOff)},
		{It: u4, Old: pkgSig(WillDropOff{NumOfPkg: 6}), New: pkgSig(WillDropOff{NumOfPkg: 2})},
	}))

	printTg("Xe vào kho — chốt, không báo nữa", arrivedMsg(arr))

	fast := up
	fast.PartnerTransportationCode = "E2611227CXE0Q0Y"
	printTg("Xe vào rồi rời kho giữa 2 lần quét → tra tab Đã rời kho, vẫn báo 🟠", departedMsg(departedHit{It: fast, Dep: DepartedTrip{
		ArrivalTime:   flexTime(time.Now().Add(-25 * time.Minute).Format(time.RFC3339)),
		DepartureTime: flexTime(time.Now().Add(-8 * time.Minute).Format(time.RFC3339)),
	}}))
	gone := up
	gone.PartnerTransportationCode = "E261121IBXWEOBS"
	printTg("Chuyến biến mất, không thấy ở đâu (huỷ / đổi kho) — tin im lặng", goneMsg([]Transport{gone}))

	a2, a3, a4 := arr, arr, arr
	a2.PartnerTransportationCode, a3.PartnerTransportationCode, a4.PartnerTransportationCode = "E26022806AA11", "E26022806BB22", "E26022806CC33"
	printTg("Từ 4 xe vào kho cùng lúc (vd. sáng ra) → gộp 1 tin", arrivedListMsg([]Transport{arr, a2, a3, a4}))

	late := up
	late.PartnerTransportationCode = "E26022811TRE9"
	late.ExpectedArrivalTime = time.Now().Add(-45 * time.Minute).Format(time.RFC3339)
	printTg("Xe trễ quá 30 phút mà chưa vào kho — báo 1 lần", lateMsg(late, time.Now(), "https://maps.google.com/?q=16.463700,107.590900"))

	setSnapshot([]Transport{arr}, []Transport{up, late}, time.Now().Add(-2*time.Minute))
	printTg("Lệnh /xe — ai trong nhóm cũng gõ được (trả lời im lặng)", statusXe(cfg, time.Now()))
	setSnapshot(nil, nil, time.Time{})
	printTg("Lệnh /lenh — danh sách lệnh", helpText())
	printTg("Lệnh /gioithieu — giới thiệu cho cả nhóm (nên ghim)", introText(cfg))

	printTg("Bật lại khi còn dữ liệu cũ (≤ 24 giờ)", resumeMsg(cfg, time.Now().Add(-40*time.Minute), 2, []Transport{arr}, []Transport{up}))

	stuck := arr
	stuck.StopSessionStatus = "TRANSFERRING"
	stuck.ArrivalTime = time.Now().Add(-95 * time.Minute).Format(time.RFC3339)
	printTg("Xe trong kho quá 90 phút chưa bàn giao xong — báo 1 lần", stuckMsg(stuck, time.Now(), 90))

	printTg("Hết khung giờ — tổng kết ngày", summaryMsg(cfg, DayStats{
		Pending: true, From: time.Now().Add(-16 * time.Hour).UnixMilli(),
		Arrived: map[string]int{"E26112201AA": 120, "E26112202BB": 85, "E26112203CC": 150, "E261122XYZ99": 30, "E261122ABC12": 27},
		Done:    map[string]int{"E26112201AA": 35, "E26112202BB": 42, "E26112203CC": 37},
		Late:    3, LastUp: 1, LateNow: 1,
	}, time.Now()))

	printTg("Đầu ca 11:00-14:00 — lặp lại cho ca mới", shiftMsg(cfg, "11:00-14:00", []Transport{arr}, []Transport{up1, up2}))

	fmt.Println()
	fmt.Println("──────────────────────────────────────────────")
	fmt.Println("Vòng đời mỗi chuyến:")
	fmt.Println("  1. Xuất hiện ở Sắp đến → 🔵 (kể cả 0 kiện)")
	fmt.Println("  2. Mỗi lần số kiện đổi → 🔄 (kèm chênh lệch)")
	fmt.Println("  3. Vào kho → 🟠 rồi DỪNG, không báo lại")
	fmt.Println("  4. Sang ca mới → tóm tắt đầu ca, chu kỳ lặp lại")
	fmt.Println("  + Trễ quá 30 phút → ⏰ một lần • hết khung giờ → 📊 tổng kết")
	waitEnter()
}
