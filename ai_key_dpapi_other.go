//go:build !windows

package main

import "errors"

func protectUserSecret([]byte) ([]byte, error) {
	return nil, errors.New("kho khóa từ xa cần Windows")
}

func unprotectUserSecret(data []byte) ([]byte, error) {
	return protectUserSecret(data)
}
