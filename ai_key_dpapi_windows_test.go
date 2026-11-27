package main

import (
	"bytes"
	"testing"
)

func TestWindowsUserKeyProtection(t *testing.T) {
	plaintext := []byte("fake Claude key for DPAPI round trip")
	sealed, err := protectUserSecret(plaintext)
	if err != nil || bytes.Contains(sealed, plaintext) {
		t.Fatalf("protect failed: %v", err)
	}
	restored, err := unprotectUserSecret(sealed)
	if err != nil || !bytes.Equal(restored, plaintext) {
		t.Fatalf("unprotect failed: %v", err)
	}
}
