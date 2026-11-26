package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScanAndPickChats(t *testing.T) {
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"kho_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			// cũ → mới: nhóm nhắn ở topic 3 trước, sau đó ở topic 9; anh nhắn riêng
			_, _ = w.Write([]byte(`{"ok":true,"result":[
				{"update_id":1,"message":{"text":"/start","message_thread_id":3,"chat":{"id":-100111,"type":"supergroup","title":"Kho DB"}}},
				{"update_id":2,"message":{"text":"/start","chat":{"id":42,"type":"private","first_name":"Lê"}}},
				{"update_id":3,"message":{"text":"hi","message_thread_id":9,"chat":{"id":-100111,"type":"supergroup","title":"Kho DB"}}}
			]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer tg.Close()
	old := tgBase
	tgBase = tg.URL
	defer func() { tgBase = old }()

	if u := tgBotUsername("x"); u != "kho_bot" {
		t.Fatalf("getMe: %q", u)
	}
	list, err := scanChats("x")
	if err != nil || len(list) != 2 {
		t.Fatalf("phải thấy 2 chat: %v %+v", err, list)
	}
	g, p, nG, nP := pickChats(list)
	if nG != 1 || nP != 1 || g == nil || p == nil {
		t.Fatalf("pick sai: %d %d", nG, nP)
	}
	if g.id != "-100111" || g.thread != 9 {
		t.Errorf("nhóm phải lấy topic của tin MỚI NHẤT (9), được %s/%d", g.id, g.thread)
	}
	if p.id != "42" {
		t.Errorf("chat riêng sai: %s", p.id)
	}
}

func TestCfgMissing(t *testing.T) {
	m := strings.Join(cfgMissing(Config{}), ",")
	for _, want := range []string{"mã kho", "Bot Token", "nơi nhận tin"} {
		if !strings.Contains(m, want) {
			t.Errorf("thiếu %q trong %q", want, m)
		}
	}
	if strings.Contains(m, "SESSION") {
		t.Error("tự lấy SESSION đang bật thì không tính thiếu SESSION")
	}
	if len(cfgMissing(Config{LocationID: "1", TelegramToken: "t", TelegramChat: "-1"})) != 0 {
		t.Error("đủ rồi thì không thiếu gì")
	}
}

func TestWizardScripted(t *testing.T) {
	dir := t.TempDir()
	oe, oc, ol, oi := exeDir, cfgPath, logPath, stdin
	exeDir, cfgPath, logPath = dir, dir+"/config.json", dir+"/log"
	defer func() { exeDir, cfgPath, logPath, stdin = oe, oc, ol, oi }()
	t.Setenv("LOCALAPPDATA", dir) // không có trình duyệt nào → nhánh dán tay
	t.Setenv("HOME", dir)

	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"kho_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			_, _ = w.Write([]byte(`{"ok":true,"result":[
				{"update_id":1,"message":{"text":"/start","chat":{"id":-100111,"type":"group","title":"Kho DB"}}},
				{"update_id":2,"message":{"text":"/start","chat":{"id":42,"type":"private","first_name":"Lê"}}}]}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			sent = append(sent, "x")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer tg.Close()
	old := tgBase
	tgBase = tg.URL
	defer func() { tgBase = old }()

	// d (dán tay) → SESSION → mã kho → token → Enter quét → Y chat riêng → n (không chạy nền) → Enter
	stdin = bufio.NewReader(strings.NewReader("d\nSESS\n2212700\ntok\n\nY\nn\n\n"))
	wizard()

	c := loadCfg()
	if c.Session != "SESS" || c.LocationID != "2212700" || c.TelegramToken != "tok" {
		t.Errorf("cấu hình sai: %+v", c)
	}
	if c.TelegramChat != "-100111" || c.TelegramPersonal != "42" || c.NotifyMode != "both" {
		t.Errorf("nơi nhận sai: chat=%s riêng=%s mode=%s", c.TelegramChat, c.TelegramPersonal, c.NotifyMode)
	}
	if !cfgReady(c) || len(sent) != 2 {
		t.Errorf("phải đủ cấu hình và gửi tin thử tới 2 nơi: ready=%v sent=%d", cfgReady(c), len(sent))
	}
}
