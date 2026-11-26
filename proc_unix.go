//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pidAlive: tiến trình còn sống? (signal 0 chỉ kiểm tra, không gửi gì)
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// procStartTime: Linux đọc trường 22 (starttime) trong /proc/<pid>/stat.
// Hệ khác (macOS — chỉ dùng khi phát triển) trả ok=false → chỉ dựa vào PID.
func procStartTime(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	// tên tiến trình nằm trong (...) và có thể chứa dấu cách → cắt sau ")"
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	// sau ")" là trường 3 trở đi → starttime (22) ở vị trí 19
	if len(f) < 20 {
		return 0, false
	}
	v, err := strconv.ParseInt(f[19], 10, 64)
	return v, err == nil
}

// detach: tách tiến trình khỏi phiên terminal — đóng cửa sổ không giết nó.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// bootTime: Linux đọc /proc/uptime; hệ khác trả 0 (không phân biệt được khởi động lại).
func bootTime() time.Time {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Time{}
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return time.Time{}
	}
	sec, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return time.Time{}
	}
	return time.Now().Add(-time.Duration(sec * float64(time.Second)))
}
