package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOutboxRetrySurvivesRestartWithoutResendingSuccess(t *testing.T) {
	defer botTestDir(t)()
	previousTG := tgBase
	t.Cleanup(func() { tgBase = previousTG })
	cfg := Config{TelegramToken: "test-token", LocationID: "depot", TelegramPersonal: "42", TelegramChat: "-99", NotifyMode: "both"}
	var mu sync.Mutex
	received := map[string][]string{}
	var available bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&fields)
		id := fields["chat_id"].(string)
		mu.Lock()
		defer mu.Unlock()
		if id == "-99" && !available {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"ok":false,"parameters":{"retry_after":25}}`))
			return
		}
		received[id] = append(received[id], fields["text"].(string))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	tgBase = server.URL
	start := time.Now()
	deliver(cfg, loud("🟠 Xe A"))
	deliver(cfg, loud("🔵 Xe B"))
	state, _ := outboxAccess(cfg, nil)
	if len(state.Pending) != 2 || state.Pending[0].Next.Before(start.Add(25*time.Second)) {
		t.Fatalf("missing ordered retry or flood-control delay: %+v", state.Pending)
	}
	saved, err := os.ReadFile(filepath.Join(exeDir, outboxFile))
	if err != nil || strings.Contains(string(saved), cfg.TelegramToken) {
		t.Fatal("queue was not saved safely")
	}
	outbox.Lock()
	outbox.path = ""
	outbox.value = deliveryState{}
	outbox.Unlock()
	mu.Lock()
	available = true
	mu.Unlock()
	retryOutbox(cfg, start.Add(2*time.Minute))
	retryOutbox(cfg, start.Add(2*time.Minute))
	state, _ = outboxAccess(cfg, nil)
	mu.Lock()
	defer mu.Unlock()
	if len(state.Pending) != 0 || strings.Join(received["-99"], ",") != "🟠 Xe A,🔵 Xe B" || len(received["42"]) != 2 {
		t.Fatalf("delivery did not resume cleanly: %+v, %v", state.Pending, received)
	}
}

func TestOutboxSummarizesExpiredMessagesAndKeepsFreshOnes(t *testing.T) {
	defer botTestDir(t)()
	previousTG := tgBase
	t.Cleanup(func() { tgBase = previousTG })
	cfg := Config{TelegramToken: "token", TelegramChat: "-99"}
	now := time.Now()
	peer := telegramPeer{Chat: "-99"}
	outboxAccess(cfg, func(s *deliveryState) {
		s.append(peer, []string{"🟠 Xe cũ A", "🔵 Xe cũ B"}, false, now.Add(-time.Hour), now, 0)
		s.append(peer, []string{"Xe mới C"}, false, now, now, 0)
	})
	got := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct{ Text string }
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p.Text
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	tgBase = server.URL
	retryOutbox(cfg, now)
	retryOutbox(cfg, now)
	if len(got) != 2 {
		t.Fatalf("expected two sends, got %d", len(got))
	}
	first, second := <-got, <-got
	if !strings.Contains(first, "2 phần tin cũ") || !strings.Contains(first, "Tin xe vào kho: 1") || strings.Contains(first, "Xe cũ A") || second != "Xe mới C" {
		t.Fatalf("unexpected catch-up: %q %q", first, second)
	}
}

func TestOutboxNeverReplaysToChangedTarget(t *testing.T) {
	defer botTestDir(t)()
	cfg := Config{TelegramToken: "token", TelegramChat: "-99"}
	now := time.Now()
	spoolOverflow(cfg, loud("Xe A"), now)
	cfg.TelegramChat = "-100"
	state, _ := outboxAccess(cfg, nil)
	if len(state.Pending) != 0 {
		t.Fatal("old group was retained after changing target")
	}
}
