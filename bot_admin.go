package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type settingChoice struct {
	text  string
	value string
}

var botSettings = []struct {
	cmd, title string
	choices    []settingChoice
}{
	{"gio", "Khung giờ", []settingChoice{{"05:00–21:00", "05:00 21:00"}, {"Cả ngày", "00:00 23:59"}}},
	{"ca", "Ca báo xe", []settingChoice{{"05:00–08:00", "05:00-08:00"}, {"Tắt tin đầu ca", "tat"}}},
	{"quet", "Chu kỳ quét", []settingChoice{{"2 phút", "2"}, {"5 phút", "5"}, {"10 phút", "10"}}},
	{"tre", "Báo trễ", []settingChoice{{"15 phút", "15"}, {"30 phút", "30"}, {"Tắt", "0"}}},
	{"bangiao", "Báo kẹt bàn giao", []settingChoice{{"60 phút", "60"}, {"90 phút", "90"}, {"Tắt", "0"}}},
}

func showBotSettings(cfg Config, target chatTarget) {
	rows := make([][]tgMenuButton, 0, len(botSettings))
	for _, setting := range botSettings {
		rows = append(rows, []tgMenuButton{{Text: setting.title, Data: "ghn:setting:" + setting.cmd}})
	}
	sendBotRows(cfg, target, "<b>Cài đặt kho</b>\nChọn mục cần xem hoặc đổi.", rows)
}

func runBotAdmin(cfg Config, target chatTarget, cmd string, args []string, now time.Time) {
	switch cmd {
	case "menu", "dieukhien":
		if target.chat == cfg.TelegramPersonal {
			showMobileHome(cfg, target, now)
		} else if cmd == "menu" {
			showOwnerBotMenu(cfg, target)
		} else {
			_ = sendTelegramAt(cfg, target, "Anh mở chat riêng với bot và dùng /dieukhien.", true)
		}
	case "ai":
		botAISetting(cfg, target, args)
	case "caidat":
		if target.chat == cfg.TelegramPersonal {
			showMobileSettings(cfg, target)
		} else {
			showBotSettings(cfg, target)
		}
	case "kiemtracapnhat", "capnhat":
		requested := ""
		if cmd == "capnhat" && len(args) == 1 {
			requested = args[0]
		}
		runBotUpdate(cfg, target, requested, now)
	case "tudongcapnhat":
		botAutoUpdate(cfg, target, "")
	case "ketquacapnhat":
		reportLastBotUpdate(cfg, target)
	case "quetngay":
		requestBotScan(cfg, target, now)
	}
}

func botAdminCallback(cfg Config, target chatTarget, q *tgCallbackQuery, now time.Time) {
	switch q.Data {
	case "ghn:ai":
		botAISetting(cfg, target, nil)
	case "ghn:ai:bat", "ghn:ai:tat":
		botAISetting(cfg, target, []string{strings.TrimPrefix(q.Data, "ghn:ai:")})
	case "ghn:settings":
		showBotSettings(cfg, target)
	case "ghn:auto", "ghn:auto:on", "ghn:auto:off":
		botAutoUpdate(cfg, target, strings.TrimPrefix(q.Data, "ghn:auto"))
	case "ghn:result":
		reportLastBotUpdate(cfg, target)
	case "ghn:scan":
		requestBotScan(cfg, target, now)
	default:
		for _, setting := range botSettings {
			prefix := "ghn:setting:" + setting.cmd
			if q.Data == prefix {
				msg, _ := runCommand(cfg, setting.cmd, nil, q.From, true, now)
				var buttons []tgMenuButton
				for _, choice := range setting.choices {
					buttons = append(buttons, tgMenuButton{Text: choice.text, Data: prefix + ":" + choice.value})
				}
				sendBotRows(cfg, target, msg, [][]tgMenuButton{buttons, {{Text: "Quay lại cài đặt", Data: "ghn:settings"}}})
				return
			}
			for _, choice := range setting.choices {
				if q.Data == prefix+":"+choice.value {
					msg, notify := runCommand(cfg, setting.cmd, strings.Fields(choice.value), q.From, true, now)
					_ = sendTelegramAt(cfg, target, msg, true)
					if notify != "" && target.chat == cfg.TelegramPersonal {
						enqueue(quiet(notify))
					}
					return
				}
			}
		}
	}
}

func botAutoUpdate(cfg Config, target chatTarget, action string) {
	channel, err := loadUpdateChannel()
	if err != nil {
		_ = sendTelegramAt(cfg, target, "⚠️ Chưa đọc được kênh cập nhật. Anh kiểm tra mục 12 trên máy kho.", true)
		return
	}
	if action != "" {
		channel.AutoInstall = action == ":on"
		if err := saveUpdateChannel(channel); err != nil {
			_ = sendTelegramAt(cfg, target, "❌ Chưa lưu được chế độ tự cập nhật.", true)
			return
		}
	}
	state := "Tắt"
	button := tgMenuButton{Text: "Bật tự cập nhật", Data: "ghn:auto:on"}
	if channel.AutoInstall {
		state = "Bật"
		button = tgMenuButton{Text: "Tắt tự cập nhật", Data: "ghn:auto:off"}
	}
	sendBotButton(cfg, target, "<b>Tự cập nhật ngoài giờ quét</b> · "+state, button)
}

func reportLastBotUpdate(cfg Config, target chatTarget) {
	b, err := readUpdateFile(filepath.Join(exeDir, updateResult), 4096)
	msg := "Máy kho đang dùng v" + version + "."
	switch {
	case errors.Is(err, os.ErrNotExist):
		msg += "\nChưa có kết quả cập nhật được lưu."
	case err != nil:
		msg += "\nChưa đọc được kết quả cập nhật."
	default:
		msg += "\n<b>Kết quả gần nhất</b>\n" + esc(strings.TrimSpace(string(b)))
	}
	if target.chat != cfg.TelegramPersonal && err == nil {
		if err := sendTelegramAt(cfg, chatTarget{chat: cfg.TelegramPersonal}, msg, true); err != nil {
			_ = sendTelegramAt(cfg, target, "Chưa gửi được kết quả. Anh mở chat riêng với bot và dùng /ketquacapnhat.", true)
			return
		}
		msg = "Em đã gửi kết quả cập nhật vào chat riêng của anh."
	}
	_ = sendTelegramAt(cfg, target, msg, true)
}

var (
	botScanQueue = make(chan chatTarget, 1)
	botScanBusy  atomic.Bool
)

func requestBotScan(cfg Config, target chatTarget, now time.Time) {
	msg := "Đã nhận yêu cầu quét. Bot sẽ báo kết quả tại đây."
	switch {
	case updateInProgress():
		msg = "App đang cài cập nhật. Anh chờ cài xong rồi quét lại."
	case !cfgReady(cfg) || cfg.liveMin() <= 0 || stopIntended():
		msg = "Máy kho chưa sẵn sàng quét."
	case cfg.Paused || !inWatchWindow(now, cfg):
		msg = "App đang tạm dừng hoặc ngoài khung giờ quét."
	case !botScanBusy.CompareAndSwap(false, true):
		msg = "Đang có một yêu cầu quét ngay. Anh chờ kết quả."
	default:
		botScanQueue <- target
	}
	_ = sendTelegramAt(cfg, target, msg, true)
}

func finishBotScan(cfg Config, target *chatTarget, message string) {
	if target == nil {
		return
	}
	botScanBusy.Store(false)
	_ = sendTelegramAt(cfg, *target, message, true)
}

func botScanResult(ok bool, arrived, upcoming []Transport) string {
	if !ok {
		return "Quét chưa thành công. Anh dùng /app để xem trạng thái máy kho."
	}
	return fmt.Sprintf("Đã quét xong. Đang trong kho: %d xe · Sắp đến: %d xe.", len(arrived), len(upcoming))
}
