package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAIKeyPairingAcceptsOnlyManagerAndCanBeUsedOnce(t *testing.T) {
	defer botTestDir(t)()
	priorTG, priorPairing := tgBase, pendingAIKey
	priorProtect, priorUnprotect := protectAIKeyVault, unprotectAIKeyVault
	t.Cleanup(func() {
		tgBase, pendingAIKey = priorTG, priorPairing
		protectAIKeyVault, unprotectAIKeyVault = priorProtect, priorUnprotect
	})
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	protectAIKeyVault = func(data []byte) ([]byte, error) {
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		return aead.Seal(nonce, nonce, data, nil), nil
	}
	unprotectAIKeyVault = func(data []byte) ([]byte, error) {
		n := aead.NonceSize()
		return aead.Open(nil, data[:n], data[n:], nil)
	}
	t.Setenv("GHN_CLAUDE_API_KEY", "previous-configured-key")
	const secret = "sk-ant-test-key-for-manager-only"
	responses := make(chan string, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(secret)) {
			t.Error("secret leaked through a bot response")
		}
		if filepath.Base(r.URL.Path) == "sendMessage" {
			var p struct{ Text string }
			_ = json.Unmarshal(body, &p)
			responses <- p.Text
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	tgBase = server.URL

	cfg := Config{TelegramToken: "fake", TelegramPersonal: "42", TelegramChat: "-99"}
	now := time.Now()
	pair, code, err := newAIPairing(cfg, "claude", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "GHNPAIR1."+pair.id+".") {
		t.Fatal("invalid pairing export")
	}
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &pair.private.PublicKey, []byte(secret), []byte("ghn-ai-key:"+pair.id))
	if err != nil {
		t.Fatal(err)
	}
	payload := "GHNKEY1." + pair.id + "." + base64.RawURLEncoding.EncodeToString(ciphertext)
	msg := &tgIncoming{From: user("77"), Text: payload, MessageID: 5, Date: now.Unix(), Chat: tgChat{Type: "supergroup", ID: json.RawMessage("-99")}}
	pendingAIKey = pair
	if !handleAIKeyInput(cfg, msg, now) || len(responses) != 0 {
		t.Fatal("group did not quietly discard sensitive input")
	}
	msg.Chat = tgChat{Type: "private", ID: json.RawMessage("42")}
	if !handleAIKeyInput(cfg, msg, now) || len(responses) != 0 {
		t.Fatal("non-manager used the pairing in private")
	}
	msg.From = user("42")
	handleAIKeyInput(cfg, msg, now.Add(aiKeyTTL))
	if got := <-responses; !strings.Contains(got, "hết hạn") {
		t.Fatalf("expired pairing not rejected: %q", got)
	}
	pendingAIKey = pair
	handleAIKeyInput(cfg, msg, now)
	if got := <-responses; !strings.Contains(got, "đã lưu khóa Claude") {
		t.Fatalf("manager could not set an encrypted key: %q", got)
	}
	handleAIKeyInput(cfg, msg, now)
	if got := <-responses; !strings.Contains(got, "hết hạn") {
		t.Fatalf("accepted replay: %q", got)
	}
	if chatPreview(payload) != "[Nội dung riêng]" || chatPreview(secret) != "[Nội dung riêng]" {
		t.Fatal("key could enter stored chat previews")
	}
	onDisk, err := os.ReadFile(filepath.Join(exeDir, aiKeyVaultFile))
	if err != nil || bytes.Contains(onDisk, []byte(secret)) {
		t.Fatal("vault write failed or contained the plaintext key")
	}
	aiKeyVault.Lock()
	aiKeyVault.path = ""
	aiKeyVault.Unlock()
	providers := configuredAIProviders()
	if len(providers) == 0 || providers[len(providers)-1].key != secret {
		t.Fatal("restored key did not override the old environment variable")
	}
}

func TestAIKeyPageDeliveredAsAttachment(t *testing.T) {
	previousTG := tgBase
	t.Cleanup(func() { tgBase = previousTG })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, header, err := r.FormFile("document")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		if header.Filename != "GHN-Tele-Tao-Ma-Khoa-AI.html" || !bytes.Equal(content, aiKeyPage) || r.FormValue("chat_id") != "42" {
			t.Error("wrong attachment or recipient")
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	tgBase = server.URL
	if err := sendAIKeyPage(Config{TelegramToken: "fake"}, chatTarget{chat: "42"}); err != nil {
		t.Fatal(err)
	}
}
