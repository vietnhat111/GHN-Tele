package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const aiMaxQuestion = 500

var (
	aiSensitive = regexp.MustCompile(`(?i)(?:\b(?:bearer|api[_ -]?key|bot[_ -]?token|mật khẩu)\b|\beyJ[\w-]+\.[\w-]+\.[\w-]+\b|\b\d{7,13}:[\w-]{20,}\b|(?:\+?\d[. -]?){9,}|\b[\w.+-]+@[\w.-]+\.[a-z]{2,}\b|\b(?:AIza|sk-)[\w-]{12,})`)
	aiTripCode  = regexp.MustCompile(`(?i)\b(?:[a-z]{1,5}\d[a-z\d]{4,}|\d{2}[a-z]\d{4,})\b`)
)

type aiQuestion struct {
	text      string
	draft     bool
	style     string
	replyText string
}

type botAIJob struct {
	message  *tgIncoming
	token    string
	question aiQuestion
}

type botAI struct {
	queue  chan botAIJob
	last   time.Time
	byUser map[string]time.Time
}

func startAIAssistant(stop <-chan struct{}) *botAI {
	a := &botAI{queue: make(chan botAIJob, 3), byUser: make(map[string]time.Time)}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-stop
		cancel()
	}()
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-a.queue:
				if ctx.Err() == nil {
					deliverAIAnswer(ctx, job)
				}
			}
		}
	}()
	return a
}

func aiQuestionFrom(m *tgIncoming, username string) (aiQuestion, bool) {
	cmd, args := cmdArgs(m.Text)
	if cmd != "" {
		if cmd != "hoi" && cmd != "soantin" {
			return aiQuestion{}, false
		}
	}
	mentioned := false
	text := strings.TrimSpace(m.Text)
	if cmd != "" {
		text = strings.Join(args, " ")
		mentioned = true
	}
	var words []string
	for _, part := range strings.Fields(text) {
		if username != "" && strings.EqualFold(strings.Trim(part, ".,:;!?()[]{}"), "@"+username) {
			mentioned = true
		} else {
			words = append(words, part)
		}
	}
	replied := username != "" && m.ReplyTo != nil && m.ReplyTo.From != nil && m.ReplyTo.From.IsBot &&
		strings.EqualFold(m.ReplyTo.From.Username, username)
	text = strings.Join(words, " ")
	if !mentioned && !replied && !(m.Chat.Type == "private" && text != "") {
		return aiQuestion{}, false
	}
	normalized := aiWords(text)
	draft := cmd == "soantin" || strings.Contains(normalized, " soan tin ") || strings.Contains(normalized, " soan thong bao ") ||
		strings.Contains(normalized, " soan giup ") || strings.Contains(normalized, " soan ho ") || strings.Contains(normalized, " viet giup ") ||
		strings.Contains(normalized, " viet ho ") || strings.Contains(normalized, " viet ban nhap ")
	var replyText string
	if !draft && replied && aiFollowUp(normalized) {
		replyText = m.ReplyTo.Text
	}
	if replyText != "" && !aiTripCode.MatchString(text) {
		ids := aiTripCode.FindAllString(m.ReplyTo.Text, 2)
		if len(ids) == 1 {
			text += " " + ids[0]
		}
	}
	return aiQuestion{text: text, draft: draft, replyText: replyText}, true
}

func aiFollowUp(words string) bool {
	return strings.Contains(words, " ban giao ") || strings.Contains(words, " doi gio ") ||
		strings.Contains(words, " vao luc ") || strings.Contains(words, " den chua ") || strings.Contains(words, " vao chua ") || strings.Contains(words, " con may phut ") ||
		strings.Contains(words, " kien ") || strings.Contains(words, " thung ") ||
		strings.Contains(words, " xong ") || strings.Contains(words, " roi chua ") ||
		strings.Contains(words, " may gio ") || strings.Contains(words, " bao lau ")
}

func (a *botAI) handle(cfg Config, m *tgIncoming, username string, now time.Time) bool {
	question, called := aiQuestionFrom(m, username)
	if !called {
		return false
	}
	if !aiChatAllowed(cfg, m) || m.SenderChat != nil || m.From == nil || m.From.IsBot ||
		m.Date <= 0 || now.Sub(time.Unix(m.Date, 0)) > 2*time.Minute {
		return true
	}
	user := rawIDString(m.From.ID)
	if user == "" || (!a.last.IsZero() && now.Sub(a.last) < 4*time.Second) {
		return true
	}
	for id, t := range a.byUser {
		if now.Sub(t) >= 25*time.Second {
			delete(a.byUser, id)
		}
	}
	cooldown := 25 * time.Second
	if question.replyText != "" {
		cooldown = 5 * time.Second
	}
	if last, recent := a.byUser[user]; recent && now.Sub(last) < cooldown {
		return true
	}
	select {
	case a.queue <- botAIJob{message: m, token: cfg.TelegramToken, question: question}:
		a.last, a.byUser[user] = now, now
	default:
	}
	return true
}

func aiChatAllowed(cfg Config, m *tgIncoming) bool {
	return cfg.AIEnabled && allowedChat(cfg, m) && (m.Chat.Type == "private" || !cfg.GroupAIOff)
}

func deliverAIAnswer(ctx context.Context, job botAIJob) {
	cfg := loadCfg()
	m := job.message
	now := time.Now()
	if !aiChatAllowed(cfg, m) || cfg.TelegramToken != job.token || now.Sub(time.Unix(m.Date, 0)) > 2*time.Minute {
		return
	}
	q := job.question
	var reply string
	var buttons [][]tgMenuButton
	history := len([]rune(q.text)) <= aiMaxQuestion && !q.draft && tripLookupAllowed(cfg, m, m.From)
	if history && q.replyText != "" {
		referenced := tripsReferencedIn(cfg, q.replyText, now)
		// A question for the whole fleet is handled by the live assistant.
		if len(referenced) > 0 && !strings.Contains(aiWords(q.text), " xe nao ") {
			ids := aiTripCode.FindAllString(q.text, 2)
			if len(ids) == 1 {
				referenced = findTripRecords(cfg, ids[0], now)
			}
			reply, buttons = tripLookupResult(cfg, m, referenced, q.text, now)
		} else if !strings.Contains(aiWords(q.text), " xe nao ") && len(aiTripCode.FindAllString(q.replyText, 2)) > 1 {
			reply = "Tin trên có nhiều chuyến. Anh gửi mã chuyến cần xem giúp em nha."
		}
	}
	if history && reply == "" && asksTripHistory(q.text) && !aiSensitive.MatchString(q.text) {
		reply, buttons = tripLookupResult(cfg, m, findTripRecords(cfg, q.text, now), q.text, now)
	}
	usedHistory := reply != ""
	if !usedHistory {
		reply = answerAIQuestion(ctx, q, cfg, getSnapshot(), now, configuredAIProviders())
	}
	latest := loadCfg()
	if ctx.Err() != nil || !aiChatAllowed(latest, m) || latest.TelegramToken != job.token || latest.LocationID != cfg.LocationID ||
		(usedHistory && !tripLookupAllowed(latest, m, m.From)) {
		return
	}
	sendTripReply(latest, m, reply, buttons)
}

func botAISetting(cfg Config, target chatTarget, args []string) {
	if len(args) == 1 {
		action := strings.ToLower(args[0])
		switch action {
		case "bat", "tat", "gon", "emut", "group-on", "group-off", "history-on", "history-off", "days3", "days7", "days14":
			if err := editCfgFile(func(c *Config) {
				switch action {
				case "bat", "tat":
					c.AIEnabled = action == "bat"
				case "gon", "emut":
					c.AIStyle = action
				case "group-on", "group-off":
					c.GroupAIOff = action == "group-off"
				case "history-on", "history-off":
					c.GroupHistoryOff = action == "history-off"
				case "days3", "days7", "days14":
					c.HistoryDays, _ = strconv.Atoi(strings.TrimPrefix(action, "days"))
				}
				cfg = *c
			}); err != nil {
				_ = sendTelegramAt(cfg, target, "Chưa lưu được cài đặt AI trên máy kho.", true)
				return
			}
		case "demo":
			_ = sendTelegramAt(cfg, target, "🧪 <b>Ví dụ minh họa</b>\n"+esc(aiFleetVoice(cfg,
				"Dạ anh, chuyến E123456 dự kiến đến lúc 13:15, còn 20 phút.", true)), true)
			return
		}
	}
	status := "Tắt"
	toggle := tgMenuButton{Text: "Bật trợ lý", Data: "ghn:ai:bat"}
	if cfg.AIEnabled {
		status = "Bật"
		toggle = tgMenuButton{Text: "Tắt trợ lý", Data: "ghn:ai:tat"}
	}
	style := "Em út vui vẻ"
	if cfg.AIStyle == "gon" {
		style = "Gọn việc"
	}
	info := "Hỏi xe dùng dữ liệu ở máy kho. Trò chuyện và soạn tin dùng dịch vụ AI đã cấu hình."
	if len(configuredAIProviders()) == 0 {
		info = "Hỏi xe dùng dữ liệu ở máy kho. Trò chuyện và soạn tin cần API key."
	}
	groupAIStatus, groupHistoryStatus := "Bật", "Bật"
	groupAI := tgMenuButton{Text: "Tắt AI nhóm", Data: "ghn:ai:group-off"}
	if cfg.GroupAIOff {
		groupAIStatus = "Tắt"
		groupAI = tgMenuButton{Text: "Bật AI nhóm", Data: "ghn:ai:group-on"}
	} else if !cfg.AIEnabled {
		groupAIStatus = "Chờ bật trợ lý"
	}
	groupHistory := tgMenuButton{Text: "Tắt tra cứu nhóm", Data: "ghn:ai:history-off"}
	if cfg.GroupHistoryOff {
		groupHistoryStatus = "Tắt"
		groupHistory = tgMenuButton{Text: "Bật tra cứu nhóm", Data: "ghn:ai:history-on"}
	}
	rows := [][]tgMenuButton{{toggle}, {groupAI, groupHistory}}
	if target.chat == cfg.TelegramPersonal {
		rows = append(rows, []tgMenuButton{{Text: "Gọn việc", Data: "ghn:ai:gon"}, {Text: "Em út vui vẻ", Data: "ghn:ai:emut"}},
			[]tgMenuButton{{Text: "Lưu 3 ngày", Data: "ghn:ai:days3"}, {Text: "Lưu 7 ngày", Data: "ghn:ai:days7"}, {Text: "Lưu 14 ngày", Data: "ghn:ai:days14"}},
			[]tgMenuButton{{Text: "Thử giọng bot", Data: "ghn:ai:demo"}, {Text: "Về bảng điều khiển", Data: "mobile:home"}})
	}
	sendBotRows(cfg, target, "<b>Trợ lý GHN Tele</b> · "+status+" · "+style+"\nAI nhóm: "+groupAIStatus+" · Tra cứu nhóm: "+groupHistoryStatus+
		"\nLưu lịch sử: "+strconv.Itoa(cfg.historyDays())+" ngày\n"+info, rows)
}

func answerAIQuestion(ctx context.Context, q aiQuestion, cfg Config, data snapshot, now time.Time, providers []aiProvider) string {
	if q.text == "" {
		return "Dạ, anh hỏi thử «xe nào sắp đến?» hoặc dùng /soantin để em soạn một tin nhắn ngắn."
	}
	if len([]rune(q.text)) > aiMaxQuestion {
		return "Anh gửi câu hỏi ngắn hơn giúp em, tối đa 500 ký tự nha."
	}
	if !q.draft {
		if answer, ok := answerAIFleet(q.text, cfg, data, now); ok {
			return answer
		}
	}
	if aiSensitive.MatchString(q.text) || aiTripCode.MatchString(q.text) {
		return "Tin này có thể chứa thông tin riêng. Anh bỏ phần đó rồi gửi lại giúp em."
	}
	if len(providers) == 0 {
		return "Em chưa được cấu hình AI trò chuyện. Anh vẫn có thể hỏi xe sắp đến hoặc xe đang ở kho."
	}
	q.style = cfg.AIStyle
	if reply := askAIProviders(ctx, q, providers); reply != "" {
		return reply
	}
	return "Em chưa kết nối được dịch vụ AI. Anh thử lại sau nha. Dùng /xe để xem số liệu kho."
}

func aiWords(question string) string {
	return " " + strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune("àáảãạăằắẳẵặâầấẩẫậ", r):
			return 'a'
		case strings.ContainsRune("èéẻẽẹêềếểễệ", r):
			return 'e'
		case strings.ContainsRune("ìíỉĩị", r):
			return 'i'
		case strings.ContainsRune("òóỏõọôồốổỗộơờớởỡợ", r):
			return 'o'
		case strings.ContainsRune("ùúủũụưừứửữự", r):
			return 'u'
		case strings.ContainsRune("ỳýỷỹỵ", r):
			return 'y'
		case r == 'đ':
			return 'd'
		case unicode.Is(unicode.Mn, r):
			return -1
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ' '
		}
		return r
	}, strings.ToLower(question))), " ") + " "
}

func aiArrivalText(item Transport, now time.Time) string {
	ans := "Chuyến " + item.PartnerTransportationCode
	if _, ok := parseISO(item.ExpectedArrivalTime); !ok {
		return ans + " chưa có giờ dự kiến"
	}
	ans += " dự kiến đến lúc " + fmtTimeShort(item.ExpectedArrivalTime)
	if left := etaLeft(item.ExpectedArrivalTime, now); left != "" {
		ans += " (" + left + ")"
	}
	return ans
}
