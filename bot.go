package main

// Lệnh Telegram — bot nghe tin nhắn trong lúc app theo dõi.
//
//   Mọi người (nhóm đã cài + chat riêng của chủ app): /xe  /app  /lenh
//   Chỉ chủ app (tài khoản của chat riêng đã cài):    /tat PIN  /bat  /gio  /ca  /tre  /quet
//
// Không gọi thêm GHN: /xe trả lời bằng số liệu lần quét gần nhất.
// Không cho đổi SESSION, token, mã kho, PIN qua bot.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------- số liệu lần quét gần nhất

type snapshot struct {
	Arr, Up []Transport
	At      time.Time
}

var (
	snapMu   sync.Mutex
	lastSnap snapshot
)

func setSnapshot(arr, up []Transport, at time.Time) {
	snapMu.Lock()
	lastSnap = snapshot{Arr: arr, Up: up, At: at}
	snapMu.Unlock()
}

func getSnapshot() snapshot {
	snapMu.Lock()
	defer snapMu.Unlock()
	return lastSnap
}

// ---------------------------------------------------------------- trạng thái bot (file)

type savedChat struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Thread int    `json:"thread"`
	Last   string `json:"last"`
	At     int64  `json:"at"`
}

type botState struct {
	Offset int64       `json:"offset"`
	Chats  []savedChat `json:"chats"` // chat đã nhắn cho bot — để mục 4 tìm được nhóm khi app đang chạy
}

func botStatePath() string { return filepath.Join(exeDir, "botstate.json") }

func loadBotState() botState {
	var st botState
	if b, err := os.ReadFile(botStatePath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveBotState(st botState) {
	b, _ := json.Marshal(st)
	_ = os.WriteFile(botStatePath(), b, 0600)
}

func rememberChat(st *botState, inc *tgIncoming) {
	id := rawIDString(inc.Chat.ID)
	if id == "" {
		return
	}
	c := savedChat{ID: id, Type: inc.Chat.Type, Name: inc.Chat.name(), Thread: inc.MessageThreadID,
		Last: truncate(strings.TrimSpace(inc.Text), 40), At: time.Now().UnixMilli()}
	out := []savedChat{c}
	for _, x := range st.Chats {
		if x.ID != id && len(out) < 20 {
			out = append(out, x)
		}
	}
	st.Chats = out
}

// ---------------------------------------------------------------- vòng nghe lệnh

type tgUser struct {
	ID        json.RawMessage `json:"id"`
	FirstName string          `json:"first_name"`
	LastName  string          `json:"last_name"`
	Username  string          `json:"username"`
}

func (u *tgUser) name() string {
	if u == nil {
		return "?"
	}
	if n := strings.TrimSpace(u.FirstName + " " + u.LastName); n != "" {
		return n
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return "?"
}

var botCommands = []struct{ cmd, desc string }{
	{"gioithieu", "Giới thiệu bot: báo gì, khi nào, ký hiệu"},
	{"xe", "Xe trong kho + xe sắp đến"},
	{"app", "Trạng thái app và cài đặt"},
	{"lenh", "Danh sách lệnh"},
	{"kiemtracapnhat", "Xem bản GHN Tele mới (chat riêng chủ app)"},
	{"capnhat", "Cài bản đã chọn (chat riêng chủ app)"},
	{"tat", "Tạm dừng báo — /tat PIN (chủ app)"},
	{"bat", "Báo lại (chủ app)"},
	{"gio", "Khung giờ — /gio 05:00 21:00 (chủ app)"},
	{"ca", "Tin đầu ca — /ca tat hoặc /ca 05:00-08:00,… (chủ app)"},
	{"tre", "Báo xe trễ quá N phút — /tre 30, 0 = tắt (chủ app)"},
	{"quet", "Chu kỳ quét — /quet 5 (2–60 phút, chủ app)"},
	{"bangiao", "Báo xe kẹt bàn giao quá N phút — /bangiao 90, 0 = tắt (chủ app)"},
	{"quanly", "Tên người quản lý — /quanly Lê Nhật (chủ app)"},
}

// startBot: chạy trong tiến trình theo dõi. Trả về khi stop đóng.
func startBot(stop <-chan struct{}) {
	go func() {
		cfg := loadCfg()
		registerCommands(cfg)
		st := loadBotState()
		if st.Offset == 0 {
			st.Offset = skipBacklog(cfg) // lần đầu: bỏ tin cũ, không thực hiện lệnh tồn đọng
			saveBotState(st)
		}
		conflicts := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			cfg = loadCfg()
			if cfg.TelegramToken == "" {
				time.Sleep(time.Minute)
				continue
			}
			ups, code, err := pollUpdates(cfg.TelegramToken, st.Offset, 50)
			if code == 409 {
				conflicts++
				if conflicts >= 6 {
					alert(cfg, "bot409", "⚠️ <b>Có chương trình khác đang dùng bot này</b>\nLệnh /xe, /app… sẽ không nhận được. "+
						"Tắt chương trình cũ trên máy kho (cái từng gửi tin \"✅ XE ĐÃ ĐẾN KHO\").")
					conflicts = 0
				}
				time.Sleep(10 * time.Second)
				continue
			}
			conflicts = 0
			if err != nil {
				time.Sleep(15 * time.Second)
				continue
			}
			for _, u := range ups {
				if u.UpdateID >= st.Offset {
					st.Offset = u.UpdateID + 1
				}
				if u.Message == nil {
					continue
				}
				rememberChat(&st, u.Message)
				if cmd, _ := cmdArgs(u.Message.Text); cmd == "capnhat" {
					// Ghi offset trước khi lệnh có thể khởi động lại tiến trình.
					saveBotState(st)
				}
				handleMessage(cfg, u.Message, time.Now())
			}
			if len(ups) > 0 {
				saveBotState(st)
			}
		}
	}()
}

// managerLink: tên người quản lý, bấm vào mở Telegram của chủ app.
// Có @username → t.me/username (mở được với mọi người); không có → tg://user?id (mở được trong nhóm).
func managerLink(cfg Config) string {
	name := strings.TrimSpace(cfg.ManagerName)
	if name == "" {
		name = "người quản lý"
	}
	switch {
	case cfg.ManagerUser != "":
		return `<a href="https://t.me/` + esc(cfg.ManagerUser) + `">` + esc(name) + `</a>`
	case cfg.TelegramPersonal != "" && !strings.HasPrefix(cfg.TelegramPersonal, "-"):
		return `<a href="tg://user?id=` + esc(cfg.TelegramPersonal) + `">` + esc(name) + `</a>`
	}
	return esc(name)
}

// fillManager: lấy tên + @username của chủ app từ Telegram (getChat) nếu chưa có.
func fillManager(cfg Config) {
	if cfg.TelegramPersonal == "" || (cfg.ManagerName != "" && cfg.ManagerUser != "") {
		return
	}
	code, data, err := httpJSON("POST", tgBase+"/bot"+cfg.TelegramToken+"/getChat", nil,
		map[string]interface{}{"chat_id": cfg.TelegramPersonal})
	if err != nil || code != 200 {
		return
	}
	var r struct {
		Ok     bool   `json:"ok"`
		Result tgChat `json:"result"`
	}
	if json.Unmarshal(data, &r) != nil || !r.Ok {
		return
	}
	c := loadCfg()
	changed := false
	if c.ManagerName == "" {
		if n := strings.TrimSpace(r.Result.FirstName + " " + r.Result.LastName); n != "" {
			c.ManagerName, changed = n, true
		}
	}
	if c.ManagerUser == "" && r.Result.Username != "" {
		c.ManagerUser, changed = r.Result.Username, true
	}
	if changed {
		_ = saveCfg(c)
	}
}

func registerCommands(cfg Config) {
	fillManager(cfg)
	var cmds []map[string]string
	for _, c := range botCommands {
		cmds = append(cmds, map[string]string{"command": c.cmd, "description": c.desc})
	}
	_ = tgCall(cfg.TelegramToken, "setMyCommands", map[string]interface{}{"commands": cmds})
	// hiện khi mở hồ sơ bot / chat với bot lần đầu
	_ = tgCall(cfg.TelegramToken, "setMyShortDescription", map[string]interface{}{
		"short_description": "Báo xe sắp đến / đã đến kho GHN vào nhóm Telegram. Gõ /gioithieu để xem chi tiết."})
	_ = tgCall(cfg.TelegramToken, "setMyDescription", map[string]interface{}{
		"description": "Bot tự theo dõi tab \"Sắp đến kho\" và \"Đã đến kho\" trên GHN rồi nhắn vào nhóm: " +
			"xe sắp đến, số kiện thay đổi, xe trễ, xe đã đến kho, tổng kết cuối ngày.\n\n" +
			"Gõ /gioithieu để xem bot báo những gì, /xe để xem xe trong kho và sắp đến."})
}

// skipBacklog: lấy offset sau tin mới nhất mà không xử lý gì.
func skipBacklog(cfg Config) int64 {
	ups, _, _ := pollUpdates(cfg.TelegramToken, -1, 0)
	var off int64
	for _, u := range ups {
		if u.UpdateID >= off {
			off = u.UpdateID + 1
		}
	}
	return off
}

func pollUpdates(token string, offset int64, timeout int) ([]tgUpdate, int, error) {
	code, data, err := httpJSONTimeout("POST", tgBase+"/bot"+token+"/getUpdates", nil, map[string]interface{}{
		"offset":          offset,
		"timeout":         timeout,
		"allowed_updates": []string{"message"},
	}, time.Duration(timeout+15)*time.Second)
	if err != nil {
		return nil, 0, err
	}
	var r struct {
		Ok          bool       `json:"ok"`
		Description string     `json:"description"`
		Result      []tgUpdate `json:"result"`
	}
	if json.Unmarshal(data, &r) != nil || !r.Ok {
		return nil, code, fmt.Errorf("getUpdates: %s (http %d)", r.Description, code)
	}
	return r.Result, code, nil
}

// ---------------------------------------------------------------- xử lý lệnh

// cmdArgs: "/gio@kho_bot 05:00 21:00" → ("gio", ["05:00","21:00"]).
func cmdArgs(text string) (string, []string) {
	f := strings.Fields(strings.TrimSpace(text))
	if len(f) == 0 || !strings.HasPrefix(f[0], "/") {
		return "", nil
	}
	c := strings.ToLower(strings.TrimPrefix(f[0], "/"))
	if i := strings.IndexByte(c, '@'); i >= 0 {
		c = c[:i]
	}
	return c, f[1:]
}

// isOwner: người gửi là chủ app = tài khoản của chat riêng đã cài (mục 4 / cài nhanh).
func isOwner(cfg Config, from *tgUser) bool {
	return from != nil && cfg.TelegramPersonal != "" && rawIDString(from.ID) == cfg.TelegramPersonal
}

// allowedChat: chỉ trả lời trong nhóm đã cài và chat riêng của chủ app.
func allowedChat(cfg Config, chatID string) bool {
	return chatID != "" && (chatID == cfg.TelegramChat || chatID == cfg.TelegramPersonal)
}

var (
	xeMu   sync.Mutex
	xeLast = map[string]time.Time{}
)

func handleMessage(cfg Config, m *tgIncoming, now time.Time) {
	cmd, args := cmdArgs(m.Text)
	if cmd == "" {
		return
	}
	if m.Date > 0 && now.Sub(time.Unix(m.Date, 0)) > 3*time.Minute {
		return // tin cũ (app vừa bật lại) — không thực hiện
	}
	if cmd == "kiemtracapnhat" || cmd == "capnhat" {
		updateBotCommand(cfg, m, cmd, args)
		return
	}
	chatID := rawIDString(m.Chat.ID)
	owner := isOwner(cfg, m.From)
	if !allowedChat(cfg, chatID) && !owner {
		return
	}
	reply, notify := runCommand(cfg, cmd, args, m.From, owner, now)
	if reply == "" {
		return
	}
	t := chatTarget{chat: chatID, thread: m.MessageThreadID}
	_ = sendTelegramAt(cfg, t, reply, true)
	if cmd == "tat" && len(args) > 0 {
		// xoá tin có PIN (được trong chat riêng; trong nhóm cần bot là admin)
		_ = tgCall(cfg.TelegramToken, "deleteMessage", map[string]interface{}{"chat_id": chatID, "message_id": m.MessageID})
	}
	if notify != "" {
		enqueue(quiet(notify))
	}
}

// runCommand: hàm (gần) thuần — trả về câu trả lời + tin báo nhóm khi đổi cài đặt.
func runCommand(cfg Config, cmd string, args []string, from *tgUser, owner bool, now time.Time) (reply, notify string) {
	ownerOnly := map[string]bool{"tat": true, "bat": true, "gio": true, "ca": true, "tre": true, "quet": true, "quanly": true, "bangiao": true}
	if ownerOnly[cmd] && !owner {
		if cfg.TelegramPersonal == "" {
			return "🔒 Lệnh này chỉ chủ app dùng được — chưa cài chat riêng của chủ app (mục 4 trên máy kho).", ""
		}
		return "🔒 Lệnh này chỉ chủ app dùng được.", ""
	}
	by := " (bởi " + esc(from.name()) + ")"
	switch cmd {
	case "start", "gioithieu":
		return introText(cfg), ""
	case "lenh", "help":
		return helpText(), ""
	case "xe":
		return statusXe(cfg, now), ""
	case "app":
		return statusApp(cfg, now), ""
	case "tat":
		if len(args) == 0 {
			return "Gõ kèm PIN: <code>/tat 1234</code>", ""
		}
		st := loadPinState()
		ok, locked, nst := pinAttempt(cfg, st, args[0], now)
		savePinState(nst)
		if !ok {
			if locked {
				return "", "🔒 <b>Sai PIN " + strconv.Itoa(pinMaxFails) + " lần qua lệnh /tat</b>" + by + " — khoá " +
					strconv.Itoa(int(pinLockFor.Minutes())) + " phút."
			}
			if nst.Until > now.UnixMilli() {
				return "🔒 Đang khoá do sai PIN nhiều lần — thử lại sau.", ""
			}
			return fmt.Sprintf("❌ Sai PIN (còn %d lần thử).", pinMaxFails-nst.Fails), ""
		}
		if cfg.Paused {
			return "⏸ App đang tạm dừng rồi. Gõ /bat để báo lại.", ""
		}
		cfg.Paused = true
		_ = saveCfg(cfg)
		return "⏸ Đã tạm dừng báo. Gõ /bat để báo lại (sẽ bù các thay đổi trong lúc dừng).",
			"⏸ <b>Tạm dừng báo xe</b>" + by + " lúc " + now.Format("15:04") + "."
	case "bat":
		if !cfg.Paused {
			return "▶️ App đang báo bình thường.", ""
		}
		cfg.Paused = false
		_ = saveCfg(cfg)
		return "▶️ Đã bật lại — lần quét tới sẽ bù các thay đổi trong lúc dừng.",
			"▶️ <b>Báo xe trở lại</b>" + by + " lúc " + now.Format("15:04") + "."
	case "gio":
		if len(args) != 2 || hhmmToMin(args[0]) < 0 || hhmmToMin(args[1]) < 0 {
			return "Cách dùng: <code>/gio 05:00 21:00</code> (cả ngày: <code>/gio 00:00 23:59</code>)\nHiện tại: " +
				cfg.watchFrom() + "–" + cfg.watchTo(), ""
		}
		cfg.WatchFrom, cfg.WatchTo = normHHMM(args[0]), normHHMM(args[1])
		_ = saveCfg(cfg)
		return "✅ Khung giờ: " + cfg.watchFrom() + "–" + cfg.watchTo(),
			"⚙️ Đổi khung giờ theo dõi thành <b>" + cfg.watchFrom() + "–" + cfg.watchTo() + "</b>" + by + "."
	case "ca":
		if len(args) == 0 {
			return "Cách dùng: <code>/ca tat</code> hoặc <code>/ca 05:00-08:00,11:00-14:00</code>\nHiện tại: " + esc(cfg.shiftsRaw()), ""
		}
		v := strings.Join(args, "")
		if (Config{Shifts: v}).shiftsOff() {
			cfg.Shifts = "tắt"
		} else if len(parseShifts(v)) == 0 {
			return "❌ Không đọc được danh sách ca. Ví dụ: <code>/ca 05:00-08:00,11:00-14:00</code>", ""
		} else {
			cfg.Shifts = v
		}
		_ = saveCfg(cfg)
		return "✅ Ca: " + esc(cfg.shiftsRaw()), "⚙️ Đổi tin đầu ca thành <b>" + esc(cfg.shiftsRaw()) + "</b>" + by + "."
	case "tre":
		n, err := firstInt(args)
		if err != nil || n < 0 || n > 600 {
			return "Cách dùng: <code>/tre 30</code> (0 = tắt báo trễ)\nHiện tại: " + minOrOff(cfg.lateMin()), ""
		}
		cfg.LateMin = intPtr(n)
		_ = saveCfg(cfg)
		return "✅ Báo xe trễ: " + minOrOff(cfg.lateMin()), "⚙️ Đổi ngưỡng báo xe trễ thành <b>" + minOrOff(cfg.lateMin()) + "</b>" + by + "."
	case "bangiao":
		n, err := firstInt(args)
		if err != nil || n < 0 || n > 600 {
			return "Cách dùng: <code>/bangiao 90</code> (0 = tắt)\nHiện tại: " + minOrOff(cfg.handoverMin()), ""
		}
		cfg.HandoverMin = intPtr(n)
		_ = saveCfg(cfg)
		return "✅ Báo xe kẹt bàn giao: " + minOrOff(cfg.handoverMin()),
			"⚙️ Đổi ngưỡng báo xe kẹt bàn giao thành <b>" + minOrOff(cfg.handoverMin()) + "</b>" + by + "."
	case "quanly":
		if len(args) == 0 {
			return "👤 Người quản lý bot: " + managerLink(cfg) + "\nĐổi tên: <code>/quanly Lê Nhật</code>", ""
		}
		name := truncate(strings.Join(args, " "), 60)
		cfg.ManagerName = name
		if from != nil && from.Username != "" {
			cfg.ManagerUser = from.Username
		}
		_ = saveCfg(cfg)
		return "✅ Người quản lý bot: " + managerLink(cfg),
			"⚙️ Đổi người quản lý bot thành " + managerLink(cfg) + by + "."
	case "quet":
		n, err := firstInt(args)
		if err != nil || n < 2 || n > 60 {
			return "Cách dùng: <code>/quet 5</code> (2–60 phút)\nHiện tại: " + strconv.Itoa(cfg.liveMin()) + " phút", ""
		}
		cfg.LiveMin = intPtr(n)
		_ = saveCfg(cfg)
		return fmt.Sprintf("✅ Quét mỗi %d phút (áp dụng từ vòng sau).", n),
			fmt.Sprintf("⚙️ Đổi chu kỳ quét thành <b>%d phút</b>%s.", n, by)
	}
	return "", "" // lệnh lạ: im lặng
}

func firstInt(args []string) (int, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("thiếu số")
	}
	return strconv.Atoi(strings.TrimSpace(args[0]))
}

func normHHMM(s string) string {
	m := hhmmToMin(s)
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// introText: /gioithieu — giải thích cho mọi người trong nhóm. Theo cài đặt hiện tại.
func introText(cfg Config) string {
	var b strings.Builder
	b.WriteString("🤖 <b>GIỚI THIỆU BOT BÁO XE ĐẾN KHO</b>\n")
	b.WriteString("🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n\n")

	b.WriteString("Bot tự theo dõi 2 tab <b>\"Sắp đến kho\"</b> và <b>\"Đã đến kho\"</b> trên GHN ")
	b.WriteString(fmt.Sprintf("mỗi %d phút", cfg.liveMin()))
	if f := cfg.fastMin(); f > 0 {
		b.WriteString(fmt.Sprintf(" (%d phút khi có xe sắp tới)", f))
	}
	if fullDay(cfg) {
		b.WriteString(", cả ngày")
	} else {
		b.WriteString(", từ " + cfg.watchFrom() + " đến " + cfg.watchTo())
	}
	b.WriteString(", rồi nhắn vào nhóm này. Mỗi chuyến chỉ báo khi có việc, không lặp lại.\n\n")

	b.WriteString("🔔 <b>TIN CÓ CHUÔNG — cần chú ý</b>\n")
	b.WriteString("🔵 <b>Xe sắp đến kho</b> — chuyến mới xuất hiện, kèm giờ dự kiến, tài xế, số kiện\n")
	b.WriteString("🟠 <b>Xe đã đến kho</b> — báo 1 lần khi xe vào kho, kèm số kiện cần nhận\n")
	if l := cfg.lateMin(); l > 0 {
		b.WriteString(fmt.Sprintf("⏰ <b>Xe trễ</b> — quá giờ dự kiến %d phút chưa vào kho, kèm SĐT tài xế và vị trí xe\n", l))
	}
	if h := cfg.handoverMin(); h > 0 {
		b.WriteString(fmt.Sprintf("⏳ <b>Xe kẹt bàn giao</b> — vào kho quá %d phút chưa bàn giao xong\n", h))
	}
	b.WriteString("⚠️ <b>Sự cố</b> — app mất kết nối GHN hoặc bị tắt ngang\n\n")

	b.WriteString("🔕 <b>TIN IM LẶNG — xem khi cần</b>\n")
	b.WriteString("🔄 Số kiện của chuyến sắp đến thay đổi\n")
	if !cfg.shiftsOff() {
		b.WriteString("🕐 Tóm tắt đầu ca: xe trong kho + xe sắp đến\n")
	}
	if !fullDay(cfg) {
		b.WriteString("📊 Tổng kết cuối ngày lúc " + cfg.watchTo() + ": số xe, số kiện, xe trễ\n")
	} else {
		b.WriteString("📊 Tổng kết mỗi ngày lúc nửa đêm\n")
	}
	b.WriteString("❔ Chuyến không còn ở \"Sắp đến\" — có thể bị huỷ hoặc đổi kho\n\n")

	b.WriteString("💬 <b>LỆNH AI CŨNG DÙNG ĐƯỢC</b>\n")
	b.WriteString("/xe — xem xe đang trong kho và xe sắp đến\n")
	b.WriteString("/app — xem app có đang chạy không\n")
	b.WriteString("/lenh — tất cả lệnh\n\n")

	b.WriteString("ℹ️ <b>Lưu ý</b>\n")
	b.WriteString("• Giờ dự kiến là theo lịch GHN, không phải GPS.\n")
	b.WriteString("• Bot chỉ đọc dữ liệu, không thao tác gì trên GHN.\n\n")
	b.WriteString("👤 Người quản lý bot: " + managerLink(cfg) + "\n")
	b.WriteString("Bot báo sai, im lặng lâu hoặc có tin ⚠️ → nhắn trực tiếp người quản lý.")
	return b.String()
}

func helpText() string {
	return "🤖 <b>LỆNH CỦA BOT</b>\n" +
		"/gioithieu — bot báo những gì, ý nghĩa ký hiệu\n" +
		"/xe — xe trong kho + xe sắp đến\n" +
		"/app — trạng thái app, cài đặt hiện tại\n" +
		"\n<b>Chỉ chủ app:</b>\n" +
		"/tat 1234 — tạm dừng báo (kèm PIN)\n" +
		"/bat — báo lại\n" +
		"/gio 05:00 21:00 — khung giờ theo dõi\n" +
		"/ca tat — tắt tin đầu ca (hoặc /ca 05:00-08:00,…)\n" +
		"/tre 30 — báo xe trễ quá 30 phút (0 = tắt)\n" +
		"/quet 5 — quét mỗi 5 phút (2–60)\n" +
		"/bangiao 90 — báo xe trong kho quá 90 phút chưa bàn giao xong (0 = tắt)\n" +
		"/quanly Lê Nhật — tên người quản lý hiện trong /gioithieu\n" +
		"/kiemtracapnhat — xem bản mới (chat riêng)\n" +
		"/capnhat 2.15.1 — cài bản đã chọn (chat riêng)"
}

// statusXe: /xe — tình hình kho từ lần quét gần nhất.
func statusXe(cfg Config, now time.Time) string {
	s := getSnapshot()
	var b strings.Builder
	b.WriteString("📋 <b>TÌNH HÌNH KHO — " + now.Format("15:04") + "</b>\n")
	b.WriteString("🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n")
	if s.At.IsZero() {
		b.WriteString("Chưa có số liệu — app chưa quét lần nào từ lúc bật (ngoài khung giờ hoặc vừa khởi động).")
		return b.String()
	}
	if cfg.Paused {
		b.WriteString("⏸ <i>App đang tạm dừng — số liệu có thể đã cũ.</i>\n")
	}
	pkg := func(list []Transport) int {
		n := 0
		for _, it := range list {
			n += totalPkg(it.WillDropOff)
		}
		return n
	}
	b.WriteString(fmt.Sprintf("\n🟠 <b>Đang trong kho: %d xe</b> · %d kiện\n", len(s.Arr), pkg(s.Arr)))
	for i, it := range s.Arr {
		if i >= 8 {
			b.WriteString(fmt.Sprintf("… và %d xe khác\n", len(s.Arr)-i))
			break
		}
		d := "Đến kho lúc " + fmtTimeShort(it.ArrivalTime) + " · " + strconv.Itoa(totalPkg(it.WillDropOff)) + " kiện"
		if st := statusVi(it.StopSessionStatus); st != "-" {
			d += " · " + esc(st)
		}
		b.WriteString(listItem(it, d))
	}
	b.WriteString(fmt.Sprintf("\n🔵 <b>Sắp đến kho: %d xe</b> · %d kiện\n", len(s.Up), pkg(s.Up)))
	for i, it := range s.Up {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("… và %d xe khác\n", len(s.Up)-i))
			break
		}
		d := "Dự kiến đến " + fmtTimeShort(it.ExpectedArrivalTime)
		if l := etaLeft(it.ExpectedArrivalTime, now); l != "" {
			if strings.HasPrefix(l, "trễ ") {
				d += " (⏰ " + l + ")"
			} else {
				d += " (" + l + ")"
			}
		}
		d += " · " + strconv.Itoa(totalPkg(it.WillDropOff)) + " kiện"
		b.WriteString(listItem(it, d))
	}
	if seen := loadSeen(); seen.Day.Pending {
		n := 0
		for _, k := range seen.Day.Arrived {
			n += k
		}
		b.WriteString(fmt.Sprintf("\n📊 Từ đầu ngày: %d xe đã đến · %d kiện · %d xe trễ\n", len(seen.Day.Arrived), n, seen.Day.Late))
	}
	b.WriteString("🕐 Số liệu lúc " + s.At.Format("15:04") + " (lần quét gần nhất)")
	return b.String()
}

// sessionLine: tình trạng SESSION cho /app — luôn có 1 dòng.
func sessionLine(cfg Config, now time.Time) string {
	if cfg.Session == "" {
		if cfg.autoSessionOn() {
			return "🔑 SESSION GHN: chưa có — sẽ tự lấy từ Chrome khi quét"
		}
		return "🔑 SESSION GHN: ❌ chưa có — cần dán ở mục 1"
	}
	exp, ok := jwtExpiredAt(cfg.Session)
	if !ok {
		s := "🔑 SESSION GHN: có, không đọc được hạn"
		if cfg.autoSessionOn() {
			s += " — hết hạn thì app tự lấy lại từ Chrome"
		}
		return s
	}
	left := exp.Sub(now)
	if left <= 0 {
		return "🔑 SESSION GHN: ❌ đã hết hạn lúc " + exp.Local().Format("02/01 15:04") + " — sẽ tự lấy lại từ Chrome"
	}
	return fmt.Sprintf("🔑 SESSION GHN còn: %s (hết lúc %s, tự gia hạn khi còn dưới 4 giờ)",
		durVi(left.Round(time.Minute)), exp.Local().Format("02/01 15:04"))
}

// statusApp: /app — app còn sống không + cài đặt.
func statusApp(cfg Config, now time.Time) string {
	var b strings.Builder
	b.WriteString("🤖 <b>TRẠNG THÁI APP</b> — v" + version + "\n")
	b.WriteString("🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n")
	switch {
	case cfg.Paused:
		b.WriteString("⏸ Đang <b>tạm dừng</b> — gõ /bat để báo lại\n")
	case inWatchWindow(now, cfg):
		b.WriteString("▶️ Đang theo dõi\n")
	default:
		b.WriteString("🌙 Ngoài khung giờ — quét lại lúc " + cfg.watchFrom() + "\n")
	}
	if t := lastScanTime(); !t.IsZero() {
		b.WriteString("🕐 Quét lần cuối: " + t.Format("02/01 15:04") + "\n")
	} else {
		b.WriteString("🕐 Quét lần cuối: chưa quét lần nào từ lúc cài\n")
	}
	b.WriteString(sessionLine(cfg, now) + "\n")
	b.WriteString("⏱ Khung giờ: " + cfg.watchFrom() + "–" + cfg.watchTo() +
		fmt.Sprintf(" · quét %d phút (nhanh %s)\n", cfg.liveMin(), minOrOff(cfg.fastMin())))
	b.WriteString("🕐 Tin đầu ca: " + esc(cfg.shiftsRaw()) + "\n")
	b.WriteString("⏰ Báo xe trễ: " + minOrOff(cfg.lateMin()) + "\n")
	b.WriteString("⏳ Báo kẹt bàn giao: " + minOrOff(cfg.handoverMin()) + "\n")
	b.WriteString("📨 Gửi tới: " + esc(cfg.groupLabel()) + "\n")
	b.WriteString("👤 Liên hệ: " + managerLink(cfg))
	return b.String()
}
