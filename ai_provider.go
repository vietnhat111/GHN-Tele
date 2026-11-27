package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	claudeChatModel      = "claude-haiku-4-5"
	claudeReasoningModel = "claude-sonnet-5"
)

const aiPersona = "Bạn là em út của nhóm kho GHN. Xưng em, nói tiếng Việt ngắn gọn, thân thiện, vui nhẹ. " +
	"Chỉ trò chuyện hoặc viết bản nháp. Không điều khiển ứng dụng, không yêu cầu bí mật, " +
	"không tự đặt giờ xe đến, số xe, thông tin tài xế hoặc trạng thái kho. Nếu cần số liệu kho, hướng dẫn dùng /xe. " +
	"Trả lời văn bản thuần, tối đa ba câu, không lặp lại phần hướng dẫn."

type aiProvider struct {
	kind string
	key  string
	url  string
}

var (
	geminiAIURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-lite:generateContent"
	claudeAIURL = "https://api.anthropic.com/v1/messages"
	aiHTTP      = &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
)

func configuredAIProviders() []aiProvider {
	var providers []aiProvider
	if key := strings.TrimSpace(os.Getenv("GHN_GEMINI_API_KEY")); key != "" {
		providers = append(providers, aiProvider{kind: "gemini", key: key, url: geminiAIURL})
	}
	if key := strings.TrimSpace(os.Getenv("GHN_CLAUDE_API_KEY")); key != "" {
		providers = append(providers, aiProvider{kind: "claude", key: key, url: claudeAIURL})
	}
	return providers
}

func askAIProviders(ctx context.Context, q aiQuestion, providers []aiProvider) string {
	reasoning := needsAIReasoning(q.text)
	task := "Trò chuyện với thành viên kho."
	if q.draft {
		task = "Soạn bản nháp tin nhắn dựa trên yêu cầu. Giữ dữ kiện do người dùng nhập và không thêm dữ kiện. Chỉ trả phần tin nhắn có thể sao chép, không nói là đã gửi."
	}
	if q.style == "gon" {
		task += " Nói gọn, lịch sự, xưng em. Không đùa hoặc thêm emoji."
	} else {
		task += " Xưng em, gọi anh hoặc anh em. Dùng cách nói Việt đời thường của anh em kho, đùa nhẹ khi hợp câu hỏi. Tối đa một emoji. Với chuyện gấp, sự cố hoặc bàn giao, nói thẳng. Tránh xưng hô suồng sã và câu đùa lặp lại. Gặp lời cảm ơn, đáp tự nhiên như «Dạ, cần xem thêm anh gọi em nha». Với một lời nhắc ngắn, vào việc ngay, không mở đầu bằng lời giới thiệu vai trò."
	}
	maxRunes := 700
	system := aiPersona
	if reasoning {
		maxRunes = 1400
		system = strings.Replace(system, "tối đa ba câu", "có thể dùng tối đa năm ý ngắn khi cần giải thích", 1)
		if !q.draft {
			task += " Nêu kết luận hoặc lựa chọn trước, rồi giải thích ngắn gọn các điểm chính. Nêu giả định nếu thiếu dữ kiện."
		}
	}
	prompt := task + "\nYêu cầu: " + q.text
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, p := range preferredAIProviders(providers, reasoning) {
		reply, retry := p.answer(ctx, system, prompt, reasoning)
		if reply != "" {
			runes := []rune(strings.TrimSpace(reply))
			if len(runes) > maxRunes {
				return string(runes[:maxRunes-1]) + "…"
			}
			return string(runes)
		}
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return ""
}

func (p aiProvider) answer(ctx context.Context, system, prompt string, reasoning bool) (string, bool) {
	timeout := 8 * time.Second
	if reasoning && p.kind == "claude" {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var data interface{}
	switch p.kind {
	case "gemini":
		parts := func(text string) map[string]interface{} {
			return map[string]interface{}{"parts": []map[string]string{{"text": text}}}
		}
		data = map[string]interface{}{
			"systemInstruction": parts(system),
			"contents":          []map[string]interface{}{parts(prompt)},
			"generationConfig":  map[string]interface{}{"maxOutputTokens": 384, "thinkingConfig": map[string]string{"thinkingLevel": "minimal"}},
		}
	case "claude":
		model, tokens := claudeChatModel, 384
		if reasoning {
			model, tokens = claudeReasoningModel, 3072
		}
		data = map[string]interface{}{
			"model": model, "system": system, "max_tokens": tokens,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		}
	default:
		return "", false
	}
	body, err := json.Marshal(data)
	if err != nil {
		return "", false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")
	if p.kind == "gemini" {
		req.Header.Set("x-goog-api-key", p.key)
	} else {
		req.Header.Set("x-api-key", p.key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := aiHTTP.Do(req)
	if err != nil {
		return "", true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		code := resp.StatusCode
		unavailable := code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500
		if reasoning && p.kind == "claude" {
			unavailable = unavailable || code == http.StatusUnauthorized || code == http.StatusPaymentRequired ||
				code == http.StatusForbidden || code == http.StatusNotFound
		}
		return "", unavailable
	}
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string
					Thought bool
				}
			}
		}
		Content []struct{ Type, Text string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return "", true
	}
	var out strings.Builder
	if p.kind == "gemini" && len(result.Candidates) > 0 {
		for _, part := range result.Candidates[0].Content.Parts {
			if !part.Thought {
				out.WriteString(part.Text)
			}
		}
	}
	if p.kind == "claude" {
		for _, item := range result.Content {
			if item.Type == "text" {
				out.WriteString(item.Text)
			}
		}
	}
	return strings.TrimSpace(out.String()), false
}
