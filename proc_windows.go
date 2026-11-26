//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"time"
)

const (
	processQueryLimitedInfo = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	stillActive             = 259    // STILL_ACTIVE
	errAccessDenied         = syscall.Errno(5)
)

// pidAlive: hỏi thẳng Windows (OpenProcess + GetExitCodeProcess).
// Trước đây dùng `tasklist` rồi so chuỗi → PID 12 khớp nhầm dòng có "1234".
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		// tiến trình của SYSTEM / quyền cao hơn: không mở được nhưng vẫn tồn tại
		return err == errAccessDenied
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

// procStartTime: thời điểm tiến trình được tạo — cùng PID mà khác giờ tạo
// nghĩa là Windows đã cấp lại PID cho chương trình khác.
func procStartTime(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer syscall.CloseHandle(h)
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, false
	}
	return created.Nanoseconds(), true
}

// detach: chạy tách khỏi console hiện tại (DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP).
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = 0x00000008 | 0x00000200
}

var procGetTickCount64 = syscall.NewLazyDLL("kernel32.dll").NewProc("GetTickCount64")

// bootTime: lúc Windows khởi động = bây giờ − thời gian máy đã chạy.
func bootTime() time.Time {
	r, _, _ := procGetTickCount64.Call()
	if r == 0 {
		return time.Time{}
	}
	return time.Now().Add(-time.Duration(r) * time.Millisecond)
}
