package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func botTestDir(t *testing.T) func() {
	dir := t.TempDir()
	oe, oc, ol := exeDir, cfgPath, logPath
	exeDir, cfgPath, logPath = dir, dir+"/config.json", dir+"/log"
	return func() { exeDir, cfgPath, logPath = oe, oc, ol }
}

func user(id string) *tgUser { return &tgUser{ID: json.RawMessage(id), FirstName: "Lê"} }

func TestCmdArgs(t *testing.T) {
	c, a := cmdArgs("/gio@kho_bot 05:00 21:00")
	if c != "gio" || len(a) != 2 || a[1] != "21:00" {
		t.Errorf("cmdArgs: %q %v", c, a)
	}
	if c, _ := cmdArgs("chào cả nhà"); c != "" {
		t.Error("tin thường không phải lệnh")
	}
}

func TestOwnerOnlyAndSettings(t *testing.T) {
	defer botTestDir(t)()
	cfg := Config{LocationID: "22127000", TelegramToken: "t", TelegramChat: "-100", TelegramPersonal: "42"}
	_ = saveCfg(cfg)
	now := time.Now()

	// người khác trong nhóm không đổi được
	if r, _ := runCommand(cfg, "quet", []string{"2"}, user("7"), isOwner(cfg, user("7")), now); !strings.Contains(r, "chỉ chủ app") {
		t.Errorf("người khác phải bị chặn: %s", r)
	}
	// ai cũng xem được
	if r, _ := runCommand(cfg, "app", nil, user("7"), false, now); !strings.Contains(r, "TRẠNG THÁI APP") {
		t.Errorf("/app: %s", r)
	}
	owner := isOwner(cfg, user("42"))
	if !owner {
		t.Fatal("chat riêng đã cài = chủ app")
	}
	// /quet ngoài giới hạn
	if r, _ := runCommand(cfg, "quet", []string{"1"}, user("42"), true, now); !strings.Contains(r, "2–60") {
		t.Errorf("/quet 1 phải bị từ chối: %s", r)
	}
	r, n := runCommand(cfg, "quet", []string{"3"}, user("42"), true, now)
	if loadCfg().liveMin() != 3 || !strings.Contains(n, "3 phút") || !strings.Contains(n, "bởi Lê") {
		t.Errorf("/quet 3: %s | %s", r, n)
	}
	cfg = loadCfg()
	runCommand(cfg, "gio", []string{"5:00", "21:30"}, user("42"), true, now)
	if c := loadCfg(); c.watchFrom() != "05:00" || c.watchTo() != "21:30" {
		t.Errorf("/gio: %s–%s", c.watchFrom(), c.watchTo())
	}
	cfg = loadCfg()
	runCommand(cfg, "ca", []string{"tat"}, user("42"), true, now)
	if !loadCfg().shiftsOff() {
		t.Error("/ca tat phải tắt ca")
	}
	cfg = loadCfg()
	runCommand(cfg, "tre", []string{"0"}, user("42"), true, now)
	if loadCfg().lateMin() != 0 {
		t.Error("/tre 0 phải tắt báo trễ")
	}
}

func TestPauseNeedsPIN(t *testing.T) {
	defer botTestDir(t)()
	cfg := Config{LocationID: "1", TelegramToken: "t", TelegramChat: "-100", TelegramPersonal: "42"}
	_ = saveCfg(cfg)
	now := time.Now()
	if r, _ := runCommand(cfg, "tat", nil, user("42"), true, now); !strings.Contains(r, "PIN") {
		t.Errorf("/tat không PIN phải nhắc: %s", r)
	}
	if r, _ := runCommand(cfg, "tat", []string{"0000"}, user("42"), true, now); !strings.Contains(r, "Sai PIN") || loadCfg().Paused {
		t.Errorf("sai PIN không được dừng: %s", r)
	}
	r, n := runCommand(loadCfg(), "tat", []string{"1234"}, user("42"), true, now)
	if !loadCfg().Paused || !strings.Contains(n, "Tạm dừng") {
		t.Errorf("PIN đúng phải tạm dừng: %s | %s", r, n)
	}
	r, n = runCommand(loadCfg(), "bat", nil, user("42"), true, now)
	if loadCfg().Paused || !strings.Contains(n, "trở lại") {
		t.Errorf("/bat: %s | %s", r, n)
	}
}

func TestStatusXe(t *testing.T) {
	defer botTestDir(t)()
	now := time.Now()
	cfg := Config{LocationID: "22127000"}
	if !strings.Contains(statusXe(cfg, now), "Chưa có số liệu") {
		t.Error("chưa quét thì phải nói chưa có số liệu")
	}
	in := sampleArrived()
	late := tripAt("TRE", now.Add(-45*time.Minute))
	setSnapshot([]Transport{in}, []Transport{late, tripAt("SAP", now.Add(40*time.Minute))}, now.Add(-2*time.Minute))
	defer setSnapshot(nil, nil, time.Time{})
	m := plain(statusXe(cfg, now))
	for _, want := range []string{"Đang trong kho: 1 xe", "Sắp đến kho: 2 xe", "⏰ trễ 45 phút", "còn 40 phút", "Số liệu lúc"} {
		if !strings.Contains(m, want) {
			t.Errorf("/xe thiếu %q:\n%s", want, m)
		}
	}
}

// Vòng nghe lệnh thật với Telegram giả: bỏ lệnh tồn đọng, trả lời /xe, chặn chat lạ.
func TestBotLoopOverHTTP(t *testing.T) {
	defer botTestDir(t)()
	_ = saveCfg(Config{LocationID: "22127000", TelegramToken: "t", TelegramChat: "-100", TelegramPersonal: "42"})

	var mu sync.Mutex
	var replies []map[string]interface{}
	calls := 0
	now := time.Now().Unix()
	msg := func(id int, chat, from, text string, date int64) string {
		return fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"date":%d,"text":%q,"chat":{"id":%s,"type":"group","title":"Kho"},"from":{"id":%s,"first_name":"Lê"}}}`,
			id, id, date, text, chat, from)
	}
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		_ = json.Unmarshal(b, &p)
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"kho_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			calls++
			switch calls {
			case 1: // skipBacklog: lệnh /tat cũ tồn đọng — không được thực hiện
				_, _ = w.Write([]byte(`{"ok":true,"result":[` + msg(5, "42", "42", "/tat 1234", now-3600) + `]}`))
			case 2:
				_, _ = w.Write([]byte(`{"ok":true,"result":[` +
					msg(6, "-100", "7", "/xe@kho_bot", now) + "," +
					msg(7, "-999", "8", "/xe", now) + "," +
					msg(8, "-100", "42", "/xe@bot_khac", now) + `]}`)) // chat lạ
			default:
				mu.Unlock()
				time.Sleep(200 * time.Millisecond)
				mu.Lock()
				_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
			}
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			replies = append(replies, p)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer tg.Close()
	ot := tgBase
	tgBase = tg.URL
	defer func() { tgBase = ot }()

	stop := make(chan struct{})
	startBot(stop)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(replies)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	close(stop)

	mu.Lock()
	defer mu.Unlock()
	if len(replies) != 1 {
		t.Fatalf("phải đúng 1 câu trả lời (/xe trong nhóm), được %d: %v", len(replies), replies)
	}
	r := replies[0]
	if r["chat_id"] != "-100" || r["disable_notification"] != true || !strings.Contains(r["text"].(string), "TÌNH HÌNH KHO") {
		t.Errorf("trả lời /xe sai: %v", r)
	}
	if loadCfg().Paused {
		t.Error("lệnh /tat tồn đọng từ trước không được thực hiện")
	}
	if st := loadBotState(); st.Offset != 9 || len(st.Chats) == 0 {
		t.Errorf("offset/chats chưa lưu: %+v", st)
	}
}

func TestIntroFollowsSettings(t *testing.T) {
	cfg := Config{LocationID: "22127000"}
	m := plain(introText(cfg))
	for _, want := range []string{"GIỚI THIỆU BOT", "mỗi 5 phút (2 phút khi có xe sắp tới)", "từ 05:00 đến 21:00",
		"TIN CÓ CHUÔNG", "quá giờ dự kiến 30 phút", "TIN IM LẶNG", "Tóm tắt đầu ca", "lúc 21:00", "/xe", "không phải GPS"} {
		if !strings.Contains(m, want) {
			t.Errorf("thiếu %q:\n%s", want, m)
		}
	}
	off := Config{LocationID: "1", LateMin: intPtr(0), Shifts: "tắt", WatchFrom: "00:00", WatchTo: "23:59"}
	m = plain(introText(off))
	if strings.Contains(m, "Xe trễ") || strings.Contains(m, "đầu ca") || !strings.Contains(m, "cả ngày") {
		t.Errorf("phải theo cài đặt (tắt trễ, tắt ca, cả ngày):\n%s", m)
	}
	if r, _ := runCommand(cfg, "gioithieu", nil, user("7"), false, time.Now()); !strings.Contains(r, "GIỚI THIỆU") {
		t.Error("ai trong nhóm cũng gõ được /gioithieu")
	}
	if r, _ := runCommand(cfg, "start", nil, user("7"), false, time.Now()); !strings.Contains(r, "GIỚI THIỆU") {
		t.Error("/start (lúc thêm bot vào nhóm) cũng ra giới thiệu")
	}
}

func TestManagerLink(t *testing.T) {
	c := Config{TelegramPersonal: "42", ManagerName: "Lê Nhật"}
	if got := managerLink(c); got != `<a href="tg://user?id=42">Lê Nhật</a>` {
		t.Errorf("không có username → link theo mã tài khoản: %s", got)
	}
	c.ManagerUser = "lenhat"
	if got := managerLink(c); got != `<a href="https://t.me/lenhat">Lê Nhật</a>` {
		t.Errorf("có username → t.me: %s", got)
	}
	if got := managerLink(Config{ManagerName: "A<b>"}); got != "A&lt;b&gt;" {
		t.Errorf("chưa có chủ app → chỉ hiện tên (đã escape): %s", got)
	}
	m := introText(c)
	if !strings.Contains(m, `👤 Người quản lý bot: <a href="https://t.me/lenhat">Lê Nhật</a>`) {
		t.Errorf("/gioithieu phải có người quản lý bấm được:\n%s", m)
	}
	if !strings.Contains(statusApp(c, time.Now()), "Liên hệ: <a href") {
		t.Error("/app phải có liên hệ")
	}
	if n, _ := restartNotice(c, "killed", time.Now(), time.Now()); !strings.Contains(n.Text, "Liên hệ: <a href") {
		t.Error("tin bị tắt ngang phải có liên hệ")
	}
}

func TestQuanlyAndFill(t *testing.T) {
	defer botTestDir(t)()
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getChat") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42,"type":"private","first_name":"Lê","last_name":"Nhật","username":"lenhat"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	ot := tgBase
	tgBase = tg.URL
	defer func() { tgBase = ot }()

	cfg := Config{LocationID: "1", TelegramToken: "t", TelegramChat: "-100", TelegramPersonal: "42"}
	_ = saveCfg(cfg)
	fillManager(cfg)
	if c := loadCfg(); c.ManagerName != "Lê Nhật" || c.ManagerUser != "lenhat" {
		t.Fatalf("tự lấy tên + username: %+v", c)
	}
	if r, _ := runCommand(loadCfg(), "quanly", []string{"x"}, user("7"), false, time.Now()); !strings.Contains(r, "chỉ chủ app") {
		t.Error("người khác không đổi được người quản lý")
	}
	r, n := runCommand(loadCfg(), "quanly", []string{"Anh", "Nhật", "-", "kho", "Huế"}, user("42"), true, time.Now())
	if loadCfg().ManagerName != "Anh Nhật - kho Huế" || !strings.Contains(r, "t.me/lenhat") || !strings.Contains(n, "Đổi người quản lý") {
		t.Errorf("/quanly: %s | %s", r, n)
	}
	// đã đặt tên thì fillManager không ghi đè
	fillManager(loadCfg())
	if loadCfg().ManagerName != "Anh Nhật - kho Huế" {
		t.Error("tên tự đặt không được bị ghi đè")
	}
}

func TestSessionLineAlwaysShown(t *testing.T) {
	now := time.Now()
	if l := sessionLine(Config{}, now); !strings.Contains(l, "chưa có") {
		t.Error(l)
	}
	if l := sessionLine(Config{Session: "khong-phai-jwt"}, now); !strings.Contains(l, "không đọc được hạn") || !strings.Contains(l, "tự lấy lại") {
		t.Error(l)
	}
	mk := func(d time.Duration) string {
		exp := `{"expired":"` + now.Add(d).Format(time.RFC3339) + `"}`
		return "h." + base64.RawURLEncoding.EncodeToString([]byte(exp)) + ".s"
	}
	if l := sessionLine(Config{Session: mk(10*time.Hour + 30*time.Minute)}, now); !strings.Contains(l, "còn: 10 giờ 30 phút") {
		t.Error(l)
	}
	if l := sessionLine(Config{Session: mk(-time.Hour)}, now); !strings.Contains(l, "đã hết hạn") {
		t.Error(l)
	}
	defer botTestDir(t)()
	m := statusApp(Config{LocationID: "1"}, now)
	if !strings.Contains(m, "chưa quét lần nào") || !strings.Contains(m, "🔑 SESSION GHN") {
		t.Errorf("/app phải luôn có dòng quét + SESSION:\n%s", m)
	}
}
