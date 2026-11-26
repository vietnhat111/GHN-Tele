package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPinDefaultAndChange(t *testing.T) {
	c := Config{}
	if !pinOK(c, "1234") || pinOK(c, "0000") {
		t.Fatal("PIN mặc định phải là 1234")
	}
	c.PinHash = hashPIN("8888")
	if pinOK(c, "1234") || !pinOK(c, "8888") {
		t.Error("đổi PIN rồi thì 1234 không còn dùng được")
	}
	for p, ok := range map[string]bool{"1234": true, "12345678": true, "123": false, "123456789": false, "12a4": false} {
		if validNewPIN(p) != ok {
			t.Errorf("validNewPIN(%q) phải là %v", p, ok)
		}
	}
}

func TestPinLockout(t *testing.T) {
	now := time.Now()
	st := pinState{}
	var locked bool
	for i := 1; i <= 3; i++ {
		var ok bool
		ok, locked, st = pinAttempt(Config{}, st, "9999", now)
		if ok {
			t.Fatal("sai PIN không được qua")
		}
	}
	if !locked || st.Until == 0 {
		t.Fatal("sai 3 lần phải khoá")
	}
	if ok, _, _ := pinAttempt(Config{}, st, "1234", now.Add(time.Minute)); ok {
		t.Error("đang khoá thì PIN đúng cũng chưa được")
	}
	if ok, _, st2 := pinAttempt(Config{}, st, "1234", now.Add(6*time.Minute)); !ok || st2.Fails != 0 {
		t.Error("hết 5 phút khoá thì PIN đúng phải qua và xoá đếm")
	}
}

func TestRestartReason(t *testing.T) {
	last := time.Date(2026, 11, 22, 10, 0, 0, 0, time.Local)
	if restartReason(false, last, last.Add(time.Hour)) != "" {
		t.Error("thoát đúng cách → không báo")
	}
	if restartReason(true, last, last.Add(-48*time.Hour)) != "killed" {
		t.Error("máy vẫn chạy từ trước → bị tắt ngang")
	}
	if restartReason(true, last, last.Add(30*time.Minute)) != "reboot" {
		t.Error("máy khởi động sau lần quét cuối → khởi động lại")
	}
	m, ok := restartNotice(Config{LocationID: "22127000"}, "killed", last, last.Add(10*time.Minute))
	if !ok || m.Quiet || !strings.Contains(m.Text, "BỊ TẮT NGANG") || !strings.Contains(m.Text, "22/11 10:00") {
		t.Errorf("tin bị tắt ngang phải có chuông + giờ quét cuối:\n%s", m.Text)
	}
	if m, _ := restartNotice(Config{}, "reboot", last, last); !m.Quiet {
		t.Error("khởi động lại máy → tin im lặng")
	}
}

func TestStopNeedsPIN(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cần /proc")
	}
	dir := t.TempDir()
	oe, oc, ol, oi := exeDir, cfgPath, logPath, stdin
	exeDir, cfgPath, logPath = dir, dir+"/config.json", dir+"/log"
	defer func() { exeDir, cfgPath, logPath, stdin = oe, oc, ol, oi }()

	var mu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		_ = json.Unmarshal(b, &p)
		mu.Lock()
		sent = append(sent, p["text"].(string))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	ot := tgBase
	tgBase = tg.URL
	defer func() { tgBase = ot }()
	_ = saveCfg(Config{Session: "s", LocationID: "22127000", TelegramToken: "t", TelegramChat: "-1"})

	// tiến trình giả đóng vai app đang theo dõi
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip("không chạy được sleep")
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }() // dọn tiến trình con khi bị tắt (máy thật không cần)
	defer func() { _ = cmd.Process.Kill(); <-done }()
	st, _ := procStartTime(cmd.Process.Pid)
	b, _ := json.Marshal(watchLock{PID: cmd.Process.Pid, Since: "x", Start: st})
	_ = os.WriteFile(watchLockPath(), b, 0600)

	// sai 3 lần → không dừng, khoá, báo nhóm
	stdin = bufio.NewReader(strings.NewReader("1111\n2222\n3333\n\n"))
	if cmdStop() {
		t.Fatal("sai PIN mà vẫn dừng được")
	}
	if !pidAlive(cmd.Process.Pid) {
		t.Fatal("sai PIN không được tắt app")
	}
	mu.Lock()
	alerted := len(sent) == 1 && strings.Contains(sent[0], "nhập sai PIN 3 lần")
	mu.Unlock()
	if !alerted {
		t.Errorf("sai 3 lần phải báo nhóm: %v", sent)
	}
	if stopIntended() {
		t.Error("chưa dừng thì không được ghi 'dừng có chủ ý'")
	}

	// hết khoá, PIN đúng → dừng, nhớ là dừng có chủ ý, báo ⏹
	savePinState(pinState{})
	stdin = bufio.NewReader(strings.NewReader("1234\n"))
	if !cmdStop() {
		t.Fatal("PIN đúng phải dừng được")
	}
	if !stopIntended() {
		t.Error("dừng bằng PIN phải ghi stopped.flag để không tự bật lại")
	}
	mu.Lock()
	last := sent[len(sent)-1]
	mu.Unlock()
	if !strings.Contains(last, "ĐÃ DỪNG THEO DÕI") {
		t.Errorf("phải báo ⏹: %s", last)
	}

	// lớp tự bật lại tôn trọng lần dừng có chủ ý (nếu không sẽ chạy watch() và treo test)
	cmdEnsure()
}

func TestEnsureSkipsWhenNotReady(t *testing.T) {
	dir := t.TempDir()
	oe, oc := exeDir, cfgPath
	exeDir, cfgPath = dir, dir+"/config.json"
	defer func() { exeDir, cfgPath = oe, oc }()
	cmdEnsure() // chưa cài → không làm gì, không treo
}
