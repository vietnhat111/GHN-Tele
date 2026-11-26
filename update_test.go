package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"testing"
)

func TestUpdateSignedRelease(t *testing.T) {
	key, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("ghn-tele binary")
	hash := sha256.Sum256(binary)
	r := updateRelease{Product: updateProduct, Version: "2.15.1", OS: runtime.GOOS, Arch: runtime.GOARCH,
		URL: "https://example.com/ghn-tele.exe", Size: int64(len(binary)), SHA256: hex.EncodeToString(hash[:])}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(private, data)
	makeManifest := func(data []byte) []byte {
		b, err := json.Marshal(signedUpdate{Release: data, Signature: signature})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if got, err := verifyUpdate(makeManifest(data), key); err != nil || got != r {
		t.Fatalf("release = %+v, err = %v", got, err)
	}
	tampered := bytes.Replace(data, []byte("2.15.1"), []byte("2.15.2"), 1)
	if _, err := verifyUpdate(makeManifest(tampered), key); err == nil {
		t.Fatal("accepted modified signed data")
	}
	for _, b := range [][]byte{binary[:len(binary)-1], append(append([]byte(nil), binary...), '!'), bytes.Repeat([]byte("X"), len(binary))} {
		if err := copyUpdatePayload(io.Discard, bytes.NewReader(b), r); err == nil {
			t.Fatalf("accepted invalid payload %q", b)
		}
	}
	if err := copyUpdatePayload(io.Discard, bytes.NewReader(binary), r); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateVersionOrder(t *testing.T) {
	for _, tc := range []struct {
		next, current  string
		newer, invalid bool
	}{
		{"2.15.1", "2.15.0", true, false},
		{"2.9.9", "2.10.0", false, false},
		{"2.15.0", "2.15.0", false, false},
		{"3.0.0", "2.15.0", true, false},
		{"2.15", "2.15.0", false, true},
		{"2.15.01", "2.15.0", false, true},
	} {
		got, err := newerVersion(tc.next, tc.current)
		if got != tc.newer || (err != nil) != tc.invalid {
			t.Errorf("%s > %s = %v, error %v", tc.next, tc.current, got, err)
		}
	}
}

func TestUpdateLockBlocksWatchUntilClosed(t *testing.T) {
	oldDir := exeDir
	exeDir = t.TempDir()
	t.Cleanup(func() { exeDir = oldDir })
	if updateInProgress() {
		t.Fatal("update reported active before obtaining lock")
	}
	lock, free, err := openUpdateLock(true)
	if err != nil || !free {
		t.Fatalf("obtain lock: free=%v, err=%v", free, err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if !updateInProgress() {
		t.Fatal("watch did not see held update lock")
	}
	if other, free, err := openUpdateLock(true); other != nil || free || err != nil {
		if other != nil {
			other.Close()
		}
		t.Fatalf("second writer: free=%v, err=%v", free, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exeDir + string(os.PathSeparator) + updateLock); err != nil {
		t.Fatal(err)
	}
	if updateInProgress() {
		t.Fatal("watch still blocked after lock closed")
	}
}
