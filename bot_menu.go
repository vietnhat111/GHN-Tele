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

func sendBotButton(cfg Config, target chatTarget, text string, buttons ...tgMenuButton) {
	sendBotRows(cfg, target, text, [][]tgMenuButton{buttons})
}

func sendBotRows(cfg Config, target chatTarget, text string, rows [][]tgMenuButton) {
	params := tgSendParams(target.chat, target.thread, text, true)
	params["reply_markup"] = map[string][][]tgMenuButton{"inline_keyboard": rows}
	_ = tgCall(cfg.TelegramToken, "sendMessage", params)
}

func showOwnerBotMenu(cfg Config, target chatTarget) {
	sendBotRows(cfg, target, "<b>GHN Tele v"+version+"</b>\nAnh chọn thao tác bên dưới.", [][]tgMenuButton{
		{{Text: "Trạng thái app", Data: botMenuStatus}, {Text: "Cài đặt", Data: "ghn:settings"}},
		{{Text: "Kiểm tra bản mới", Data: botMenuCheck}, {Text: "Tự cập nhật", Data: "ghn:auto"}},
		{{Text: "Kết quả cài gần nhất", Data: "ghn:result"}, {Text: "Quét ngay", Data: "ghn:scan"}},
		{{Text: "Trợ lý AI", Data: "ghn:ai"}},
	})
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
	owner := allowedChat(cfg, q.Message) && q.Message.Date > 0 && isOwner(cfg, q.From)
	params := map[string]interface{}{"callback_query_id": q.ID}
	if !owner {
		params["text"] = "Chỉ chủ app dùng được nút này trong chat riêng hoặc nhóm kho đã cài."
	}
	// Telegram bỏ hiệu ứng chờ ngay cả khi bước kiểm tra hoặc tải mất thời gian.
	_ = tgCall(cfg.TelegramToken, "answerCallbackQuery", params)
	if !owner {
		return
	}
	target := chatTarget{chat: rawIDString(q.Message.Chat.ID), thread: q.Message.MessageThreadID}
	if strings.HasPrefix(q.Data, "mobile:") {
		if q.Message.Chat.Type == "private" && target.chat == cfg.TelegramPersonal {
			mobileCallback(cfg, target, q.From, q.Data, now)
		}
		return
	}
	switch q.Data {
	case botMenuStatus:
		_ = sendTelegramAt(cfg, target, statusApp(cfg, now), true)
	case botMenuCheck:
		runBotUpdate(cfg, target, "", now)
	default:
		if !strings.HasPrefix(q.Data, botMenuInstall) {
			botAdminCallback(cfg, target, q, now)
			return
		}
		selected, valid := selectedBotVersion(q.Data, now)
		if !valid {
			sendBotButton(cfg, target, "Nút cài đã hết hạn. Anh kiểm tra lại bản mới.",
				tgMenuButton{Text: "Kiểm tra bản mới", Data: botMenuCheck})
			return
		}
		runBotUpdate(cfg, target, selected, now)
	}
}

func runBotUpdate(cfg Config, target chatTarget, requested string, now time.Time) {
	reply := func(s string) { _ = sendTelegramAt(cfg, target, s, true) }
	replyError := func(err error) {
		if target.chat == cfg.TelegramPersonal {
			reply("Cập nhật: " + esc(err.Error()))
			return
		}
		reply("Chưa thực hiện được cập nhật. Anh kiểm tra lại trong chat riêng với bot.")
	}
	if updateInProgress() {
		reply("Đang cài một bản cập nhật. Anh chờ kết quả trong chat riêng.")
		return
	}
	r, manifest, newer, err := checkUpdate()
	if err != nil {
		replyError(err)
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
		sendBotButton(cfg, target, msg, tgMenuButton{Text: "Cài v" + r.Version, Data: installButtonData(r.Version, now)})
		return
	}
	reply("Đang tải và xác minh bản v" + r.Version + "…")
	dir, err := stageUpdate(r, manifest)
	if err != nil {
		replyError(err)
		return
	}
	reply("Đã tải và xác minh. App sẽ báo kết quả trong chat riêng sau khi cài v" + r.Version + ".")
	if err := startStagedUpdate(dir, cfg.TelegramPersonal, false); err != nil {
		replyError(err)
	}
}
