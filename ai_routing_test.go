package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClaudeRoutingForLongAndReasoningQuestions(t *testing.T) {
	var calls []string
	claudeCode := http.StatusOK
	claude := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model     string
			System    string
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		calls = append(calls, req.Model)
		if req.Model != claudeReasoningModel || req.MaxTokens <= 384 || !strings.Contains(req.System, "năm ý ngắn") {
			t.Errorf("expected expanded reasoning request, got %+v", req)
		}
		if claudeCode != http.StatusOK {
			w.WriteHeader(claudeCode)
			return
		}
		w.Write([]byte(`{"content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"Claude OK"}]}`))
	}))
	defer claude.Close()
	gemini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "gemini")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"Gemini OK"}]}}]}`))
	}))
	defer gemini.Close()
	providers := []aiProvider{{"gemini", "fake", gemini.URL}, {"claude", "fake", claude.URL}}
	for _, question := range []string{
		"So sánh hai phương án xếp xe vào bãi.",
		strings.Repeat("Đây là tình huống cần đọc kỹ. ", 20),
	} {
		calls = nil
		if got := askAIProviders(context.Background(), aiQuestion{text: question}, providers); got != "Claude OK" ||
			strings.Join(calls, ",") != claudeReasoningModel || providers[0].kind != "gemini" {
			t.Fatalf("wrong provider order or result: %q %v", got, calls)
		}
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		claudeCode = status
		calls = nil
		if got := askAIProviders(context.Background(), aiQuestion{text: "Lập kế hoạch dỡ hàng"}, providers); got != "Gemini OK" ||
			strings.Join(calls, ",") != claudeReasoningModel+",gemini" {
			t.Fatalf("expected fallback at HTTP %d: %q %v", status, got, calls)
		}
	}
	calls = nil
	if got := askAIProviders(context.Background(), aiQuestion{text: "So sánh hai phương án"}, providers[:1]); got != "Gemini OK" ||
		strings.Join(calls, ",") != "gemini" {
		t.Fatalf("Gemini-only installation stopped working: %q %v", got, calls)
	}
}

func TestRoutingPreservesWarehouseAccuracyAndPrivacy(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"content":[{"type":"text","text":"Claude OK"}]}`))
	}))
	defer server.Close()
	providers := []aiProvider{{"claude", "fake", server.URL}}
	now := time.Now()
	data := snapshot{At: now, Arr: []Transport{{PartnerTransportationCode: "E123456", WillDropOff: WillDropOff{NumOfPkg: 10}}},
		Up: []Transport{tripAt("E654321", now.Add(-10*time.Minute))}}
	for _, tc := range []struct{ question, want string }{
		{"Phân tích hôm nay có xe nào trễ không?", "1 chuyến đã quá giờ"},
		{"So sánh số kiện đang ở kho. Chuyến E123456 có bao nhiêu kiện?", "10 kiện"},
		{"Ca mình còn việc gì em? " + strings.Repeat("Cho anh biết với nhé. ", 35), "Quá giờ dự kiến: 1 xe"},
		{"Hãy so sánh hai cách liên hệ người có số 0900123456", "thông tin riêng"},
		{strings.Repeat("ế", aiMaxQuestion+1), "2.400 ký tự"},
	} {
		if got := answerAIQuestion(context.Background(), aiQuestion{text: tc.question}, Config{}, data, now, providers); !strings.Contains(got, tc.want) {
			t.Errorf("%q: want %q", got, tc.want)
		}
	}
	if calls != 0 {
		t.Fatalf("sent %d local or sensitive questions to Claude", calls)
	}
	if got := answerAIQuestion(context.Background(), aiQuestion{text: "So sánh hai phương án xếp xe vào bãi."}, Config{}, data, now, providers); got != "Claude OK" || calls != 1 {
		t.Fatalf("general reasoning stayed in warehouse matcher: %q (%d)", got, calls)
	}
}
