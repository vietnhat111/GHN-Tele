package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func writeLock(t *testing.T, l watchLock) {
	b, _ := json.Marshal(l)
	if err := os.WriteFile(watchLockPath(), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWatchLockPIDReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cần /proc")
	}
	old := exeDir
	exeDir = t.TempDir()
	defer func() { exeDir = old }()

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip("không chạy được sleep")
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	pid := cmd.Process.Pid
	st, ok := procStartTime(pid)
	if !ok {
		t.Fatal("phải đọc được giờ tạo tiến trình")
	}

	writeLock(t, watchLock{PID: pid, Since: "x", Start: st})
	if running, p, _ := watchRunning(); !running || p != pid {
		t.Fatal("đúng PID + đúng giờ tạo → đang chạy")
	}

	// PID bị cấp lại cho chương trình khác: giờ tạo không khớp
	writeLock(t, watchLock{PID: pid, Since: "x", Start: st + 12345})
	if running, _, _ := watchRunning(); running {
		t.Error("giờ tạo khác → KHÔNG phải tiến trình theo dõi")
	}
	stopWatch(pid)
	if !pidAlive(pid) {
		t.Fatal("stopWatch đã giết nhầm chương trình khác!")
	}

	// lock bản cũ chưa có giờ tạo → vẫn tin PID
	writeLock(t, watchLock{PID: pid, Since: "x"})
	if running, _, _ := watchRunning(); !running {
		t.Error("lock cũ không có start vẫn phải nhận")
	}

	// PID chết → không chạy
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	writeLock(t, watchLock{PID: pid, Since: "x", Start: st})
	if running, _, _ := watchRunning(); running {
		t.Error("tiến trình đã chết → không chạy")
	}
}

func TestAcquireReleaseLock(t *testing.T) {
	old := exeDir
	exeDir = t.TempDir()
	defer func() { exeDir = old }()
	release, err := acquireWatchLock()
	if err != nil {
		t.Fatal(err)
	}
	l, ok := readWatchLock()
	if !ok || l.PID != os.Getpid() {
		t.Fatalf("lock phải ghi PID của mình: %+v", l)
	}
	if runtime.GOOS == "linux" && l.Start == 0 {
		t.Error("Linux phải ghi giờ tạo tiến trình")
	}
	// tiến trình khác đã ghi đè lock → release không được xoá lock của nó
	writeLock(t, watchLock{PID: 999999, Since: "y"})
	release()
	if _, ok := readWatchLock(); !ok {
		t.Error("release xoá nhầm lock của tiến trình khác")
	}
}
