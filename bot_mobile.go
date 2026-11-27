package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	mobilePromptPrefix = "🔧 Nhập giá trị"
	mobileTTL          = 5 * time.Minute
)

type mobileField struct {
	key     string
	label   string
	example string
}

var mobileFields = []mobileField{
	{"gio", "Giờ theo dõi", "06:00 22:00"},
	{"ca", "Ca báo xe", "06:00-10:00,14:00-18:00 hoặc tắt"},
	{"quet", "Chu kỳ quét", "5, từ 2 đến 60 phút"},
	{"nhanh", "Quét nhanh", "2, hoặc 0 để tắt"},
	{"tre", "Báo xe trễ", "30, hoặc 0 để tắt"},
	{"bangiao", "Báo kẹt bàn giao", "90, hoặc 0 để tắt"},
	{"sapden", "Theo dõi xe sắp đến", "bật hoặc tắt"},
	{"noinhan", "Nơi nhận báo xe", "nhóm, riêng hoặc cả hai"},
	{"tugiahan", "Tự lấy phiên GHN", "bật hoặc tắt"},
	{"quanly", "Tên hiển thị người quản lý", "Anh Nhật"},
}

type mobileDraft struct {
	field                                     mobileField
	owner, botToken, original, input, confirm string
	prompt                                    int
	expires                                   time.Time
}

var (
	mobileMu   sync.Mutex
	mobileEdit mobileDraft
)

func showMobileHome(cfg Config, t chatTarget, now time.Time) {
	mobileMu.Lock()
	mobileEdit = mobileDraft{}
	mobileMu.Unlock()
	state := "Ngoài giờ quét"
	if cfg.Paused {
		state = "Đang tạm dừng báo"
	} else if inWatchWindow(now, cfg) {
		state = "Đang theo dõi"
	}
	msg := "<b>GHN Tele · Điều khiển máy kho</b>\n" + state + " · v" + version
	sendBotRows(cfg, t, msg, [][]tgMenuButton{
		{{Text: "Trạng thái", Data: botMenuStatus}, {Text: "Quét ngay", Data: "ghn:scan"}},
		{{Text: "Kiểm tra máy kho", Data: "mobile:health"}},
		{{Text: "Cài đặt theo dõi", Data: "mobile:settings"}, {Text: "Trợ lý AI", Data: "ghn:ai"}},
		{{Text: "Kiểm tra bản mới", Data: botMenuCheck}, {Text: "Tự cập nhật", Data: "ghn:auto"}},
		{{Text: "Kết quả cập nhật", Data: "ghn:result"}, {Text: "Tạm dừng / Báo lại", Data: "mobile:alerts"}},
	})
}

func showMobileSettings(cfg Config, t chatTarget) {
	var rows [][]tgMenuButton
	for i := 0; i < len(mobileFields); i += 2 {
		var row []tgMenuButton
		for j := i; j < i+2 && j < len(mobileFields); j++ {
			f := mobileFields[j]
			row = append(row, tgMenuButton{Text: f.label, Data: "mobile:edit:" + f.key})
		}
		rows = append(rows, row)
	}
	rows = append(rows, []tgMenuButton{{Text: "Về bảng điều khiển", Data: "mobile:home"}})
	sendBotRows(cfg, t, "<b>Cài đặt máy kho</b>\nChọn mục, nhập giá trị và bấm Lưu sau khi xem trước.", rows)
}

func mobileCallback(cfg Config, t chatTarget, from *tgUser, data string, now time.Time) {
	switch {
	case data == "mobile:home" || data == "mobile:cancel":
		showMobileHome(cfg, t, now)
	case data == "mobile:health":
		showWarehouseHealth(cfg, t, now)
	case data == "mobile:health:test":
		sendWarehouseTest(cfg, t)
	case data == "mobile:settings":
		showMobileSettings(cfg, t)
	case data == "mobile:alerts":
		sendBotButton(cfg, t, "Gõ <code>/tat PIN</code> trong chat riêng để tạm dừng báo. Bấm nút bên dưới để báo lại.",
			tgMenuButton{Text: "Báo lại", Data: "mobile:resume"}, tgMenuButton{Text: "Về bảng điều khiển", Data: "mobile:home"})
	case data == "mobile:resume":
		reply, notice := runCommand(cfg, "bat", nil, from, true, now)
		_ = sendTelegramAt(cfg, t, reply, true)
		if notice != "" {
			enqueue(quiet(notice))
		}
	case strings.HasPrefix(data, "mobile:edit:"):
		key := strings.TrimPrefix(data, "mobile:edit:")
		for _, f := range mobileFields {
			if key == f.key {
				mobilePrompt(cfg, t, f, now)
				return
			}
		}
	case strings.HasPrefix(data, "mobile:save:"):
		saveMobileDraft(cfg, t, strings.TrimPrefix(data, "mobile:save:"), now)
	}
}

func mobilePrompt(cfg Config, t chatTarget, f mobileField, now time.Time) {
	message := mobilePromptPrefix + " mới cho <b>" + esc(f.label) + "</b>.\nĐang dùng: <b>" + esc(mobileCurrent(cfg, f.key)) +
		"</b>\nAnh trả lời đúng tin này. Ví dụ: <code>" + esc(f.example) + "</code>\nDùng /menu để thoát."
	p := tgSendParams(t.chat, 0, message, true)
	p["reply_markup"] = map[string]interface{}{"force_reply": true, "input_field_placeholder": "Nhập giá trị mới"}
	code, b, err := httpJSON("POST", tgBase+"/bot"+cfg.TelegramToken+"/sendMessage", nil, p)
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			ID int `json:"message_id"`
		} `json:"result"`
	}
	if err != nil || code != 200 || json.Unmarshal(b, &result) != nil || !result.OK || result.Result.ID <= 0 {
		_ = sendTelegramAt(cfg, t, "Chưa mở được ô nhập. Anh thử lại giúp em.", true)
		return
	}
	mobileMu.Lock()
	mobileEdit = mobileDraft{field: f, owner: t.chat, botToken: cfg.TelegramToken, original: mobileCurrent(cfg, f.key), prompt: result.Result.ID, expires: now.Add(mobileTTL)}
	mobileMu.Unlock()
}

func handleMobileReply(cfg Config, m *tgIncoming, username string, now time.Time) bool {
	if m.ReplyTo == nil || !strings.HasPrefix(m.ReplyTo.Text, mobilePromptPrefix) || m.ReplyTo.From == nil || !m.ReplyTo.From.IsBot ||
		username == "" || !strings.EqualFold(m.ReplyTo.From.Username, username) {
		return false
	}
	// Giữ các câu trả lời cho bảng cài đặt ngoài luồng trợ lý AI.
	if m.Chat.Type != "private" || !allowedChat(cfg, m) || m.SenderChat != nil || !isOwner(cfg, m.From) {
		return true
	}
	if m.Date <= 0 || now.Sub(time.Unix(m.Date, 0)) > mobileTTL || time.Unix(m.Date, 0).After(now.Add(time.Minute)) {
		return true
	}
	t := chatTarget{chat: cfg.TelegramPersonal}
	if cmd, _ := cmdArgs(m.Text); cmd == "menu" || cmd == "dieukhien" {
		mobileCallback(cfg, t, m.From, "mobile:cancel", now)
		return true
	}
	mobileMu.Lock()
	d := mobileEdit
	mobileMu.Unlock()
	if d.owner != t.chat || d.botToken != cfg.TelegramToken || d.prompt != m.ReplyTo.MessageID || !now.Before(d.expires) {
		sendBotButton(cfg, t, "Phiên nhập đã hết hạn. Anh mở lại mục cần chỉnh.", tgMenuButton{Text: "Cài đặt", Data: "mobile:settings"})
		return true
	}
	input := strings.TrimSpace(m.Text)
	if len([]rune(input)) > 120 {
		_ = sendTelegramAt(cfg, t, "Giá trị dài quá. Anh trả lời lại tin yêu cầu với tối đa 120 ký tự.", true)
		return true
	}
	preview, _, err := parseMobileEdit(cfg, d.field.key, input)
	if err != nil {
		_ = sendTelegramAt(cfg, t, esc(err.Error())+" Anh trả lời lại tin yêu cầu.", true)
		return true
	}
	var nonce [8]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		_ = sendTelegramAt(cfg, t, "Chưa tạo được nút lưu. Anh thử lại giúp em.", true)
		return true
	}
	d.confirm, d.input, d.expires = hex.EncodeToString(nonce[:]), input, now.Add(mobileTTL)
	mobileMu.Lock()
	mobileEdit = d
	mobileMu.Unlock()
	sendBotButton(cfg, t, "<b>"+esc(d.field.label)+"</b>\nĐang dùng: "+esc(d.original)+"\nSẽ đổi thành: <b>"+esc(preview)+"</b>",
		tgMenuButton{Text: "Lưu", Data: "mobile:save:" + d.confirm}, tgMenuButton{Text: "Hủy", Data: "mobile:cancel"})
	return true
}

func saveMobileDraft(cfg Config, t chatTarget, nonce string, now time.Time) {
	mobileMu.Lock()
	d := mobileEdit
	valid := d.owner == t.chat && d.botToken == cfg.TelegramToken && d.confirm != "" && nonce == d.confirm && now.Before(d.expires)
	if valid {
		mobileEdit = mobileDraft{}
	}
	mobileMu.Unlock()
	if !valid {
		sendBotButton(cfg, t, "Nút lưu đã hết hạn hoặc đã dùng. Anh chọn lại mục cần chỉnh.", tgMenuButton{Text: "Cài đặt", Data: "mobile:settings"})
		return
	}
	var savedValue string
	var changeErr error
	err := editCfgFileChecked(func(latest *Config) error {
		if latest.TelegramPersonal != d.owner || latest.TelegramToken != d.botToken || mobileCurrent(*latest, d.field.key) != d.original {
			changeErr = errors.New("Cài đặt đã thay đổi. Anh mở lại để xem giá trị mới nhất.")
			return changeErr
		}
		value, apply, err := parseMobileEdit(*latest, d.field.key, d.input)
		if err != nil {
			changeErr = err
			return err
		}
		apply(latest)
		savedValue = value
		return nil
	})
	msg := "✅ Đã lưu <b>" + esc(d.field.label) + "</b> · " + esc(savedValue)
	if err != nil {
		msg = "Chưa ghi được cài đặt trên máy kho."
		if changeErr != nil {
			msg = esc(changeErr.Error())
		}
	}
	sendBotButton(cfg, t, msg, tgMenuButton{Text: "Cài đặt khác", Data: "mobile:settings"}, tgMenuButton{Text: "Về bảng điều khiển", Data: "mobile:home"})
}

func mobileCurrent(c Config, key string) string {
	switch key {
	case "gio":
		return c.watchFrom() + "–" + c.watchTo()
	case "ca":
		return c.shiftsRaw()
	case "quet":
		return strconv.Itoa(c.liveMin()) + " phút"
	case "nhanh":
		return minOrOff(c.fastMin())
	case "tre":
		return minOrOff(c.lateMin())
	case "bangiao":
		return minOrOff(c.handoverMin())
	case "sapden":
		return onOff(c.upcomingOn())
	case "noinhan":
		return c.groupLabel()
	case "tugiahan":
		return onOff(c.autoSessionOn())
	case "quanly":
		if c.ManagerName != "" {
			return c.ManagerName
		}
		return "Chưa đặt"
	}
	return ""
}

func parseMobileEdit(cfg Config, key, input string) (string, func(*Config), error) {
	input = strings.TrimSpace(input)
	switch key {
	case "gio":
		parts := strings.Fields(strings.NewReplacer("–", " ", "-", " ").Replace(input))
		if len(parts) != 2 || hhmmToMin(parts[0]) < 0 || hhmmToMin(parts[1]) < 0 {
			return "", nil, errors.New("Anh nhập hai mốc giờ, ví dụ 06:00 22:00.")
		}
		from, to := normHHMM(parts[0]), normHHMM(parts[1])
		return from + "–" + to, func(c *Config) { c.WatchFrom, c.WatchTo = from, to }, nil
	case "ca":
		clean := strings.Join(strings.Fields(input), "")
		if (Config{Shifts: clean}).shiftsOff() {
			return "tắt", func(c *Config) { c.Shifts = "tắt" }, nil
		}
		parts := strings.Split(clean, ",")
		if len(parts) > 8 || len(parseShifts(clean)) != len(parts) {
			return "", nil, errors.New("Anh nhập tối đa 8 ca, ví dụ 06:00-10:00,14:00-18:00 hoặc tắt.")
		}
		return clean, func(c *Config) { c.Shifts = clean }, nil
	case "quet", "nhanh", "tre", "bangiao":
		min, max := 0, 600
		if key == "quet" {
			min, max = 2, 60
		}
		if key == "nhanh" {
			max = cfg.liveMin() - 1
		}
		n, err := strconv.Atoi(input)
		if err != nil || n < min || n > max {
			return "", nil, fmt.Errorf("Anh nhập số phút từ %d đến %d.", min, max)
		}
		return minOrOff(n), func(c *Config) {
			switch key {
			case "quet":
				c.LiveMin = intPtr(n)
			case "nhanh":
				c.FastMin = intPtr(n)
			case "tre":
				c.LateMin = intPtr(n)
			case "bangiao":
				c.HandoverMin = intPtr(n)
			}
		}, nil
	case "sapden", "tugiahan":
		s := strings.ToLower(input)
		if s != "bật" && s != "bat" && s != "tắt" && s != "tat" {
			return "", nil, errors.New("Anh nhập bật hoặc tắt.")
		}
		on := s == "bật" || s == "bat"
		return onOff(on), func(c *Config) {
			if key == "sapden" {
				c.NotifyUpcoming = boolPtr(on)
			} else {
				c.AutoSession = boolPtr(on)
			}
		}, nil
	case "noinhan":
		mode := ""
		switch strings.ToLower(input) {
		case "nhóm", "nhom", "group":
			mode = "group"
		case "riêng", "rieng", "personal":
			mode = "personal"
		case "cả hai", "ca hai", "both":
			mode = "both"
		default:
			return "", nil, errors.New("Anh nhập nhóm, riêng hoặc cả hai.")
		}
		if mode != "personal" && cfg.TelegramChat == "" {
			return "", nil, errors.New("Máy kho chưa cấu hình nhóm nhận tin ở mục 4.")
		}
		return (Config{NotifyMode: mode}).groupLabel(), func(c *Config) { c.NotifyMode = mode }, nil
	case "quanly":
		if input == "" || len([]rune(input)) > 60 || strings.ContainsAny(input, "\r\n") {
			return "", nil, errors.New("Anh nhập tên hiển thị từ 1 đến 60 ký tự, trên một dòng.")
		}
		return input, func(c *Config) { c.ManagerName = input }, nil
	}
	return "", nil, errors.New("Mục cài đặt này chưa hỗ trợ chỉnh từ Telegram.")
}
