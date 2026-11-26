package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateNoticeLimitsAndEscapesNotes(t *testing.T) {
	notes := "  - Bản <mới> & ổn định\n\n* " + strings.Repeat("ệ", 135) + "\n• Dòng ba\nDòng bốn"
	msg := updateSuccessNotice(updateRelease{Version: "2.16.3", Notes: notes}, " Đã bật lại theo dõi.")
	html := telegramUpdateNotice(msg)
	for _, want := range []string{"<b>✅ GHN Tele đã cập nhật lên v2.16.3</b>", "<b>Có gì mới?</b>",
		"• Bản &lt;mới&gt; &amp; ổn định", strings.Repeat("ệ", 119) + "…", "• Dòng ba", "Đã bật lại theo dõi."} {
		if !strings.Contains(html, want) {
			t.Errorf("notice missing %q", want)
		}
	}
	if strings.Contains(html, "Dòng bốn") || strings.Count(html, "• ") != 3 {
		t.Fatalf("expected exactly three changes: %s", html)
	}
	if msg := updateSuccessNotice(updateRelease{Version: "2.16.3"}, ""); strings.Contains(msg, "Có gì mới?") {
		t.Fatal("empty notes should not render an empty section")
	}
}

func TestSuccessfulLocalUpdateReportsToOwner(t *testing.T) {
	defer botTestDir(t)()
	previousTG := tgBase
	t.Cleanup(func() { tgBase = previousTG })
	type telegramMessage struct {
		Chat string `json:"chat_id"`
		Text string `json:"text"`
	}
	messages := make(chan telegramMessage, 4)
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var m telegramMessage
		if err := json.NewDecoder(req.Body).Decode(&m); err != nil {
			t.Error(err)
		}
		messages <- m
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	tgBase = tg.URL
	if err := saveCfg(Config{TelegramToken: "fake", TelegramChat: "-100", TelegramPersonal: "42"}); err != nil {
		t.Fatal(err)
	}
	status := updateSuccessNotice(updateRelease{Version: "2.16.3", Notes: "Có nút <mới>"}, "")
	if err := reportUpdate(t.TempDir(), "", nil, status); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-messages:
		if got.Chat != "42" || !strings.Contains(got.Text, "• Có nút &lt;mới&gt;") {
			t.Fatalf("unexpected message: %+v", got)
		}
	default:
		t.Fatal("expected a private notice")
	}
	b, err := os.ReadFile(filepath.Join(exeDir, updateResult))
	if err != nil || !strings.Contains(string(b), status) || strings.Contains(string(b), "<b>") {
		t.Fatalf("result not stored as plain text: %s, %v", b, err)
	}
	_ = reportUpdate(t.TempDir(), "42", errors.New("thử cài thất bại"), "")
	if got := <-messages; strings.Contains(got.Text, "Có gì mới?") || !strings.Contains(got.Text, "Lỗi cập nhật") {
		t.Fatalf("unexpected failure notice: %+v", got)
	}
}
