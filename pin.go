package main

// Mã PIN cho các thao tác dừng / sửa cấu hình — chặn người khác vô tình hoặc tự ý
// tắt app trên máy kho dùng chung. Không giấu tiến trình, không chặn Task Manager:
// bị tắt ngang thì lớp "tự bật lại" (guard.go) lo và báo vào nhóm.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultPIN  = "1234"
	pinMaxFails = 3
	pinLockFor  = 5 * time.Minute
)

func hashPIN(p string) string {
	h := sha256.Sum256([]byte("ghn-tele:pin:" + strings.TrimSpace(p)))
	return hex.EncodeToString(h[:])
}

// pinOK: đúng PIN? Chưa đặt PIN riêng thì PIN là mặc định 1234.
func pinOK(c Config, input string) bool {
	want := c.PinHash
	if want == "" {
		want = hashPIN(defaultPIN)
	}
	return hashPIN(input) == want
}

func validNewPIN(p string) bool {
	if len(p) < 4 || len(p) > 8 {
		return false
	}
	for _, r := range p {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pinState: số lần sai + khoá đến lúc nào. Lưu ra file để mở lại app không xoá được khoá.
type pinState struct {
	Fails int   `json:"fails"`
	Until int64 `json:"until"`
}

func pinStatePath() string { return filepath.Join(exeDir, "pinlock.json") }

func loadPinState() pinState {
	var st pinState
	if b, err := os.ReadFile(pinStatePath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func savePinState(st pinState) {
	b, _ := json.Marshal(st)
	_ = os.WriteFile(pinStatePath(), b, 0600)
}

// pinAttempt: hàm thuần cho 1 lần nhập — trả về (đúng?, vừa bị khoá?, trạng thái mới).
func pinAttempt(c Config, st pinState, input string, now time.Time) (bool, bool, pinState) {
	if st.Until > now.UnixMilli() {
		return false, false, st
	}
	if pinOK(c, input) {
		return true, false, pinState{}
	}
	st.Fails++
	if st.Fails >= pinMaxFails {
		return false, true, pinState{Until: now.Add(pinLockFor).UnixMilli()}
	}
	return false, false, st
}

// requirePIN: hỏi PIN trước thao tác nhạy cảm. Enter trống = huỷ (không tính sai).
func requirePIN(action string) bool {
	cfg := loadCfg()
	for {
		st := loadPinState()
		if st.Until > time.Now().UnixMilli() {
			fmt.Printf("🔒 Nhập sai PIN quá nhiều — khoá đến %s.\n", time.UnixMilli(st.Until).Format("15:04:05"))
			return false
		}
		in := strings.TrimSpace(ask("🔒 Nhập mã PIN để "+action+" (Enter = huỷ)", ""))
		if in == "" {
			fmt.Println("Đã huỷ.")
			return false
		}
		ok, locked, nst := pinAttempt(cfg, st, in, time.Now())
		savePinState(nst)
		if ok {
			if cfg.PinHash == "" {
				fmt.Println("ℹ️ PIN đang là mặc định 1234 — nên đổi ở mục 1 → sửa từng mục.")
			}
			return true
		}
		if locked {
			fmt.Printf("❌ Sai PIN %d lần — khoá %d phút.\n", pinMaxFails, int(pinLockFor.Minutes()))
			logf("[pin] Sai PIN %d lần khi %s — khoá", pinMaxFails, action)
			if cfgReady(cfg) {
				_ = sendTelegram(cfg, "🔒 <b>Có người nhập sai PIN "+fmt.Sprint(pinMaxFails)+" lần</b>\n"+
					"Thao tác: "+esc(action)+"\n"+
					"Lúc: "+time.Now().Format("02/01 15:04")+" — khoá "+fmt.Sprint(int(pinLockFor.Minutes()))+" phút.\n"+
					"App vẫn đang theo dõi bình thường.")
			}
			return false
		}
		fmt.Printf("❌ Sai PIN (còn %d lần thử).\n", pinMaxFails-nst.Fails)
	}
}

// changePIN: đổi PIN (đã qua requirePIN trước đó).
func changePIN(cfg *Config) bool {
	p1 := strings.TrimSpace(ask("PIN mới (4–8 chữ số)", ""))
	if !validNewPIN(p1) {
		fmt.Println("❌ PIN phải là 4–8 chữ số — giữ PIN cũ.")
		return false
	}
	if strings.TrimSpace(ask("Nhập lại PIN mới", "")) != p1 {
		fmt.Println("❌ Hai lần nhập không khớp — giữ PIN cũ.")
		return false
	}
	cfg.PinHash = hashPIN(p1)
	fmt.Println("✅ Đã đổi PIN.")
	return true
}
