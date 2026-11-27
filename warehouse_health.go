package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

type scanResult int

const (
	scanUnknown scanResult = iota
	scanHealthy
	scanSession
	scanFailed
	scanPartial
	scanStore
)

type scanHealth struct {
	at                                    time.Time
	result                                scanResult
	arrived, upcoming, pages, reconciling int
	timing                                scanTiming
}

var warehouseScan atomic.Pointer[scanHealth]

func recordScanHealth(result scanResult) {
	warehouseScan.Store(&scanHealth{at: time.Now(), result: result})
}

type scanTiming struct {
	arrived, upcoming, departed time.Duration
}

func observeScan(result scanResult, arr, up *ArrivedResp, seen *SeenStore, timing scanTiming) {
	h := &scanHealth{at: time.Now(), result: result, arrived: len(arr.list()), upcoming: len(up.list()), timing: timing}
	if arr != nil {
		h.pages += arr.Pages
	}
	if up != nil {
		h.pages += up.Pages
	}
	for _, count := range seen.Missing {
		if count == vanishAfter || count < 0 {
			h.reconciling++
		}
	}
	warehouseScan.Store(h)
}

func latestScanHealth() scanHealth {
	if h := warehouseScan.Load(); h != nil {
		return *h
	}
	return scanHealth{}
}

func healthTime(t time.Time) string {
	if t.IsZero() {
		return "chưa ghi nhận"
	}
	return t.Local().Format("02/01 15:04:05")
}

func targetLabel(cfg Config, peer telegramPeer) string {
	if peer.Chat == cfg.TelegramPersonal {
		return "Chat riêng"
	}
	if peer.Thread > 0 {
		return fmt.Sprintf("Nhóm kho · topic %d", peer.Thread)
	}
	return "Nhóm kho"
}

func warehouseHealthText(cfg Config, now time.Time) string {
	s := latestScanHealth()
	var b strings.Builder
	b.WriteString("<b>Kiểm tra máy kho</b> · v" + version + "\n")
	switch {
	case cfg.Paused:
		b.WriteString("Theo dõi: đang tạm dừng báo\n")
	case !inWatchWindow(now, cfg):
		b.WriteString("Theo dõi: ngoài khung giờ quét\n")
	default:
		b.WriteString("Theo dõi: trong khung giờ quét\n")
	}
	b.WriteString("Lần quét GHN đầy đủ: " + healthTime(lastScanTime()) + "\n")
	if !s.at.IsZero() {
		label := map[scanResult]string{scanHealthy: "thành công", scanSession: "phiên GHN gặp lỗi", scanFailed: "không quét được", scanPartial: "dữ liệu GHN chưa đầy đủ", scanStore: "chưa lưu được kết quả quét và tin báo"}
		b.WriteString("Lần thử gần nhất: " + healthTime(s.at) + " · " + label[s.result] + "\n")
	}
	if s.pages > 0 {
		fmt.Fprintf(&b, "GHN lần thử: %d xe đã đến · %d xe sắp đến · %d trang · %d chuyến chờ đối soát\n",
			s.arrived, s.upcoming, s.pages, s.reconciling)
	}
	if s.timing.arrived > 0 {
		fmt.Fprintf(&b, "Thời gian GHN · Đã đến %s", s.timing.arrived.Round(time.Millisecond))
		if s.timing.upcoming > 0 {
			fmt.Fprintf(&b, " · Sắp đến %s", s.timing.upcoming.Round(time.Millisecond))
		}
		if s.timing.departed > 0 {
			fmt.Fprintf(&b, " · Đã rời %s", s.timing.departed.Round(time.Millisecond))
		}
		b.WriteByte('\n')
	}
	b.WriteString(sessionLine(cfg, now) + "\n")
	state, writeFailed := outboxAccess(cfg, nil)
	b.WriteString(fmt.Sprintf("\n<b>Telegram</b> · %d phần tin đang chờ\n", len(state.Pending)))
	pendingByPeer := make(map[telegramPeer]int)
	for _, item := range state.Pending {
		pendingByPeer[item.Peer]++
	}
	for _, t := range cfg.targets() {
		peer := peerFor(t)
		h := state.health(peer)
		fmt.Fprintf(&b, "• %s: %s · %d phần chờ", targetLabel(cfg, peer), healthTime(h.OKAt), pendingByPeer[peer])
		if h.Error != "" {
			b.WriteString("\n  Lỗi lúc " + healthTime(h.ErrorAt) + " · " + esc(h.Error))
		}
		if h.Rejected > 0 {
			b.WriteString(fmt.Sprintf("\n  Telegram đã từ chối %d phần tin.", h.Rejected))
		}
		if h.Overflow > 0 {
			b.WriteString(fmt.Sprintf("\n  Có %d phần tin vượt sức chứa.", h.Overflow))
		}
		b.WriteString("\n")
	}
	if writeFailed {
		b.WriteString("⚠️ Hàng đợi chưa ghi được xuống đĩa.")
	}
	return strings.TrimSpace(b.String())
}

func showWarehouseHealth(cfg Config, t chatTarget, now time.Time) {
	sendBotButton(cfg, t, warehouseHealthText(cfg, now),
		tgMenuButton{Text: "Gửi tin thử", Data: "mobile:health:test"},
		tgMenuButton{Text: "Về bảng điều khiển", Data: "mobile:home"})
}

func sendWarehouseTest(cfg Config, t chatTarget) {
	var lines []string
	for _, dest := range cfg.targets() {
		peer := peerFor(dest)
		err := sendOutboxPart(cfg, peer, "🧪 <b>GHN Tele gửi tin thử</b>\nKiểm tra nơi nhận báo xe từ bảng điều khiển.", true)
		outboxAccess(cfg, func(s *deliveryState) { s.record(peer, err, time.Now()) })
		field := targetLabel(cfg, peer) + " · đã gửi"
		if err != nil {
			field = targetLabel(cfg, peer) + " · " + err.Error()
		}
		lines = append(lines, "• "+esc(field))
	}
	if len(lines) == 0 {
		lines = append(lines, "Máy kho chưa có nơi nhận tin.")
	}
	_ = sendTelegramAt(cfg, t, "<b>Kết quả gửi tin thử</b>\n"+strings.Join(lines, "\n"), true)
}
