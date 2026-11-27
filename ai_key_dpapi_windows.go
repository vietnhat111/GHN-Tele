package main

import (
	"errors"
	"runtime"
	"slices"
	"syscall"
	"unsafe"
)

type dpapiBlob struct {
	size uint32
	data *byte
}

var (
	crypt32DLL        = syscall.NewLazyDLL("crypt32.dll")
	protectData       = crypt32DLL.NewProc("CryptProtectData")
	unprotectData     = crypt32DLL.NewProc("CryptUnprotectData")
	freeProtectedData = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

func protectUserSecret(data []byte) ([]byte, error)   { return callDPAPI(protectData, data) }
func unprotectUserSecret(data []byte) ([]byte, error) { return callDPAPI(unprotectData, data) }

func callDPAPI(proc *syscall.LazyProc, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("missing DPAPI input")
	}
	input := dpapiBlob{uint32(len(data)), &data[0]}
	var output dpapiBlob
	const noUI = 1
	ok, _, err := proc.Call(uintptr(unsafe.Pointer(&input)), 0, 0, 0, 0, noUI, uintptr(unsafe.Pointer(&output)))
	runtime.KeepAlive(data)
	if ok == 0 {
		return nil, err
	}
	defer freeProtectedData.Call(uintptr(unsafe.Pointer(output.data)))
	raw := unsafe.Slice(output.data, int(output.size))
	defer clear(raw)
	return slices.Clone(raw), nil
}
