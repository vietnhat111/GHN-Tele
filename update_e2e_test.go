//go:build linux

package main

// Thử nguyên vòng tự cập nhật bằng 2 bản exe thật (build trong lúc test):
// tìm bản mới → tải + kiểm chữ ký + tự kiểm tra → cài → bản mới báo ✅ →
// bản mới bị tắt ngang 2 lần → tự quay về bản cũ → báo ⚠️.
// Chạy: go test -run TestUpdateEndToEnd (bỏ qua với -short).

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestUpdateEndToEnd(t *testing.T) {
	if testing.Short() || runtime.GOOS != "linux" {
		t.Skip("test dài, chỉ chạy trên Linux không -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("cần go để build 2 bản thử")
	}

	// --- Telegram giả (http thường; bản build thử trỏ tgBase vào đây)
	var mu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"kho_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var p map[string]interface{}
			_ = json.Unmarshal(b, &p)
			mu.Lock()
			sent = append(sent, p["text"].(string))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer tg.Close()
	waitMsg := func(want string, max time.Duration) bool {
		end := time.Now().Add(max)
		for time.Now().Before(end) {
			mu.Lock()
			for _, s := range sent {
				if strings.Contains(s, want) {
					mu.Unlock()
					return true
				}
			}
			mu.Unlock()
			time.Sleep(200 * time.Millisecond)
		}
		return false
	}

	// --- build 2 bản
	work := t.TempDir()
	build := func(ver, out string) {
		cmd := exec.Command("go", "build", "-o", out, "-ldflags",
			"-X main.version="+ver+" -X main.tgBase="+tg.URL+" -X main.apiBase=http://127.0.0.1:1", ".")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", ver, err, out)
		}
	}
	kho := filepath.Join(work, "kho") // thư mục máy kho
	_ = os.MkdirAll(kho, 0700)
	exeA := filepath.Join(kho, "ghn-tele")
	exeB := filepath.Join(work, "release", "ghn-tele.exe")
	_ = os.MkdirAll(filepath.Dir(exeB), 0700)
	build("2.15.0", exeA)
	build("2.15.1", exeB)

	// --- máy kho: cấu hình sẵn, khung giờ không chứa lúc này (không gọi GHN)
	oe, oc, ol, op, ov, ok := exeDir, cfgPath, logPath, exePathOverride, version, updatePubKey
	defer func() { exeDir, cfgPath, logPath, exePathOverride, version, updatePubKey = oe, oc, ol, op, ov, ok }()
	exeDir, cfgPath, logPath, exePathOverride, version = kho, filepath.Join(kho, cfgName), filepath.Join(kho, logName), exeA, "2.15.0"
	from := time.Now().Add(3 * time.Hour).Format("15:04")
	to := time.Now().Add(4 * time.Hour).Format("15:04")
	cfg := Config{Session: "s", LocationID: "1", TelegramToken: "t", TelegramChat: "-100",
		AutoSession: boolPtr(false), WatchFrom: from, WatchTo: to}
	if err := saveCfg(cfg); err != nil {
		t.Fatal(err)
	}

	// --- ký bản phát hành bằng khoá thử
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	updatePubKey = base64.StdEncoding.EncodeToString(pub)
	exeBytes, _ := os.ReadFile(exeB)
	sum := sha256.Sum256(exeBytes)

	// --- GitHub giả (HTTPS)
	var manifest []byte
	gh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + defaultUpdateRepo + "/releases/latest/download/update.json":
			_, _ = w.Write(manifest)
		case "/" + defaultUpdateRepo + "/releases/download/v2.15.1/ghn-tele.exe":
			_, _ = w.Write(exeBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gh.Close()
	oURL, oHTTP := updateBaseURL, updateHTTP
	updateBaseURL, updateHTTP = gh.URL, gh.Client()
	defer func() { updateBaseURL, updateHTTP = oURL, oHTTP }()
	m := updateManifest{Version: "2.15.1", URL: gh.URL + "/" + defaultUpdateRepo + "/releases/download/v2.15.1/ghn-tele.exe",
		Size: int64(len(exeBytes)), SHA256: hex.EncodeToString(sum[:]), Notes: "Thử cập nhật"}
	m.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m.payload()))
	manifest, _ = json.Marshal(m)

	// chữ ký sai → không nhận
	bad := m
	bad.Notes = "bị sửa"
	if verifyManifest(bad, updatePubKey) == nil {
		t.Fatal("sửa nội dung mà chữ ký vẫn đúng?!")
	}

	// 1) tìm + tải + tự kiểm tra
	u := &updater{checkNow: make(chan struct{}, 1)}
	upd = u
	u.checkOnce(loadCfg())
	_, _, lastErr, prepVer, _ := u.snapshot()
	if prepVer != "2.15.1" {
		t.Fatalf("phải chuẩn bị xong v2.15.1, lỗi: %s", lastErr)
	}
	if p, _, ok := u.takePrepared(loadCfg(), time.Now().Add(3*time.Hour+30*time.Minute)); ok || p != "" {
		t.Fatal("trong giờ theo dõi không được cài")
	}

	// 2) cài (ngoài giờ) → bản mới nhận vòng theo dõi, báo ✅
	p, v, ok2 := u.takePrepared(loadCfg(), time.Now())
	if !ok2 {
		t.Fatal("ngoài giờ phải cài")
	}
	if err := installUpdate(p, v, m.Notes); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command(exeA, "--version").Output(); !strings.Contains(string(out), "v2.15.1") {
		t.Fatalf("exe ở máy kho phải là bản mới: %s", out)
	}
	if !fileExists(exeA + ".previous") {
		t.Fatal("phải giữ bản cũ .previous")
	}
	// bản mới chờ tiến trình cũ (test) thoát — ở đây test còn sống nên nó chờ tối đa 30s rồi chạy
	if !waitMsg("Đã cập nhật lên v2.15.1", 45*time.Second) {
		t.Fatalf("bản mới phải báo ✅; tin đã gửi: %v", sent)
	}
	pidB := func() int {
		l, _ := readWatchLock()
		return l.PID
	}
	b1 := pidB()
	if b1 == 0 {
		t.Fatal("bản mới phải đang theo dõi")
	}

	// 3) bản mới bị tắt ngang 2 lần → tự quay về bản cũ
	kill := func(pid int) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		var ws syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &ws, 0, nil) // dọn tiến trình con của test (máy thật không cần)
		for i := 0; i < 50 && pidAlive(pid); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	kill(b1)
	ensure := exec.Command(exeA, "--ensure") // Task Scheduler bật lại
	detach(ensure)
	_ = ensure.Start()
	go func() { _ = ensure.Wait() }()
	for i := 0; i < 50 && (pidB() == b1 || pidB() == 0 || !pidAlive(pidB())); i++ {
		time.Sleep(200 * time.Millisecond)
	}
	b2 := pidB()
	if b2 == b1 || !pidAlive(b2) {
		t.Fatal("tự bật lại lần 1 phải chạy bản mới")
	}
	if loadUpdateState().Crashes != 1 {
		t.Fatalf("đếm tắt ngang = 1, được %d", loadUpdateState().Crashes)
	}
	kill(b2)
	ensure2 := exec.Command(exeA, "--ensure")
	detach(ensure2)
	_ = ensure2.Start()
	go func() { _ = ensure2.Wait() }()
	if !waitMsg("đã tự quay về v2.15.0", 45*time.Second) {
		t.Fatalf("phải quay về bản cũ và báo ⚠️; tin: %v", sent)
	}
	if out, _ := exec.Command(exeA, "--version").Output(); !strings.Contains(string(out), "v2.15.0") {
		t.Fatalf("sau khi quay về, exe phải là bản cũ: %s", out)
	}
	if !loadUpdateState().skipped("2.15.1") {
		t.Error("bản lỗi phải vào danh sách bỏ qua")
	}
	if l, ok := readWatchLock(); ok {
		kill(l.PID) // dọn
	}
}
