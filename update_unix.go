//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
)

func tryUpdateFileLock(f *os.File, exclusive bool) (bool, error) {
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func replaceUpdateFile(src, dst string) error { return os.Rename(src, dst) }

func activeUpdateProcess(l watchLock) bool {
	if !lockAlive(l) {
		return false
	}
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", l.PID))
		if err == nil {
			if i := strings.LastIndexByte(string(b), ')'); i >= 0 && strings.HasPrefix(string(b[i+1:]), " Z ") {
				return false
			}
		}
	}
	return true
}
