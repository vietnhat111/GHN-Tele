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
)

type scanHealth struct {
	at     time.Time
	result scanResult
}

var warehouseScan atomic.Pointer[scanHealth]

func recordScanHealth(result scanResult) {
	warehouseScan.Store(&scanHealth{at: time.Now(), result: result})
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
		label := map[scanResult]string{scanHealthy: "thành công", scanSession: "phiên GHN gặp lỗi", scanFailed: "không quét được", scanPartial: "chỉ lấy được dữ liệu xe đã đến"}
		b.WriteString("Lần thử gần nhất: " + healthTime(s.at) + " · " + label[s.result] + "\n")
	}
	b.WriteString(sessionLine(cfg, now) + "\n")
	state, writeFailed := outboxAccess(cfg, nil)
	b.WriteString(fmt.Sprintf("\n<b>Telegram</b> · %d phần tin đang chờ\n", len(state.Pending)))
	for _, t := range cfg.targets() {
		peer := peerFor(t)
		h := state.health(peer)
		b.WriteString("• " + targetLabel(cfg, peer) + ": " + healthTime(h.OKAt))
		if h.Error != "" {
			b.WriteString("\n  Lỗi lúc " + healthTime(h.ErrorAt) + " · " + esc(h.Error))
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
