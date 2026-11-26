//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
)

func tryUpdateFileLock(f *os.File, exclusive bool) (bool, error) {
	flags := uintptr(1) // LOCKFILE_FAIL_IMMEDIATELY
	if exclusive {
		flags |= 2 // LOCKFILE_EXCLUSIVE_LOCK
	}
	var overlap syscall.Overlapped
	ok, _, err := lockFileEx.Call(f.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(&overlap)))
	if ok != 0 {
		return true, nil
	}
	if errors.Is(err, syscall.Errno(33)) { // ERROR_LOCK_VIOLATION
		return false, nil
	}
	return false, err
}

func replaceUpdateFile(src, dst string) error {
	from, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	const replaceAndFlush = 9 // MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH
	ok, _, err := moveFileEx.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), replaceAndFlush)
	if ok == 0 {
		return err
	}
	return nil
}

func activeUpdateProcess(l watchLock) bool { return lockAlive(l) }
