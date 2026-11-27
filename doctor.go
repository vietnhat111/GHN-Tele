package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// doctor: in ra toàn bộ trạng thái thật của app (cấu hình, phiên, nơi nhận tin,
// tiến trình theo dõi, khung giờ, task Windows, log) để biết chính xác vì sao
// không có tin — thay vì đoán.
func doctor() {
	fmt.Println("==============================================")
	fmt.Println("  KIỂM TRA TOÀN BỘ — ghn-tele v" + version)
	fmt.Println("==============================================")
	fmt.Println("EXE     :", exePath())
	fmt.Println("Thư mục :", exeDir)
	fmt.Println("Hệ điều hành:", runtime.GOOS+"/"+runtime.GOARCH)
	fmt.Println()

	cfg := loadCfg()

	// --- cấu hình
	var miss []string
	if cfg.LocationID == "" {
		miss = append(miss, "mã kho")
	}
	if cfg.TelegramToken == "" {
		miss = append(miss, "bot token")
	}
	if cfg.TelegramChat == "" {
		miss = append(miss, "chat id")
	}
	if len(miss) > 0 {
		fmt.Println("❌ Cấu hình THIẾU:", strings.Join(miss, ", "))
	} else {
		fmt.Println("✅ Cấu hình: đủ (kho", cfg.LocationID+", nhận tin "+cfg.TelegramChat+")")
	}

	// --- session
	if exp, ok := jwtExpiredAt(cfg.Session); ok {
		left := time.Until(exp)
		switch {
		case left <= 0:
			fmt.Printf("❌ SESSION đã hết hạn lúc %s\n", exp.Local().Format("02/01 15:04"))
		case left < 2*time.Hour:
			fmt.Printf("⚠️ SESSION còn %.1f giờ (sắp hết lúc %s)\n", left.Hours(), exp.Local().Format("02/01 15:04"))
		default:
			fmt.Printf("✅ SESSION còn %.1f giờ\n", left.Hours())
		}
	} else {
		fmt.Println("• SESSION: không đọc được hạn (dùng token lạ hoặc chưa có)")
	}
	if cfg.autoSessionOn() {
		cands := browserSessionCandidates(cfg)
		if len(cands) == 0 {
			fmt.Println("⚠️ Tự lấy SESSION: bật nhưng KHÔNG thấy profile trình duyệt nào có nhanh.ghn.vn")
			fmt.Println("   → đăng nhập nhanh.ghn.vn bằng Chrome trên máy này, hoặc dán SESSION tay (mục 1)")
		} else {
			fmt.Printf("✅ Tự lấy SESSION: thấy %d phiên trong trình duyệt (mới nhất: %s/%s)\n",
				len(cands), cands[0].browser, cands[0].profile)
		}
	} else {
		fmt.Println("• Tự lấy SESSION: tắt")
	}

	// --- telegram
	if cfg.TelegramToken != "" {
		if u := tgBotUsername(cfg.TelegramToken); u != "" {
			fmt.Println("✅ Telegram bot: @" + u)
		} else {
			fmt.Println("❌ Telegram bot: không gọi được getMe (sai token hoặc không có mạng)")
		}
	}

	// --- tiến trình theo dõi
	running, pid, since := watchRunning()
	if running {
		fmt.Printf("🟢 Theo dõi: ĐANG CHẠY — PID %d (từ %s)\n", pid, since)
		if t := lastScanTime(); !t.IsZero() {
			fmt.Printf("   Lần quét cuối: %s\n", t.Format("02/01 15:04:05"))
		} else {
			fmt.Println("   Chưa quét lần nào trong phiên này")
		}
	} else {
		fmt.Println("🔴 Theo dõi: CHƯA CHẠY — không có tiến trình nào quét, nên không có tin tự động")
		fmt.Println("   → mở app, chọn 5 (chạy ngay) hoặc 6 (tự chạy khi đăng nhập)")
		if cfg.liveOn() {
			fmt.Printf("   (config đang bật %d phút nhưng chưa có tiến trình)\n", cfg.liveMin())
		}
	}

	// --- khung giờ
	now := time.Now()
	inWin := inWatchWindow(now, cfg)
	fmt.Printf("Khung giờ theo dõi: %s–%s\n", cfg.watchFrom(), cfg.watchTo())
	if inWin {
		fmt.Printf("   Bây giờ %s → TRONG khung, sẽ quét.\n", now.Format("15:04"))
	} else {
		fmt.Printf("   Bây giờ %s → NGOÀI khung, app nghỉ đến %s.\n", now.Format("15:04"), cfg.watchFrom())
		fmt.Println("   Muốn báo cả đêm: mục 8 đổi khung thành 00:00–23:59.")
	}
	fmt.Printf("Nhận tin: %s (nhóm %s • riêng %s)\n", cfg.groupLabel(), orDash(cfg.TelegramChat), orDash(cfg.TelegramPersonal))
	fmt.Printf("Ca: %s\n", cfg.shiftsRaw())
	if cfg.Paused {
		fmt.Println("Báo xe: ⏸ TẠM DỪNG (lệnh /tat) — /bat hoặc mục 5 để bật")
	}
	fmt.Printf("Cập nhật: kho GitHub %s · tự cập nhật %s\n", cfg.updateRepo(), map[bool]string{true: "BẬT (ngoài giờ)", false: "TẮT"}[cfg.autoUpdateOn()])
	if st := loadUpdateState(); st.To != "" {
		fmt.Printf("Lần cập nhật gần nhất: v%s → v%s (%s)\n", st.From, st.To, time.UnixMilli(st.At).Format("02/01 15:04"))
	}
	pin := "đã đổi"
	if cfg.PinHash == "" {
		pin = "MẶC ĐỊNH 1234 — nên đổi ở mục 1"
	}
	fmt.Printf("PIN dừng/sửa: %s\n", pin)
	switch {
	case !cfg.autoStartOn():
		fmt.Println("Tự bật lại: TẮT (đã gỡ tự chạy ở mục 6)")
	case stopIntended():
		fmt.Println("Tự bật lại: TẠM NGỪNG — đã dừng bằng PIN, bật lại ở mục 5")
	default:
		fmt.Println("Tự bật lại: BẬT — Task Scheduler kiểm tra mỗi 5 phút")
	}
	fmt.Printf("Quét: %d phút • nhanh %s khi có xe tới trong 30' • báo trễ: %s\n",
		cfg.liveMin(), minOrOff(cfg.fastMin()), minOrOff(cfg.lateMin()))
	if s := loadSeen(); s.Day.Pending {
		fmt.Printf("Tổng kết đang gom: %d xe vào kho từ %s (gửi khi hết khung)\n",
			len(s.Day.Arrived), time.UnixMilli(s.Day.From).Format("02/01 15:04"))
	}
	if t := lastScanTime(); !t.IsZero() {
		if canResume(loadSeen(), t, now) {
			fmt.Println("Bật lại bây giờ: TIẾP TỤC (báo bù thay đổi trong lúc tắt)")
		} else {
			fmt.Println("Bật lại bây giờ: BẮT ĐẦU MỚI (lần quét đầu chỉ ghi nhớ)")
		}
	}

	// --- task Windows
	if runtime.GOOS == "windows" {
		out, err := exec.Command("schtasks", "/Query", "/TN", "GHN-DenKho-Watch", "/FO", "LIST").CombinedOutput()
		if err != nil {
			fmt.Println("Task GHN-DenKho-Watch: KHÔNG có (chưa cài mục 6)")
		} else {
			state := ""
			for _, line := range strings.Split(string(out), "\n") {
				l := strings.TrimSpace(line)
				if strings.HasPrefix(l, "Status:") || strings.Contains(l, "Trạng thái:") || strings.HasPrefix(l, "Task To Run:") || strings.Contains(l, "Task To Run") {
					state += "   " + l + "\n"
				}
			}
			fmt.Println("Task GHN-DenKho-Watch:")
			if state == "" {
				fmt.Printf("   %s", strings.TrimSpace(string(out)))
			} else {
				fmt.Print(state)
			}
		}
	}

	// --- log
	fmt.Println()
	fmt.Println("--- 12 dòng log cuối ---")
	if b, err := os.ReadFile(filepath.Join(exeDir, logName)); err == nil {
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(lines) > 12 {
			lines = lines[len(lines)-12:]
		}
		for _, l := range lines {
			fmt.Println("  " + l)
		}
	} else {
		fmt.Println("  (chưa có ghn-tele.log)")
	}
	fmt.Println()
	fmt.Println("Gợi ý: gửi ảnh phần này khi cần hỗ trợ.")
}
