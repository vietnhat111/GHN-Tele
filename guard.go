package main

// Tự bật lại khi bị tắt ngang (Task Manager, lỗi, mất điện...).
//
//   running.flag  — ghi khi vòng theo dõi chạy, xoá khi thoát đúng cách hoặc dừng bằng PIN.
//                   Lúc khởi động mà còn file này = lần trước kết thúc bất thường.
//   stopped.flag  — anh dừng có chủ ý (PIN) → không tự bật lại cho tới khi bật bằng tay.
//
// Task Scheduler (Windows):
//   GHN-DenKho-Watch  — khi đăng nhập Windows → --ensure
//   GHN-DenKho-Guard  — mỗi 5 phút → --ensure (không chạy thì bật lại)
// Cả hai hiện rõ trong Task Scheduler; tiến trình ghn-tele.exe hiện rõ trong Task Manager.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	taskWatch = "GHN-DenKho-Watch"
	taskGuard = "GHN-DenKho-Guard"
)

func runningFlagPath() string { return filepath.Join(exeDir, "running.flag") }
func stoppedFlagPath() string { return filepath.Join(exeDir, "stopped.flag") }
func guardVbsPath() string    { return filepath.Join(exeDir, "guard.vbs") }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func markRunning() {
	_ = os.WriteFile(runningFlagPath(), []byte(fmt.Sprintf("%d %s", os.Getpid(), time.Now().Format(time.RFC3339))), 0600)
}
func clearRunning()      { _ = os.Remove(runningFlagPath()) }
func stopIntended() bool { return fileExists(stoppedFlagPath()) }

// setStopIntent: dừng bằng PIN → nhớ để guard không tự bật lại.
func setStopIntent() {
	_ = os.WriteFile(stoppedFlagPath(), []byte(time.Now().Format(time.RFC3339)), 0600)
	clearRunning()
}

// clearStopIntent: anh bật lại bằng tay → guard tiếp tục canh.
func clearStopIntent() { _ = os.Remove(stoppedFlagPath()) }

// restartReason: lần trước kết thúc thế nào. Hàm thuần.
//
//	""       — thoát bình thường / lần đầu
//	"reboot" — máy tắt hoặc khởi động lại (khởi động sau lần quét cuối)
//	"killed" — bị tắt ngang khi máy vẫn chạy (Task Manager, lỗi...)
func restartReason(hadRunningFlag bool, lastScan, boot time.Time) string {
	if !hadRunningFlag {
		return ""
	}
	if !boot.IsZero() && !lastScan.IsZero() && boot.After(lastScan) {
		return "reboot"
	}
	return "killed"
}

// restartNotice: tin báo khi tự bật lại sau khi kết thúc bất thường.
func restartNotice(cfg Config, reason string, lastScan, now time.Time) (outMsg, bool) {
	last := "-"
	if !lastScan.IsZero() {
		last = lastScan.Format("02/01 15:04")
	}
	switch reason {
	case "killed":
		return loud("⚠️ <b>APP BỊ TẮT NGANG — ĐÃ TỰ BẬT LẠI</b>\n" +
			"🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n" +
			"🕐 Quét lần cuối trước khi bị tắt: " + last + "\n" +
			"▶️ Bật lại lúc: " + now.Format("02/01 15:04") + "\n" +
			"Ai đó đã tắt app bằng Task Manager, hoặc app bị lỗi. Muốn dừng hẳn: mục 5 (cần PIN).\n" +
			"👤 Liên hệ: " + managerLink(cfg)), true
	case "reboot":
		return quiet("ℹ️ <b>Máy kho đã khởi động lại</b> — app tự chạy lại.\n" +
			"🕐 Quét lần cuối trước đó: " + last), true
	}
	return outMsg{}, false
}

// cmdEnsure: Task Scheduler gọi mỗi 5 phút + khi đăng nhập. Không chạy thì bật lại —
// trừ khi anh đã chủ động dừng bằng PIN.
func cmdEnsure() {
	if stopIntended() || updateInProgress() {
		return
	}
	if running, _, _ := watchRunning(); running {
		return
	}
	if !cfgReady(loadCfg()) {
		return
	}
	logf("[guard] Không thấy tiến trình theo dõi → tự bật lại")
	watch() // chính tiến trình này trở thành vòng theo dõi
}

// ensureTasks: (Windows) tạo/cập nhật 2 tác vụ. Gọi khi bắt đầu theo dõi để máy đã cài
// bản cũ tự có thêm lớp tự bật lại. Bỏ qua nếu anh đã gỡ tự chạy (mục 6).
func ensureTasks(cfg Config) {
	if runtime.GOOS != "windows" || !cfg.autoStartOn() {
		return
	}
	channelActive := updateChannelConfigured()
	if channelActive && allowUpdateAccount() != nil {
		return // Tác vụ cũ dưới SYSTEM không được sửa lịch chạy bằng tài khoản vận hành.
	}
	if out, err := createTasks(exePath(), cfg.autoSessionOn() || channelActive); err != nil {
		logf("[guard] Không tạo được tác vụ tự bật lại: %v %s", err, strings.TrimSpace(out))
	}
}

// createTasks: tác vụ khi đăng nhập + tác vụ canh mỗi 5 phút, đều chạy --ensure ẩn.
func createTasks(exe string, atLogon bool) (string, error) {
	if !atLogon {
		tr := fmt.Sprintf(`"%s" --ensure`, exe)
		if out, err := exec.Command("schtasks", "/Create", "/F", "/TN", taskWatch, "/TR", tr,
			"/SC", "ONSTART", "/RU", "SYSTEM", "/RL", "HIGHEST").CombinedOutput(); err != nil {
			return string(out), err
		}
		out, err := exec.Command("schtasks", "/Create", "/F", "/TN", taskGuard, "/TR", tr,
			"/SC", "MINUTE", "/MO", "5", "/RU", "SYSTEM", "/RL", "HIGHEST").CombinedOutput()
		return string(out), err
	}
	content := "CreateObject(\"Wscript.Shell\").Run \"\"\"" + exe + "\"\" --ensure\", 0, False\r\n"
	if err := os.WriteFile(guardVbsPath(), []byte(content), 0644); err != nil {
		return "", err
	}
	tr := `wscript.exe "` + guardVbsPath() + `"`
	watchArgs := []string{"/Create", "/F", "/TN", taskWatch, "/TR", tr, "/SC", "ONLOGON", "/DELAY", "0001:00"}
	guardArgs := []string{"/Create", "/F", "/TN", taskGuard, "/TR", tr, "/SC", "MINUTE", "/MO", "5"}
	if updateChannelConfigured() {
		watchArgs = append(watchArgs, "/IT")
		guardArgs = append(guardArgs, "/IT")
	}
	if out, err := exec.Command("schtasks", watchArgs...).CombinedOutput(); err != nil {
		return string(out), err
	}
	out, err := exec.Command("schtasks", guardArgs...).CombinedOutput()
	return string(out), err
}

func deleteTasks() {
	if runtime.GOOS != "windows" {
		return
	}
	_ = exec.Command("schtasks", "/Delete", "/TN", taskWatch, "/F").Run()
	_ = exec.Command("schtasks", "/Delete", "/TN", taskGuard, "/F").Run()
}

// refreshStopShortcut: phím Ctrl+Alt+S cũ chạy ẩn (không nhập được PIN) →
// viết lại để mở cửa sổ hỏi PIN. Shortcut ngoài Desktop vẫn trỏ file này nên không cần tạo lại.
func refreshStopShortcut() {
	vbs := filepath.Join(exeDir, "stop-watch.vbs")
	if runtime.GOOS != "windows" || !fileExists(vbs) {
		return
	}
	content := "CreateObject(\"Wscript.Shell\").Run \"\"\"" + exePath() + "\"\" --stop\", 1, False\r\n"
	_ = os.WriteFile(vbs, []byte(content), 0644)
}
