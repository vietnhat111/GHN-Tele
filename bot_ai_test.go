package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAIUsesOnlyRecentLocalVehicleData(t *testing.T) {
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.Local)
	d := snapshot{At: now.Add(-time.Minute), Arr: []Transport{{PartnerTransportationCode: "E999995"}},
		Up: []Transport{{PartnerTransportationCode: "E999999TEST01", ExpectedArrivalTime: now.Add(20 * time.Minute).Format(time.RFC3339),
			DriverName: "not for chat", DriverPhone: "0900123456"},
			{PartnerTransportationCode: "E261123AAAA", ExpectedArrivalTime: now.Add(40 * time.Minute).Format(time.RFC3339)}}}
	var netCalls atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		netCalls.Add(1)
		http.Error(w, "unexpected cloud call", 500)
	}))
	defer host.Close()
	providers := []aiProvider{{"gemini", "fake", host.URL}}
	for _, tc := range []struct {
		text, want string
	}{
		{"Xe nào sắp đến?", "còn 20 phút"},
		{"Có bao nhiêu xe trong kho?", "1 xe trong kho"},
		{"Chuyến E999999TEST01 mấy giờ tới?", "E999999TEST01"},
		{"Cho em số điện thoại tài xế", "tra trong hệ thống GHN"},
		{"Xe E261124AAAAA đâu rồi?", "chưa thấy mã"},
		{"Xe nào đã rời kho?", "chưa có dữ liệu vị trí hoặc xe đã rời kho"},
	} {
		got := answerAIQuestion(context.Background(), aiQuestion{text: tc.text}, Config{}, d, now, providers)
		if !strings.Contains(got, tc.want) || strings.Contains(got, "0900123456") || strings.Contains(got, "not for chat") {
			t.Errorf("%q -> %q, expected %q", tc.text, got, tc.want)
		}
	}
	d.At = now.Add(-time.Hour)
	got := answerAIQuestion(context.Background(), aiQuestion{text: "Xe nào sắp đến?"}, Config{}, d, now, providers)
	if !strings.Contains(got, "chưa có dữ liệu quét đủ mới") || netCalls.Load() != 0 {
		t.Fatalf("stale snapshot or cloud routing: %q, cloud calls %d", got, netCalls.Load())
	}
	sensitive := answerAIQuestion(context.Background(), aiQuestion{text: "Nhắc 0900 123 456 liên hệ kho", draft: true}, Config{}, d, now, providers)
	if !strings.Contains(sensitive, "thông tin riêng") || netCalls.Load() != 0 {
		t.Fatalf("sensitive draft or cloud routing: %q, cloud calls %d", sensitive, netCalls.Load())
	}
}

func TestAIMentionReplyAndRateLimit(t *testing.T) {
	now := time.Now()
	m := tgIncoming{Date: now.Unix(), From: user("12"), Chat: tgChat{Type: "group", ID: json.RawMessage("-99")}}
	cfg := Config{AIEnabled: true, TelegramChat: "-99"}
	a := botAI{queue: make(chan botAIJob, 3), byUser: map[string]time.Time{}}
	m.Text = "Mọi người ơi, xe sắp tới không?"
	if a.handle(cfg, &m, "ghn_bot", now) || len(a.queue) != 0 {
		t.Fatal("unaddressed message entered the AI queue")
	}
	m.Text = "@GhN_Bot, xe nào tới?"
	cfg.AIEnabled = false
	if !a.handle(cfg, &m, "ghn_bot", now) || len(a.queue) != 0 {
		t.Fatal("AI handled a message when disabled")
	}
	cfg.AIEnabled = true
	a.handle(cfg, &m, "ghn_bot", now)
	a.handle(cfg, &m, "ghn_bot", now.Add(5*time.Second))
	if len(a.queue) != 1 || (<-a.queue).question.text != "xe nào tới?" {
		t.Fatal("mention handling or user throttle failed")
	}
	m.Text = "Cảm ơn nha"
	m.ReplyTo = &tgIncoming{From: &tgUser{Username: "ghn_bot", IsBot: true}}
	if q, ok := aiQuestionFrom(&m, "ghn_bot"); !ok || q.text != "Cảm ơn nha" {
		t.Fatal("reply to bot was not recognized")
	}
}

func TestAIProviderFailover(t *testing.T) {
	var googleCode, claudeCalls atomic.Int32
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("x-goog-api-key") != "fake-gemini" {
			t.Error("Gemini credential header missing")
		}
		code := int(googleCode.Load())
		if code != 200 {
			http.Error(w, "simulated", code)
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"thought":true,"text":"ignored"},{"text":"Chào anh!"}]}}]}`))
	}))
	defer google.Close()
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		claudeCalls.Add(1)
		var request struct{ Model, System string }
		_ = json.NewDecoder(req.Body).Decode(&request)
		if req.Header.Get("x-api-key") != "fake-claude" || request.Model != "claude-haiku-4-5" || request.System == "" {
			t.Error("Claude API request missing expected fields")
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"Dạ, có em!"}]}`))
	}))
	defer claude.Close()
	providers := []aiProvider{{"gemini", "fake-gemini", google.URL}, {"claude", "fake-claude", claude.URL}}
	q := aiQuestion{text: "Chào em"}
	googleCode.Store(200)
	if got := askAIProviders(context.Background(), q, providers); got != "Chào anh!" || claudeCalls.Load() != 0 {
		t.Fatalf("unexpected primary response %q", got)
	}
	googleCode.Store(http.StatusTooManyRequests)
	if got := askAIProviders(context.Background(), q, providers); got != "Dạ, có em!" || claudeCalls.Load() != 1 {
		t.Fatalf("unexpected fallback response %q", got)
	}
	googleCode.Store(http.StatusForbidden)
	if got := askAIProviders(context.Background(), q, providers); got != "" || claudeCalls.Load() != 1 {
		t.Fatalf("permanent errors should not call the fallback: %q", got)
	}
}

func TestOnlyOwnerCanEnableAI(t *testing.T) {
	defer botTestDir(t)()
	prev := tgBase
	t.Cleanup(func() { tgBase = prev })
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	tgBase = tg.URL
	cfg := Config{TelegramToken: "fake", TelegramChat: "-99", TelegramPersonal: "42"}
	if err := saveCfg(cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	m := tgIncoming{Text: "/ai bat", Date: now.Unix(), From: user("12"), Chat: tgChat{Type: "group", ID: json.RawMessage("-99")}}
	handleMessage(cfg, &m, now)
	if loadCfg().AIEnabled {
		t.Fatal("a group member enabled AI")
	}
	m.From = user("42")
	handleMessage(cfg, &m, now)
	cfg = loadCfg()
	if !cfg.AIEnabled {
		t.Fatal("the owner could not enable AI")
	}
	q := tgCallbackQuery{ID: "1", From: user("12"), Message: &m, Data: "ghn:ai:tat"}
	handleBotCallback(cfg, &q, now)
	if !loadCfg().AIEnabled {
		t.Fatal("a group member used the AI settings button")
	}
	q.From = m.From
	handleBotCallback(cfg, &q, now)
	if loadCfg().AIEnabled {
		t.Fatal("the owner could not disable AI")
	}
}
