package main

import (
	"strconv"
	"strings"
	"time"
)

const (
	botMenuStatus  = "ghn:status"
	botMenuCheck   = "ghn:check"
	botMenuInstall = "ghn:install:"
	botInstallTTL  = 15 * time.Minute
)

type tgCallbackQuery struct {
	ID      string      `json:"id"`
	From    *tgUser     `json:"from"`
	Message *tgIncoming `json:"message"`
	Data    string      `json:"data"`
}

type tgMenuButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

func ownerPrivateChat(cfg Config, m *tgIncoming, from *tgUser) string {
	if m == nil || m.Chat.Type != "private" || !isOwner(cfg, from) {
		return ""
	}
	chat := rawIDString(m.Chat.ID)
	if chat != cfg.TelegramPersonal {
		return ""
	}
	return chat
}

func sendBotButton(cfg Config, chat, text string, buttons ...tgMenuButton) {
	params := tgSendParams(chat, 0, text, true)
	params["reply_markup"] = map[string][][]tgMenuButton{"inline_keyboard": {buttons}}
	_ = tgCall(cfg.TelegramToken, "sendMessage", params)
}

func showOwnerBotMenu(cfg Config, chat string) {
	sendBotButton(cfg, chat, "<b>GHN Tele v"+version+"</b>\nAnh chọn thao tác bên dưới.",
		tgMenuButton{Text: "Trạng thái app", Data: botMenuStatus},
		tgMenuButton{Text: "Kiểm tra bản mới", Data: botMenuCheck})
}

func installButtonData(releaseVersion string, now time.Time) string {
	return botMenuInstall + releaseVersion + ":" + strconv.FormatInt(now.Unix(), 10)
}

func selectedBotVersion(data string, now time.Time) (string, bool) {
	payload, ok := strings.CutPrefix(data, botMenuInstall)
	if !ok {
		return "", false
	}
	selected, timestamp, ok := strings.Cut(payload, ":")
	if !ok {
		return "", false
	}
	if _, err := versionParts(selected); err != nil {
		return "", false
	}
	issued, err := strconv.ParseInt(timestamp, 10, 64)
	age := now.Unix() - issued
	if err != nil || issued <= 0 || age < 0 || age >= int64(botInstallTTL/time.Second) {
		return "", false
	}
	return selected, true
}

func handleBotCallback(cfg Config, q *tgCallbackQuery, now time.Time) {
	chat := ownerPrivateChat(cfg, q.Message, q.From)
	params := map[string]interface{}{"callback_query_id": q.ID}
	if chat == "" {
		params["text"] = "Dùng nút này trong chat riêng của chủ app."
	}
	// Telegram bỏ hiệu ứng chờ ngay cả khi bước kiểm tra hoặc tải mất thời gian.
	_ = tgCall(cfg.TelegramToken, "answerCallbackQuery", params)
	if chat == "" {
		return
	}
	switch q.Data {
	case botMenuStatus:
		_ = sendTelegramAt(cfg, chatTarget{chat: chat}, statusApp(cfg, now), true)
	case botMenuCheck:
		runBotUpdate(cfg, chat, "", now)
	default:
		if !strings.HasPrefix(q.Data, botMenuInstall) {
			return
		}
		selected, valid := selectedBotVersion(q.Data, now)
		if !valid {
			sendBotButton(cfg, chat, "Nút cài đã hết hạn. Anh kiểm tra lại bản mới.",
				tgMenuButton{Text: "Kiểm tra bản mới", Data: botMenuCheck})
			return
		}
		runBotUpdate(cfg, chat, selected, now)
	}
}

func runBotUpdate(cfg Config, chat, requested string, now time.Time) {
	reply := func(s string) { _ = sendTelegramAt(cfg, chatTarget{chat: chat}, s, true) }
	if updateInProgress() {
		reply("Đang cài một bản cập nhật. Anh chờ kết quả trong chat này.")
		return
	}
	r, manifest, newer, err := checkUpdate()
	if err != nil {
		reply("Cập nhật: " + esc(err.Error()))
		return
	}
	if !newer {
		reply("Kênh cập nhật chưa có bản mới hơn v" + version + ".")
		return
	}
	if requested != r.Version {
		msg := "Đang dùng v" + version + " · Có bản <b>v" + r.Version + "</b>"
		if r.Notes != "" {
			msg += "\n" + esc(r.Notes)
		}
		if requested != "" {
			msg += "\nPhiên bản trên kênh vừa thay đổi. Anh chọn lại bên dưới."
		}
		sendBotButton(cfg, chat, msg, tgMenuButton{Text: "Cài v" + r.Version, Data: installButtonData(r.Version, now)})
		return
	}
	reply("Đang tải và xác minh bản v" + r.Version + "…")
	dir, err := stageUpdate(r, manifest)
	if err != nil {
		reply("Cập nhật: " + esc(err.Error()))
		return
	}
	reply("Đã tải và xác minh. App sẽ báo lại kết quả sau khi cài v" + r.Version + ".")
	if err := startStagedUpdate(dir, chat, false); err != nil {
		reply("Cập nhật: " + esc(err.Error()))
	}
}
