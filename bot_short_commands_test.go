package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShortAICommandsKeepTheOldAliases(t *testing.T) {
	for _, tc := range []struct {
		command, text string
		draft         bool
	}{
		{"/q@kho_bot Xe nào tới trước?", "Xe nào tới trước?", false},
		{"/hoi Xe nào tới trước?", "Xe nào tới trước?", false},
		{"/soan Nhắc ca sau nhận hàng", "Nhắc ca sau nhận hàng", true},
		{"/soantin@kho_bot Nhắc ca sau nhận hàng", "Nhắc ca sau nhận hàng", true},
	} {
		m := &tgIncoming{Text: tc.command, Chat: tgChat{Type: "supergroup"}}
		if q, ok := aiQuestionFrom(m, "kho_bot"); !ok || q.draft != tc.draft || q.text != tc.text {
			t.Errorf("routing %s: %+v, called=%v", tc.command, q, ok)
		}
	}
	if _, ok := aiQuestionFrom(&tgIncoming{Text: "/giao", Chat: tgChat{Type: "private"}}, "kho_bot"); ok {
		t.Fatal("local shift command was intercepted by the AI handler")
	}
}

func TestShiftCommandWorksForConfiguredGroupWithAIDisabled(t *testing.T) {
	prevTG, prevSnap, prevHealth := tgBase, getSnapshot(), warehouseScan.Load()
	t.Cleanup(func() {
		tgBase = prevTG
		snapMu.Lock()
		lastSnap = prevSnap
		snapMu.Unlock()
		warehouseScan.Store(prevHealth)
	})
	reply := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Text   string
			Thread int `json:"message_thread_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p.Thread != 10 {
			t.Error("shift summary sent outside the requesting topic")
		}
		reply <- p.Text
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	tgBase = server.URL
	now := time.Now()
	setSnapshot([]Transport{{PartnerTransportationCode: "E123456", StopSessionStatus: "ARRIVED"}}, nil, now.Add(-time.Minute))
	warehouseScan.Store(nil)
	cfg := Config{TelegramToken: "mock", TelegramChat: "-99", AIEnabled: false}
	m := &tgIncoming{Text: "/giao@kho_bot", From: user("7"), Date: now.Unix(), MessageThreadID: 10,
		Chat: tgChat{Type: "supergroup", ID: json.RawMessage("-100")}}
	handleMessage(cfg, m, now)
	if len(reply) != 0 {
		t.Fatal("unconfigured group got operational data")
	}
	m.Chat.ID = json.RawMessage("-99")
	handleMessage(cfg, m, now)
	select {
	case got := <-reply:
		if !strings.Contains(got, "Chưa bàn giao xong: 1 xe") || !strings.Contains(got, "lượt quét") {
			t.Fatalf("wrong shift summary: %q", got)
		}
	default:
		t.Fatal("no local shift reply when AI was off")
	}
}
