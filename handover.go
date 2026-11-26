package main

// Theo dõi bàn giao — lấy từ trạng thái ở tab "Đã đến kho" (ARRIVED → TRANSFERRING →
// TRANSFERRED) mà app vẫn quét, không gọi thêm GHN.
//   ⏳ Xe trong kho quá handoverMin phút chưa bàn giao xong → báo 1 lần (có chuông).
//   📊 Tổng kết: bàn giao xong X/Y · trung bình N phút/xe · xe chưa bàn giao xong.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// inKho: một xe đang ở tab Đã đến kho.
type inKho struct {
	ArrivedAt int64     `json:"arrivedAt"` // ms
	Status    string    `json:"status"`
	DoneAt    int64     `json:"doneAt"` // ms lúc thấy TRANSFERRED; -1 = đã xong từ trước khi app thấy
	Stuck     bool      `json:"stuck"`  // đã báo ⏳
	Last      Transport `json:"last"`
}

const statusDone = "TRANSFERRED"

func (c Config) handoverMin() int {
	if c.HandoverMin == nil {
		return 90
	}
	if *c.HandoverMin < 0 {
		return 0
	}
	return *c.HandoverMin
}

// trackHandover: hàm thuần — cập nhật trạng thái bàn giao, trả về xe vừa bị kẹt.
// silent = lần quét gốc: chỉ ghi nhớ.
func trackHandover(seen *SeenStore, arr []Transport, now time.Time, silent bool, handoverMin int) []Transport {
	ms := now.UnixMilli()
	present := map[string]bool{}
	var stuck []Transport
	for _, it := range arr {
		code := it.PartnerTransportationCode
		if code == "" {
			continue
		}
		present[code] = true
		k, known := seen.InKho[code]
		if !known {
			k.ArrivedAt = ms
			if t, ok := parseISO(it.ArrivalTime); ok {
				k.ArrivedAt = t.UnixMilli()
			}
			if it.StopSessionStatus == statusDone {
				k.DoneAt = -1 // xong trước khi app thấy → không tính thời gian
			}
		}
		k.Status, k.Last = it.StopSessionStatus, it
		if it.StopSessionStatus == statusDone && k.DoneAt == 0 {
			k.DoneAt = ms
		}
		if k.DoneAt != 0 {
			recordDone(seen, code, k)
		} else if handoverMin > 0 && !k.Stuck && ms-k.ArrivedAt >= int64(handoverMin)*60000 {
			k.Stuck = true
			if !silent {
				stuck = append(stuck, it)
				seen.Day.Stuck++
			}
		}
		seen.InKho[code] = k
	}
	// rời tab Đã đến kho = đã bàn giao xong và rời kho
	for code, k := range seen.InKho {
		if present[code] {
			continue
		}
		if k.DoneAt == 0 {
			k.DoneAt = ms
		}
		recordDone(seen, code, k)
		delete(seen.InKho, code)
	}
	return stuck
}

// recordDone: ghi thời gian bàn giao vào số liệu ngày (chỉ xe đến trong phiên này).
func recordDone(seen *SeenStore, code string, k inKho) {
	if _, today := seen.Day.Arrived[code]; !today {
		return
	}
	if _, done := seen.Day.Done[code]; done {
		return
	}
	mins := -1
	if k.DoneAt > 0 && k.ArrivedAt > 0 && k.DoneAt >= k.ArrivedAt {
		mins = int((k.DoneAt - k.ArrivedAt) / 60000)
	}
	seen.Day.Done[code] = mins
}

// stuckMsg: ⏳ một xe kẹt bàn giao.
func stuckMsg(it Transport, now time.Time, handoverMin int) string {
	msg := fmt.Sprintf("⏳ <b>XE TRONG KHO QUÁ %d PHÚT CHƯA BÀN GIAO XONG</b>\n", handoverMin) +
		tripHead(it) + "\n" +
		"🕐 Đến kho lúc: " + fmtTime(it.ArrivalTime)
	if t, ok := parseISO(it.ArrivalTime); ok {
		msg += " (đã " + durVi(now.Sub(t)) + ")"
	}
	msg += "\n📋 Bàn giao: " + esc(statusVi(it.StopSessionStatus)) + "\n" +
		driverLine(it) + "\n" + cargoLine(it)
	return msg
}

func stuckListMsg(list []Transport, now time.Time, handoverMin int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("⏳ <b>%d XE TRONG KHO QUÁ %d PHÚT CHƯA BÀN GIAO XONG</b>\n", len(list), handoverMin))
	for i, it := range list {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("… và %d xe khác\n", len(list)-i))
			break
		}
		d := "Đến kho lúc " + fmtTimeShort(it.ArrivalTime) + " · " + esc(statusVi(it.StopSessionStatus))
		if !blank(it.DriverPhone) {
			d += "\n   📞 SĐT: " + esc(strings.TrimSpace(it.DriverPhone))
		}
		b.WriteString(listItem(it, d))
	}
	return strings.TrimRight(b.String(), "\n")
}

// durVi: 1 giờ 25 phút / 45 phút.
func durVi(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	if m < 60 {
		return fmt.Sprintf("%d phút", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d giờ", m/60)
	}
	return fmt.Sprintf("%d giờ %d phút", m/60, m%60)
}

// handoverSummary: 2 dòng cho 📊 — "" nếu chưa có xe nào đến.
func handoverSummary(d DayStats) string {
	if len(d.Arrived) == 0 {
		return ""
	}
	done, sum, n := 0, 0, 0
	var pending []string
	for code := range d.Arrived {
		m, ok := d.Done[code]
		if !ok {
			pending = append(pending, code)
			continue
		}
		done++
		if m >= 0 {
			sum += m
			n++
		}
	}
	line := fmt.Sprintf("✅ Bàn giao xong: <b>%d/%d</b> xe", done, len(d.Arrived))
	if n > 0 {
		line += fmt.Sprintf(" · trung bình %d phút/xe", (sum+n/2)/n)
	}
	if len(pending) > 0 {
		sort.Strings(pending)
		shown := pending
		more := ""
		if len(shown) > 6 {
			shown, more = shown[:6], fmt.Sprintf(" +%d xe", len(pending)-6)
		}
		for i := range shown {
			shown[i] = esc(shown[i])
		}
		line += "\n⏳ Chưa bàn giao xong: " + strings.Join(shown, ", ") + more
	}
	return line
}
