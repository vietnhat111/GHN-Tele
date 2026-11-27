package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBotSettingsAccessAndPreservation(t *testing.T) {
	defer botTestDir(t)()
	cfg := Config{TelegramToken: "fake", TelegramChat: "@Depot", TelegramPersonal: "42", LocationID: "1", Session: "saved"}
	if err := saveCfg(cfg); err != nil {
		t.Fatal(err)
	}
	previousTG := tgBase
	t.Cleanup(func() { tgBase = previousTG })
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	tgBase = tg.URL
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.Local)
	m := &tgIncoming{From: user("42"), Date: now.Unix(), Text: "/gio 06:00 22:00",
		Chat: tgChat{Type: "supergroup", ID: json.RawMessage("-99"), Username: "unrelated"}}
	handleMessage(cfg, m, now)
	if loadCfg().WatchFrom != "" {
		t.Fatal("the owner edited settings from an unrelated group")
	}
	m.Chat.Username = "depot"
	m.SenderChat = &m.Chat
	handleMessage(cfg, m, now)
	if loadCfg().WatchFrom != "" {
		t.Fatal("message sent on behalf of a chat edited settings")
	}
	m.SenderChat = nil
	m.From = user("12")
	handleMessage(cfg, m, now)
	if loadCfg().WatchFrom != "" {
		t.Fatal("a group member edited settings")
	}
	m.From = user("42")
	handleMessage(cfg, m, now)
	if loadCfg().WatchFrom != "" {
		t.Fatal("owner settings must be hidden and disabled in the configured group")
	}
	m.Chat = tgChat{Type: "private", ID: json.RawMessage("42")}
	handleMessage(cfg, m, now)
	saved := loadCfg()
	if saved.watchFrom() != "06:00" || saved.watchTo() != "22:00" || saved.Session != "saved" {
		t.Fatal("exact time was not saved or unrelated settings were changed")
	}
	q := tgCallbackQuery{ID: "press", Message: m, From: user("12"), Data: "ghn:setting:quet:10"}
	handleBotCallback(saved, &q, now)
	if loadCfg().liveMin() == 10 {
		t.Fatal("a group member used an owner button")
	}
	q.From = user("42")
	handleBotCallback(saved, &q, now)
	if loadCfg().liveMin() != 10 {
		t.Fatal("the owner could not use the button in the configured private chat")
	}
	if got := chatPreview("/tat@GHN_bot 8124"); got != "/tat" {
		t.Fatal("bot state can retain the PIN")
	}
}

func TestTelegramUpdateSettingRetainsChannel(t *testing.T) {
	defer botTestDir(t)()
	previousTG := tgBase
	t.Cleanup(func() { tgBase = previousTG })
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) == "sendMessage" {
			var params struct{ Text string }
			_ = json.NewDecoder(r.Body).Decode(&params)
			if !strings.Contains(params.Text, "Bật") && !strings.Contains(params.Text, "Tắt") {
				t.Error("the new state was not reported")
			}
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	tgBase = tg.URL
	const manifest = "https://example.test/update.json"
	channel := updateChannel{ManifestURL: manifest, PublicKey: make([]byte, 32)}
	if err := saveUpdateChannel(channel); err != nil {
		t.Fatal(err)
	}
	cfg := Config{TelegramToken: "fake", TelegramPersonal: "42"}
	botAutoUpdate(cfg, chatTarget{chat: "42"}, ":on")
	saved, err := loadUpdateChannel()
	if err != nil || !saved.AutoInstall || saved.ManifestURL != manifest || string(saved.PublicKey) != string(channel.PublicKey) {
		t.Fatalf("channel changed or auto-install is not enabled: %v", err)
	}
	botAutoUpdate(cfg, chatTarget{chat: "42"}, ":off")
	saved, err = loadUpdateChannel()
	if err != nil || saved.AutoInstall {
		t.Fatalf("auto-install was not disabled: %v", err)
	}
}

func TestManualTelegramScanWakesWatcher(t *testing.T) {
	defer botTestDir(t)()
	target := chatTarget{chat: "42", thread: 8}
	botScanQueue <- target
	var queued *chatTarget
	if waitForNextWatch(time.Hour, &queued) || queued == nil || *queued != target {
		t.Fatal("the watcher did not receive the Telegram request")
	}
}

func TestBotSettingDoesNotReportSuccessAfterWriteFailure(t *testing.T) {
	defer botTestDir(t)()
	cfgPath = filepath.Join(t.TempDir(), "missing", "config.json")
	msg, notify := runCommand(Config{}, "quet", []string{"10"}, user("42"), true, time.Now())
	if !strings.Contains(msg, "Chưa lưu") || notify != "" {
		t.Fatalf("write failure was reported as success: %q | %q", msg, notify)
	}
}

func TestCommandAddressedToCorrectBot(t *testing.T) {
	if commandAddressedToBot("/capnhat@OtherBot 2.16.3", "GHNTeleBot") ||
		commandAddressedToBot("/capnhat@OtherBot 2.16.3", "") ||
		!commandAddressedToBot("/capnhat@ghntelebot 2.16.3", "GHNTeleBot") ||
		!commandAddressedToBot("/capnhat 2.16.3", "GHNTeleBot") {
		t.Fatal("bot accepted an unrelated command or ignored its own")
	}
}
