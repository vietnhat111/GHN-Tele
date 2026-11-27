package main

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

const lookupTTL = 5 * time.Minute

type tripChoice struct {
	user     string
	target   chatTarget
	location string
	token    string
	code     string
	question string
	expires  time.Time
}

var lookupChoices = struct {
	sync.Mutex
	entries map[string]tripChoice
}{entries: make(map[string]tripChoice)}

func tripLookupAllowed(cfg Config, m *tgIncoming, from *tgUser) bool {
	if !allowedChat(cfg, m) || from == nil || from.IsBot || rawIDString(from.ID) == "" {
		return false
	}
	if m.Chat.Type == "private" {
		return isOwner(cfg, from)
	}
	return !cfg.GroupHistoryOff
}

func targetForMessage(m *tgIncoming) chatTarget {
	return chatTarget{chat: rawIDString(m.Chat.ID), thread: m.MessageThreadID}
}

func showTripLookup(cfg Config, m *tgIncoming, query string, now time.Time) {
	if !tripLookupAllowed(cfg, m, m.From) || m.SenderChat != nil {
		return
	}
	if query == "" || len([]rune(query)) > aiMaxQuestion {
		sendTripReply(cfg, m, "Dạ, anh gõ /chuyen kèm mã chuyến, biển số hoặc tên tuyến. Ví dụ: /chuyen Đà Nẵng.", nil)
		return
	}
	rows := findTripRecords(cfg, query, now)
	reply, buttons := tripLookupResult(cfg, m, rows, "", now)
	sendTripReply(cfg, m, reply, buttons)
}

func tripLookupResult(cfg Config, m *tgIncoming, rows []tripRecord, question string, now time.Time) (string, [][]tgMenuButton) {
	switch len(rows) {
	case 0:
		return "Em chưa thấy chuyến phù hợp trong lịch sử đã ghi tại kho. Anh thử mã chuyến hoặc tên tuyến đầy đủ hơn nha.", nil
	case 1:
		return describeTrip(rows[0], question, now), nil
	}
	lookupChoices.Lock()
	defer lookupChoices.Unlock()
	for id, choice := range lookupChoices.entries {
		if !now.Before(choice.expires) {
			delete(lookupChoices.entries, id)
		}
	}
	if len(lookupChoices.entries) > 500 {
		return "Dạ có nhiều chuyến phù hợp. Anh gửi riêng mã chuyến hoặc biển số để em lọc chính xác hơn nha.", nil
	}
	var buttons [][]tgMenuButton
	for _, row := range rows[:min(len(rows), 5)] {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			continue
		}
		id := hex.EncodeToString(random[:])
		lookupChoices.entries[id] = tripChoice{user: rawIDString(m.From.ID), target: targetForMessage(m), location: cfg.LocationID,
			token: deliveryBinding(cfg), code: row.Code, question: question, expires: now.Add(lookupTTL)}
		label := row.Code
		if row.Plate != "" {
			label += " · " + row.Plate
		}
		buttons = append(buttons, []tgMenuButton{{Text: truncate(label, 64), Data: "trip:" + id}})
	}
	if len(buttons) == 0 {
		return "Dạ, có nhiều chuyến phù hợp. Anh nhắn mã chuyến cụ thể giúp em nha.", nil
	}
	if len(rows) > len(buttons) {
		return "Dạ, có nhiều chuyến khớp. Em hiện tối đa 5 chuyến gần nhất. Anh chọn bên dưới hoặc gõ mã chuyến để tìm tiếp nha.", buttons
	}
	return "Dạ, anh chọn giúp em đúng chuyến nha.", buttons
}

func handleTripChoice(cfg Config, q *tgCallbackQuery, now time.Time) {
	params := map[string]interface{}{"callback_query_id": q.ID}
	params["text"] = "Nút dành cho người hỏi và có hiệu lực 5 phút."
	if tripLookupAllowed(cfg, q.Message, q.From) {
		id := strings.TrimPrefix(q.Data, "trip:")
		lookupChoices.Lock()
		choice, ok := lookupChoices.entries[id]
		if ok && now.Before(choice.expires) && choice.target == targetForMessage(q.Message) &&
			choice.user == rawIDString(q.From.ID) && choice.location == cfg.LocationID && choice.token == deliveryBinding(cfg) {
			delete(lookupChoices.entries, id)
			delete(params, "text")
		}
		lookupChoices.Unlock()
		if _, rejected := params["text"]; !rejected {
			_ = tgCallOnce(cfg.TelegramToken, "answerCallbackQuery", params, 4*time.Second)
			rows := findTripRecords(cfg, choice.code, now)
			if len(rows) == 1 && strings.EqualFold(rows[0].Code, choice.code) {
				sendTripReply(cfg, q.Message, describeTrip(rows[0], choice.question, now), nil)
			} else {
				sendTripReply(cfg, q.Message, "Chuyến này đã ra khỏi lịch sử lưu tại máy kho rồi anh nha.", nil)
			}
			return
		}
	}
	_ = tgCallOnce(cfg.TelegramToken, "answerCallbackQuery", params, 4*time.Second)
}

func sendTripReply(cfg Config, m *tgIncoming, text string, rows [][]tgMenuButton) {
	target := targetForMessage(m)
	params := tgSendParams(target.chat, target.thread, esc(text), true)
	if m.MessageID > 0 {
		params["reply_parameters"] = map[string]interface{}{"message_id": m.MessageID, "allow_sending_without_reply": true}
	}
	if len(rows) > 0 {
		params["reply_markup"] = map[string][][]tgMenuButton{"inline_keyboard": rows}
	}
	_ = tgCallOnce(cfg.TelegramToken, "sendMessage", params, 5*time.Second)
}

func tripsReferencedIn(cfg Config, reply string, now time.Time) []tripRecord {
	var records []tripRecord
	words := aiWords(reply)
	cutoff := now.AddDate(0, 0, -cfg.historyDays())
	for _, row := range tripRecords(cfg) {
		if !row.Seen.Before(cutoff) && strings.Contains(words, aiWords(row.Code)) {
			row.Events = recentTripEvents(row.Events, cutoff)
			records = append(records, row)
		}
	}
	sortTripRecords(records)
	return records
}

func asksTripHistory(text string) bool {
	words := aiWords(text)
	for _, phrase := range []string{"lich su", "tra cuu", "doi gio", "doi lich", "tuyen", "bien so"} {
		if strings.Contains(words, " "+phrase+" ") {
			return true
		}
	}
	return false
}
