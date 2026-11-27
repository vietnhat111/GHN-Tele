package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signed(t *testing.T, m updateManifest) (updateManifest, string) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m.payload()))
	return m, base64.StdEncoding.EncodeToString(pub)
}

func TestVerifyManifest(t *testing.T) {
	base := updateManifest{Version: "2.15.1", URL: "https://github.com/a/b/releases/download/v2.15.1/ghn-tele.exe",
		Size: 100, SHA256: strings.Repeat("a", 64), Notes: "x"}
	m, pub := signed(t, base)
	if err := verifyManifest(m, pub); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*updateManifest){
		"đổi phiên bản":  func(x *updateManifest) { x.Version = "9.9.9" },
		"đổi link tải":   func(x *updateManifest) { x.URL = "https://evil.example/x.exe" },
		"đổi mã SHA":     func(x *updateManifest) { x.SHA256 = strings.Repeat("b", 64) },
		"đổi ghi chú":    func(x *updateManifest) { x.Notes = "giả" },
		"không chữ ký":   func(x *updateManifest) { x.Sig = "" },
		"đổi dung lượng": func(x *updateManifest) { x.Size = 101 },
	} {
		x := m
		mut(&x)
		if verifyManifest(x, pub) == nil {
			t.Errorf("%s mà vẫn nhận", name)
		}
	}
	_, other := signed(t, base)
	if verifyManifest(m, other) == nil {
		t.Error("ký bằng khoá khác mà vẫn nhận")
	}
	h, hpub := signed(t, updateManifest{Version: "2.15.1", URL: "http://github.com/x", Size: 1, SHA256: strings.Repeat("a", 64)})
	if verifyManifest(h, hpub) == nil {
		t.Error("link không phải HTTPS phải bị từ chối")
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"2.15.1", "2.15.0", true}, {"2.15.0", "2.15.0", false}, {"2.14.9", "2.15.0", false}, {"2.20.0", "2.9.0", true}, {"v3.0.0", "2.99.99", true}, {"x", "1.0.0", false}}
	for _, c := range cases {
		if isNewer(c.a, c.b) != c.want {
			t.Errorf("isNewer(%s,%s) phải %v", c.a, c.b, c.want)
		}
	}
}

func TestInstallWindow(t *testing.T) {
	cfg := Config{WatchFrom: "05:00", WatchTo: "21:00"}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 27, h, m, 0, 0, time.Local) }
	if installWindow(cfg, at(12, 0)) {
		t.Error("trong giờ theo dõi không cài")
	}
	if !installWindow(cfg, at(21, 5)) || !installWindow(cfg, at(3, 0)) {
		t.Error("ngoài giờ phải cài được")
	}
	if installWindow(cfg, at(4, 50)) {
		t.Error("còn 10 phút là tới giờ theo dõi → không cài")
	}
	full := Config{WatchFrom: "00:00", WatchTo: "23:59"}
	if !installWindow(full, at(2, 0)) || installWindow(full, at(12, 0)) {
		t.Error("khung cả ngày → chỉ cài 01:00–04:00")
	}
	if installDue(Config{WatchFrom: "05:00", WatchTo: "21:00", AutoUpdate: boolPtr(false)}, at(22, 0), false) {
		t.Error("tắt tự cập nhật thì không tự cài")
	}
	if !installDue(cfg, at(12, 0), true) {
		t.Error("/capnhat ngay thì cài luôn kể cả trong giờ")
	}
	if s := nextInstallText(cfg, at(15, 0)); s != "21:00 hôm nay" {
		t.Errorf("giờ cài dự kiến: %q", s)
	}
}

func TestUpdateCommandAndForceReset(t *testing.T) {
	defer botTestDir(t)()
	_ = saveCfg(Config{LocationID: "1"})
	old := upd
	upd = &updater{checkNow: make(chan struct{}, 1)}
	defer func() { upd = old }()

	r, _ := updateCommand(loadCfg(), nil, time.Now())
	if !strings.Contains(r, "Đang dùng: v"+version) || !strings.Contains(r, "Tự cập nhật: BẬT") {
		t.Errorf("/capnhat: %s", r)
	}
	if _, n := updateCommand(loadCfg(), []string{"tat"}, time.Now()); loadCfg().autoUpdateOn() || n == "" {
		t.Error("/capnhat tat phải tắt + báo nhóm")
	}
	updateCommand(loadCfg(), []string{"bat"}, time.Now())
	if !loadCfg().autoUpdateOn() {
		t.Error("/capnhat bat phải bật")
	}
	// "cài ngay" khi không có bản mới không được treo lại
	upd.requestNow(true)
	upd.mu.Lock()
	upd.latest = updateManifest{Version: version}
	upd.mu.Unlock()
	st := loadUpdateState()
	if !isNewer(upd.latest.Version, version) && !st.skipped(upd.latest.Version) {
		upd.mu.Lock()
		upd.force = false
		upd.mu.Unlock()
	}
	if upd.isForced() {
		t.Error("không có bản mới thì bỏ cờ cài ngay")
	}
	upd.latest = updateManifest{Version: "99.0.0"}
	if r, _ := updateCommand(loadCfg(), []string{"bo"}, time.Now()); !strings.Contains(r, "Bỏ qua v99.0.0") || !loadUpdateState().skipped("99.0.0") {
		t.Errorf("/capnhat bo: %s", r)
	}
	if r, _ := runCommand(loadCfg(), "capnhat", nil, user("7"), false, time.Now()); !strings.Contains(r, "chỉ chủ app") {
		t.Error("/capnhat chỉ chủ app dùng được")
	}
}

func TestSelfTestFailBlocksInstall(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 'SELFTEST FAIL: không đọc được cấu hình'\nexit 1\n"), 0700)
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("cần sh")
	}
	if err := runSelfTest(script, "2.15.1"); err == nil || !strings.Contains(err.Error(), "không đọc được cấu hình") {
		t.Errorf("tự kiểm tra lỗi phải chặn cài: %v", err)
	}
	ok := filepath.Join(dir, "ok")
	_ = os.WriteFile(ok, []byte("#!/bin/sh\necho 'SELFTEST OK v2.15.0'\n"), 0700)
	if runSelfTest(ok, "2.15.1") == nil {
		t.Error("báo đúng phiên bản khác → phải chặn")
	}
}

func TestSignToolRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	dir := t.TempDir()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := filepath.Join(dir, "k.secret")
	_ = os.WriteFile(key, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0600)
	exe := filepath.Join(dir, "ghn-tele.exe")
	_ = os.WriteFile(exe, []byte("binary"), 0700)
	bin := filepath.Join(dir, "tool")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Skipf("không build được: %s", out)
	}
	if out, err := exec.Command(bin, "--sign-update", key, exe, "2.15.1", "x").CombinedOutput(); err == nil || !strings.Contains(string(out), "KHÔNG khớp") {
		t.Fatalf("khoá lạ phải bị từ chối: %s", out)
	}
	sign := exec.Command(bin, "--sign-update", key, exe, "2.15.1", "Sửa", "lỗi", "nhỏ")
	sign.Env = append(os.Environ(), "GHN_ALLOW_OTHER_KEY=1")
	if out, err := sign.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "update.json"))
	var m updateManifest
	_ = json.Unmarshal(b, &m)
	if err := verifyManifest(m, base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	if m.Notes != "Sửa lỗi nhỏ" || m.Size != 6 || !strings.HasSuffix(m.URL, "/releases/download/v2.15.1/ghn-tele.exe") {
		t.Errorf("update.json sai: %+v", m)
	}
}
