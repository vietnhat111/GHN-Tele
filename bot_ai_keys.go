package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed GHN-Tele-Tao-Ma-Khoa-AI.html
var aiKeyPage []byte

const aiKeyTTL = 7 * time.Minute

type aiPairing struct {
	id, provider, owner, botToken string
	private                       *rsa.PrivateKey
	expires                       time.Time
}

// Only the Telegram update loop accesses the in-flight pairing.
var pendingAIKey aiPairing
var aiKeyFormat = regexp.MustCompile(`^[A-Za-z0-9_.-]{20,256}$`)

func aiKeyVendor(provider string) string {
	if provider == "claude" {
		return "Claude"
	}
	return "Gemini"
}

func showAIKeyMenu(cfg Config, target chatTarget) {
	if runtime.GOOS != "windows" {
		_ = sendTelegramAt(cfg, target, "Đổi khóa từ xa cần app chạy trên Windows.", true)
		return
	}
	pendingAIKey = aiPairing{}
	_, claude := aiKeySource("claude")
	_, gemini := aiKeySource("gemini")
	sendBotRows(cfg, target, "<b>Khóa API trên máy kho</b>\nClaude · "+claude+"\nGemini · "+gemini+
		"\nAnh chọn dịch vụ muốn cài hoặc đổi khóa.", [][]tgMenuButton{
		{{Text: "Đổi khóa Claude", Data: "mobile:keys:claude"}, {Text: "Đổi khóa Gemini", Data: "mobile:keys:gemini"}},
		{{Text: "Tải trang tạo mã", Data: "mobile:keys:page"}},
		{{Text: "Về AI và tra cứu", Data: "ghn:ai"}},
	})
}

func newAIPairing(cfg Config, provider string, now time.Time) (aiPairing, string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return aiPairing{}, "", err
	}
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return aiPairing{}, "", err
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return aiPairing{}, "", err
	}
	b64 := base64.RawURLEncoding
	pair := aiPairing{id: b64.EncodeToString(id), provider: provider, owner: cfg.TelegramPersonal,
		botToken: cfg.TelegramToken, private: key, expires: now.Add(aiKeyTTL)}
	return pair, "GHNPAIR1." + pair.id + "." + b64.EncodeToString(public), nil
}

func (p aiPairing) open(code string) ([]byte, error) {
	parts := strings.SplitN(code, ".", 3)
	if len(parts) != 3 || parts[0] != "GHNKEY1" || parts[1] != p.id || p.private == nil {
		return nil, fmt.Errorf("mã không khớp phiên")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(ciphertext) != p.private.Size() {
		return nil, fmt.Errorf("mã khóa không hợp lệ")
	}
	return rsa.DecryptOAEP(sha256.New(), rand.Reader, p.private, ciphertext, []byte("ghn-ai-key:"+p.id))
}

func aiKeyCallback(cfg Config, target chatTarget, action string, now time.Time) {
	if runtime.GOOS != "windows" {
		_ = sendTelegramAt(cfg, target, "Đổi khóa từ xa cần app chạy trên Windows.", true)
		return
	}
	switch action {
	case "":
		showAIKeyMenu(cfg, target)
	case "page":
		if err := sendAIKeyPage(cfg, target); err != nil {
			_ = sendTelegramAt(cfg, target, "Em chưa gửi được trang tạo mã. Anh bấm thử lại nha.", true)
		}
	case "claude", "gemini":
		pendingAIKey = aiPairing{}
		pair, code, err := newAIPairing(cfg, action, now)
		if err != nil {
			_ = sendTelegramAt(cfg, target, "Em chưa mở được phiên đổi khóa. Anh thử lại nha.", true)
			return
		}
		pendingAIKey = pair
		sendBotButton(cfg, target, "<b>Đổi khóa "+aiKeyVendor(action)+"</b>\nSao chép mã ghép bên dưới vào trang tạo mã. Dán khóa API trong trang đó rồi gửi lại đúng mã GHNKEY1 trang tạo. Mã có hiệu lực 7 phút.\n\n<code>"+esc(code)+"</code>",
			tgMenuButton{Text: "Tải trang tạo mã", Data: "mobile:keys:page"},
			tgMenuButton{Text: "Hủy đổi khóa", Data: "mobile:keys:"})
	}
}

func aiKeyInput(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "GHNKEY1.") || strings.HasPrefix(text, "GHNPAIR1.") ||
		strings.HasPrefix(text, "sk-ant-") || strings.HasPrefix(text, "AIza")
}

func handleAIKeyInput(cfg Config, m *tgIncoming, now time.Time) bool {
	text := strings.TrimSpace(m.Text)
	if !aiKeyInput(text) {
		return false
	}
	if m.Chat.Type != "private" || !allowedChat(cfg, m) || !isOwner(cfg, m.From) ||
		m.SenderChat != nil || m.From.IsBot {
		return true
	}
	target := targetForMessage(m)
	if m.MessageID > 0 {
		_ = tgCallOnce(cfg.TelegramToken, "deleteMessage", map[string]interface{}{"chat_id": target.chat, "message_id": m.MessageID}, 3*time.Second)
	}
	reply := func(text string) {
		_ = tgCallOnce(cfg.TelegramToken, "sendMessage", tgSendParams(target.chat, target.thread, text, true), 5*time.Second)
	}
	if !strings.HasPrefix(text, "GHNKEY1.") {
		reply("Anh nhập khóa gốc vào trang tạo mã. Gửi về bot dòng bắt đầu bằng GHNKEY1 nha.")
		return true
	}
	pair := pendingAIKey
	if !now.Before(pair.expires) {
		pendingAIKey = aiPairing{}
	}
	if pair.private == nil || pair.owner != target.chat || pair.botToken != cfg.TelegramToken || !now.Before(pair.expires) ||
		m.Date <= 0 || now.Sub(time.Unix(m.Date, 0)) > aiKeyTTL {
		reply("Phiên đổi khóa đã hết hạn hoặc không còn hiệu lực. Anh mở Khóa API trong /menu để tạo mã mới nha.")
		return true
	}
	if len(text) > 600 {
		reply("Mã khóa dài quá. Anh sao chép lại đúng dòng GHNKEY1 trên trang tạo mã nha.")
		return true
	}
	secret, err := pair.open(text)
	if err != nil {
		reply("Mã khóa chưa khớp. Anh kiểm tra đã dùng mã ghép mới nhất nha.")
		return true
	}
	defer clear(secret)
	pendingAIKey = aiPairing{} // Consume once, even when validation or write fails.
	if !aiKeyFormat.Match(secret) {
		reply("Khóa API chưa đúng định dạng. Anh mở lại mục Khóa API để thử lại nha.")
		return true
	}
	if err := setAIKey(pair.provider, string(secret)); err != nil {
		reply("Em chưa lưu được khóa trên Windows. Anh mở lại mục Khóa API để thử lại nha.")
		return true
	}
	reply("✅ Em đã lưu khóa " + aiKeyVendor(pair.provider) + " trên máy kho. Bot dùng ngay cho yêu cầu tiếp theo. Quyền dùng API sẽ được kiểm tra khi anh hỏi thử.")
	return true
}

func sendAIKeyPage(cfg Config, target chatTarget) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("document", "GHN-Tele-Tao-Ma-Khoa-AI.html")
	if err != nil {
		return err
	}
	if _, err = file.Write(aiKeyPage); err != nil {
		return err
	}
	for name, value := range map[string]string{"chat_id": target.chat, "disable_notification": "true",
		"caption": "Tải trang về rồi mở bằng trình duyệt trên điện thoại. Trang tạo mã tại chỗ. Anh chỉ gửi lại dòng GHNKEY1 cho bot."} {
		if err = writer.WriteField(name, value); err != nil {
			return err
		}
	}
	if err = writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, tgBase+"/bot"+cfg.TelegramToken+"/sendDocument", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: aiHTTP.CheckRedirect}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		OK bool `json:"ok"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result) != nil || !result.OK {
		return fmt.Errorf("không gửi được trang tạo mã")
	}
	return nil
}
