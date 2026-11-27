package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const aiKeyVaultFile = "ai-keys.dpapi"

var aiKeyVault struct {
	sync.Mutex
	path string
	keys map[string]string
	err  error
}

var protectAIKeyVault, unprotectAIKeyVault = protectUserSecret, unprotectUserSecret

func loadAIKeyVaultLocked() {
	path := filepath.Join(exeDir, aiKeyVaultFile)
	if aiKeyVault.path == path {
		return
	}
	aiKeyVault.path, aiKeyVault.keys, aiKeyVault.err = path, nil, nil
	sealed, err := readUpdateFile(path, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err == nil {
		var plain []byte
		plain, err = unprotectAIKeyVault(sealed)
		if err == nil {
			err = json.Unmarshal(plain, &aiKeyVault.keys)
			clear(plain)
		}
	}
	if err != nil {
		aiKeyVault.keys = nil
		aiKeyVault.err = errors.New("không mở được kho khóa của tài khoản Windows đang chạy app")
	}
}

func aiKeySource(provider string) (string, string) {
	aiKeyVault.Lock()
	defer aiKeyVault.Unlock()
	loadAIKeyVaultLocked()
	if key := aiKeyVault.keys[provider]; key != "" {
		return key, "Lưu trên máy kho"
	}
	env := "GHN_GEMINI_API_KEY"
	if provider == "claude" {
		env = "GHN_CLAUDE_API_KEY"
	}
	if key := strings.TrimSpace(os.Getenv(env)); key != "" {
		return key, "Biến môi trường Windows"
	}
	return "", "Chưa cài khóa"
}

func setAIKey(provider, key string) error {
	if provider != "claude" && provider != "gemini" {
		return errors.New("dịch vụ AI không hợp lệ")
	}
	aiKeyVault.Lock()
	defer aiKeyVault.Unlock()
	loadAIKeyVaultLocked()
	if aiKeyVault.err != nil {
		return aiKeyVault.err
	}
	keys := make(map[string]string, len(aiKeyVault.keys)+1)
	for id, existing := range aiKeyVault.keys {
		keys[id] = existing
	}
	keys[provider] = key
	plain, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	defer clear(plain)
	sealed, err := protectAIKeyVault(plain)
	if err != nil {
		return errors.New("Windows không lưu được khóa đã mã hóa")
	}
	if err := writeAtomicFile(aiKeyVault.path, sealed); err != nil {
		return errors.New("máy kho chưa ghi được tệp khóa")
	}
	aiKeyVault.keys = keys
	return nil
}
