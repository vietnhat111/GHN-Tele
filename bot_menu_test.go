package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBotUpdateButtons(t *testing.T) {
	previousDir, previousTG, previousHTTP := exeDir, tgBase, updateHTTP
	exeDir = t.TempDir()
	t.Cleanup(func() { exeDir, tgBase, updateHTTP = previousDir, previousTG, previousHTTP })
	key, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const nextVersion = "2.16.3"
	var releaseReads, assetReads int
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/asset" {
			assetReads++
			http.Error(w, "asset should not be requested", http.StatusNotFound)
			return
		}
		releaseReads++
		r := updateRelease{Product: updateProduct, Version: nextVersion, OS: runtime.GOOS, Arch: runtime.GOARCH,
			URL: "https://" + req.Host + "/asset", Size: 8, SHA256: strings.Repeat("a", 64)}
		data, _ := json.Marshal(r)
		_ = json.NewEncoder(w).Encode(signedUpdate{Release: data, Signature: ed25519.Sign(private, data)})
	}))
	defer host.Close()
	updateHTTP = host.Client()
	channel, err := json.Marshal(updateChannel{PublicKey: key, ManifestURL: host.URL + "/update.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exeDir, channelFile), channel, 0600); err != nil {
		t.Fatal(err)
	}
	type telegramCall struct {
		method string
		params map[string]interface{}
	}
	var calls []telegramCall
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var params map[string]interface{}
		_ = json.NewDecoder(req.Body).Decode(&params)
		calls = append(calls, telegramCall{method: filepath.Base(req.URL.Path), params: params})
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	tgBase = tg.URL
	cfg := Config{TelegramToken: "dummy", TelegramPersonal: "42", TelegramChat: "-99"}
	now := time.Unix(1_800_000_000, 0)
	owner := &tgIncoming{Chat: tgChat{Type: "private", ID: json.RawMessage("42")}, From: user("42"), Text: "/menu", Date: now.Unix()}
	handleMessage(cfg, owner, now)
	if len(calls) != 1 || calls[0].method != "sendMessage" || calls[0].params["reply_markup"] == nil {
		t.Fatalf("menu did not show inline buttons: %#v", calls)
	}
	calls = nil
	check := tgCallbackQuery{ID: "press", From: user("7"), Message: owner, Data: botMenuCheck}
	handleBotCallback(cfg, &check, now)
	if len(calls) != 1 || calls[0].method != "answerCallbackQuery" || releaseReads != 0 {
		t.Fatalf("non-owner accessed update: %#v, requests=%d", calls, releaseReads)
	}
	calls = nil
	group := *owner
	group.Chat = tgChat{Type: "group", ID: json.RawMessage("-100")}
	check.From, check.Message = owner.From, &group
	handleBotCallback(cfg, &check, now)
	if len(calls) != 1 || calls[0].method != "answerCallbackQuery" || releaseReads != 0 {
		t.Fatalf("unconfigured group accessed update: %#v, requests=%d", calls, releaseReads)
	}
	calls = nil
	group.Chat.ID = json.RawMessage("-99")
	group.MessageThreadID = 23
	check.Message = &group
	handleBotCallback(cfg, &check, now)
	if len(calls) != 2 || calls[1].params["message_thread_id"] != float64(23) || releaseReads != 1 {
		t.Fatalf("owner in configured topic could not check: %#v, requests=%d", calls, releaseReads)
	}
	calls = nil
	check.Message = owner
	handleBotCallback(cfg, &check, now)
	if len(calls) != 2 || calls[0].method != "answerCallbackQuery" || calls[1].method != "sendMessage" || releaseReads != 2 {
		t.Fatalf("owner check failed: %#v, requests=%d", calls, releaseReads)
	}
	markup, _ := json.Marshal(calls[1].params["reply_markup"])
	if !strings.Contains(string(markup), installButtonData(nextVersion, now)) {
		t.Fatalf("release response lacks a version-bound button: %s", markup)
	}
	calls = nil
	check.Data = installButtonData(nextVersion, now.Add(-botInstallTTL))
	handleBotCallback(cfg, &check, now)
	if releaseReads != 2 || len(calls) != 2 || !strings.Contains(calls[1].params["text"].(string), "hết hạn") {
		t.Fatalf("expired button was not refused: %#v, requests=%d", calls, releaseReads)
	}
	calls = nil
	check.Data = installButtonData("2.16.4", now)
	handleBotCallback(cfg, &check, now)
	if len(calls) != 2 || !strings.Contains(calls[1].params["text"].(string), "vừa thay đổi") || assetReads != 0 {
		t.Fatalf("stale selection was not refused: %#v, downloads=%d", calls, assetReads)
	}
}
