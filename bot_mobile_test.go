package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMobilePanelPrivatePreviewAndSave(t *testing.T) {
	defer botTestDir(t)()
	previousTG := tgBase
	t.Cleanup(func() {
		tgBase = previousTG
		mobileMu.Lock()
		mobileEdit = mobileDraft{}
		mobileMu.Unlock()
	})
	type call struct {
		Text        string
		ReplyMarkup map[string]interface{} `json:"reply_markup"`
	}
	calls := make(chan call, 16)
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c call
		_ = json.NewDecoder(r.Body).Decode(&c)
		calls <- c
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":100}}`))
	}))
	defer tg.Close()
	tgBase = tg.URL
	cfg := Config{TelegramToken: "fake", TelegramPersonal: "42", TelegramChat: "-99", Session: "preserve", WatchFrom: "05:00", WatchTo: "21:00"}
	if err := saveCfg(cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	owner := user("42")
	private := tgChat{Type: "private", ID: json.RawMessage("42")}
	q := tgCallbackQuery{ID: "tap", From: owner, Message: &tgIncoming{Chat: tgChat{Type: "supergroup", ID: json.RawMessage("-99")}, Date: now.Unix()}, Data: "mobile:edit:gio"}
	handleBotCallback(cfg, &q, now)
	if len(calls) != 1 {
		t.Fatal("group could open a private edit")
	}
	<-calls
	q.Message.Chat = private
	handleBotCallback(cfg, &q, now)
	<-calls
	prompt := <-calls
	if prompt.ReplyMarkup["force_reply"] != true || !strings.Contains(prompt.Text, "Giờ theo dõi") {
		t.Fatal("missing input prompt")
	}
	m := tgIncoming{Text: "06:00 22:00", Date: now.Unix(), Chat: private, From: owner,
		ReplyTo: &tgIncoming{MessageID: 100, Text: mobilePromptPrefix + " mới", From: &tgUser{IsBot: true, Username: "ghn_bot"}}}
	if !handleMobileReply(cfg, &m, "ghn_bot", now) {
		t.Fatal("settings reply was not handled")
	}
	preview := <-calls
	if !strings.Contains(preview.Text, "06:00–22:00") || loadCfg().WatchFrom != "05:00" {
		t.Fatal("preview should show proposed hours without saving")
	}
	keyboard := preview.ReplyMarkup["inline_keyboard"].([]interface{})
	btn := keyboard[0].([]interface{})[0].(map[string]interface{})
	q.Data = btn["callback_data"].(string)
	handleBotCallback(cfg, &q, now)
	<-calls
	saved := <-calls
	got := loadCfg()
	if !strings.Contains(saved.Text, "Đã lưu") || got.WatchFrom != "06:00" || got.WatchTo != "22:00" || got.Session != "preserve" {
		t.Fatal("save failed or changed unrelated data")
	}
	handleBotCallback(got, &q, now)
	<-calls
	if again := <-calls; !strings.Contains(again.Text, "đã dùng") {
		t.Fatal("a save button was accepted twice")
	}
	q.Data = "mobile:edit:gio"
	handleBotCallback(got, &q, now)
	<-calls
	<-calls
	m.Text = "07:00 22:00"
	handleMobileReply(got, &m, "ghn_bot", now)
	preview = <-calls
	keyboard = preview.ReplyMarkup["inline_keyboard"].([]interface{})
	q.Data = keyboard[0].([]interface{})[0].(map[string]interface{})["callback_data"].(string)
	if err := editCfgFile(func(c *Config) { c.WatchFrom = "08:00" }); err != nil {
		t.Fatal(err)
	}
	handleBotCallback(loadCfg(), &q, now)
	<-calls
	if rejected := <-calls; !strings.Contains(rejected.Text, "đã thay đổi") || loadCfg().WatchFrom != "08:00" {
		t.Fatal("stale preview overwrote a newer edit")
	}
}

func TestMobileValidation(t *testing.T) {
	cfg := Config{LiveMin: intPtr(5)}
	for _, tc := range []struct{ key, value string }{
		{"gio", "06:00 29:00"}, {"ca", "06:00-10:00,lỗi"}, {"quet", "1"}, {"nhanh", "5"}, {"noinhan", "cả hai"}, {"password", "secret"},
	} {
		if _, _, err := parseMobileEdit(cfg, tc.key, tc.value); err == nil {
			t.Errorf("accepted %s %q", tc.key, tc.value)
		}
	}
}
