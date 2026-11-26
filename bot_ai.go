package main

import (
	"context"
	"fmt"
	"regexp"
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
	text  string
	draft bool
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
		return aiQuestion{text: strings.Join(args, " "), draft: cmd == "soantin"}, true
	}
	if username == "" {
		return aiQuestion{}, false
	}
	mentioned := false
	text := strings.TrimSpace(m.Text)
	var words []string
	for _, part := range strings.Fields(text) {
		if strings.EqualFold(strings.Trim(part, ".,:;!?()[]{}"), "@"+username) {
			mentioned = true
		} else {
			words = append(words, part)
		}
	}
	replied := m.ReplyTo != nil && m.ReplyTo.From != nil && m.ReplyTo.From.IsBot &&
		strings.EqualFold(m.ReplyTo.From.Username, username)
	if !mentioned && !replied {
		return aiQuestion{}, false
	}
	return aiQuestion{text: strings.Join(words, " ")}, true
}

func (a *botAI) handle(cfg Config, m *tgIncoming, username string, now time.Time) bool {
	question, called := aiQuestionFrom(m, username)
	if !called {
		return false
	}
	if !cfg.AIEnabled || !allowedChat(cfg, m) || m.SenderChat != nil || m.From == nil || m.From.IsBot ||
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
	if _, recent := a.byUser[user]; recent {
		return true
	}
	select {
	case a.queue <- botAIJob{message: m, token: cfg.TelegramToken, question: question}:
		a.last, a.byUser[user] = now, now
	default:
	}
	return true
}

func deliverAIAnswer(ctx context.Context, job botAIJob) {
	cfg := loadCfg()
	if !cfg.AIEnabled || cfg.TelegramToken != job.token || !allowedChat(cfg, job.message) ||
		time.Since(time.Unix(job.message.Date, 0)) > 2*time.Minute {
		return
	}
	reply := answerAIQuestion(ctx, job.question, cfg, getSnapshot(), time.Now(), configuredAIProviders())
	cfg = loadCfg()
	if ctx.Err() != nil || !cfg.AIEnabled || cfg.TelegramToken != job.token || !allowedChat(cfg, job.message) {
		return
	}
	m := job.message
	args := tgSendParams(rawIDString(m.Chat.ID), m.MessageThreadID, esc(reply), true)
	if m.MessageID > 0 {
		args["reply_parameters"] = map[string]interface{}{"message_id": m.MessageID, "allow_sending_without_reply": true}
	}
	_ = tgCall(cfg.TelegramToken, "sendMessage", args)
}

func botAISetting(cfg Config, target chatTarget, args []string) {
	reply := func(s string) { _ = sendTelegramAt(cfg, target, s, true) }
	if len(args) == 1 && (strings.EqualFold(args[0], "bat") || strings.EqualFold(args[0], "tat")) {
		cfg.AIEnabled = strings.EqualFold(args[0], "bat")
		if err := editCfgFile(func(c *Config) { c.AIEnabled = cfg.AIEnabled }); err != nil {
			reply("Chưa lưu được trạng thái AI trên máy kho.")
			return
		}
	}
	status := "Tắt"
	if cfg.AIEnabled {
		status = "Bật"
	}
	if len(configuredAIProviders()) == 0 {
		status += " · Hỏi xe dùng dữ liệu tại máy kho. Trò chuyện và soạn tin cần API key."
	}
	button := tgMenuButton{Text: "Bật trợ lý", Data: "ghn:ai:bat"}
	if cfg.AIEnabled {
		button = tgMenuButton{Text: "Tắt trợ lý", Data: "ghn:ai:tat"}
	}
	sendBotButton(cfg, target, "<b>Trợ lý GHN Tele</b> · "+status+
		"\nKhi có API key, câu trò chuyện hoặc nhờ soạn tin được gửi tới dịch vụ AI đã cấu hình.", button)
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

func answerAIFleet(question string, cfg Config, data snapshot, now time.Time) (string, bool) {
	w := aiWords(question)
	contains := func(phrase string) bool { return strings.Contains(w, " "+phrase+" ") }
	var found *Transport
	var arrived bool
	for i, list := range [][]Transport{data.Arr, data.Up} {
		for j := range list {
			code := strings.TrimSpace(list[j].PartnerTransportationCode)
			if code != "" && strings.Contains(w, aiWords(code)) {
				found, arrived = &list[j], i == 0
				break
			}
		}
		if found != nil {
			break
		}
	}
	fleet := found != nil || aiTripCode.MatchString(question) || contains("xe") || strings.Contains(strings.ToLower(question), "chuyến") ||
		contains("bien so") || contains("tai xe") || contains("eta") || contains("bao nhieu kien") || contains("bao nhieu thung") ||
		contains("bao lau nua") || contains("may gio toi") || contains("trong kho") || contains("sap den")
	if !fleet {
		return "", false
	}
	if contains("dien thoai") || contains("sdt") || contains("so tai xe") || contains("bien so") {
		return "Em có thể báo số xe, mã chuyến và giờ dự kiến. Phần thông tin tài xế anh tra trong hệ thống GHN nha.", true
	}
	freshness := 2 * time.Duration(cfg.liveMin()) * time.Minute
	if freshness < 15*time.Minute {
		freshness = 15 * time.Minute
	}
	if cfg.Paused || data.At.IsZero() || now.Sub(data.At) > freshness {
		return "Em chưa có dữ liệu quét đủ mới để trả lời chắc chắn. Anh dùng /app để xem lần quét gần nhất.", true
	}
	scanTime := " · Quét lúc " + data.At.Format("15:04") + "."
	askCargo := contains("bao nhieu kien") || contains("bao nhieu thung")
	if found != nil {
		if askCargo {
			return fmt.Sprintf("Dạ, chuyến %s có %d kiện cần xuống kho%s", found.PartnerTransportationCode, totalPkg(found.WillDropOff), scanTime), true
		}
		if arrived {
			return fmt.Sprintf("Dạ, chuyến %s đã vào kho lúc %s%s", found.PartnerTransportationCode, fmtTimeShort(found.ArrivalTime), scanTime), true
		}
		return aiArrivalText(*found, now) + scanTime, true
	}
	if aiTripCode.MatchString(question) {
		return "Em chưa thấy mã đó trong dữ liệu lần quét mới nhất" + scanTime, true
	}
	if askCargo {
		total := func(list []Transport) int {
			n := 0
			for _, trip := range list {
				n += totalPkg(trip.WillDropOff)
			}
			return n
		}
		result := fmt.Sprintf("Dạ, xe trong kho có %d kiện cần xuống", total(data.Arr))
		if cfg.upcomingOn() {
			result += fmt.Sprintf(" · Xe sắp đến có %d kiện cần xuống", total(data.Up))
		}
		return result + scanTime, true
	}
	if contains("roi kho") || contains("dang o dau") {
		return "Em chưa có dữ liệu vị trí hoặc xe đã rời kho trong câu trả lời này.", true
	}
	if contains("trong kho") || contains("da den") || contains("da vao") {
		return fmt.Sprintf("Dạ, đang có %d xe trong kho%s", len(data.Arr), scanTime), true
	}
	if !cfg.upcomingOn() {
		return "Máy kho đang tắt quét xe sắp đến.", true
	}
	var next *Transport
	var nextTime time.Time
	for i := range data.Up {
		eta, ok := parseISO(data.Up[i].ExpectedArrivalTime)
		if ok && !eta.Before(now) && (next == nil || eta.Before(nextTime)) {
			next, nextTime = &data.Up[i], eta
		}
	}
	answer := fmt.Sprintf("Dạ, có %d xe đang sắp đến", len(data.Up))
	if next != nil {
		answer += ". " + aiArrivalText(*next, now)
	} else if len(data.Up) > 0 {
		answer += ". Em chưa thấy chuyến nào có giờ dự kiến sắp tới"
	}
	return answer + scanTime, true
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
