package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// App chỉ làm 3 việc: báo xe sắp đến, báo xe đã đến, và không báo trùng.

var (
	lastScanUnix atomic.Int64
)

// ---------------------------------------------------------------- đã báo gì

type SeenStore struct {
	HistoryCheck int64                  `json:"tripHistoryCheck,omitempty"`
	History      map[string]tripRecord  `json:"tripHistory,omitempty"`
	ScanAt       int64                  `json:"scanAt,omitempty"`
	Arrived      map[string]int64       `json:"arrived"` // mã chuyến → đã báo đến
	Sig          map[string]string      `json:"sig"`     // chữ ký số kiện đã báo gần nhất
	SigAt        map[string]int64       `json:"sigAt"`   // lần cuối thấy chuyến ở Sắp đến (để dọn chuyến huỷ)
	Late         map[string]int64       `json:"late"`    // chuyến đã báo ⏰ trễ (báo 1 lần)
	Last         map[string]Transport   `json:"last"`    // bản cuối thấy ở Sắp đến (để báo khi chuyến biến mất)
	Missing      map[string]int         `json:"missing"` // số lần quét liên tiếp không thấy chuyến
	Departed     map[string]departedHit `json:"departed,omitempty"`
	InKho        map[string]inKho       `json:"inKho"` // xe đang ở tab Đã đến kho (theo dõi bàn giao)

	// số liệu cho 📊 tổng kết — cộng dồn trong khung giờ, xoá khi đã gửi
	Day DayStats `json:"day"`
}

type DayStats struct {
	Pending bool           `json:"pending"` // đã quét trong khung, chưa gửi tổng kết
	From    int64          `json:"from"`    // lần quét đầu của phiên (ms)
	Arrived map[string]int `json:"arrived"` // mã chuyến vào kho → số kiện
	Late    int            `json:"late"`    // số chuyến đã báo trễ
	Done    map[string]int `json:"done"`    // mã chuyến → phút bàn giao (-1 = không rõ)
	Stuck   int            `json:"stuck"`   // số xe đã báo kẹt bàn giao
	LastUp  int            `json:"lastUp"`  // số xe ở Sắp đến lần quét cuối
	LateNow int            `json:"lateNow"` // trong đó đang trễ
}

func newSeen() *SeenStore {
	return &SeenStore{Arrived: map[string]int64{}, Sig: map[string]string{}, SigAt: map[string]int64{},
		Late: map[string]int64{}, Last: map[string]Transport{}, Missing: map[string]int{},
		InKho: map[string]inKho{}, Departed: map[string]departedHit{}, Day: newDay()}
}

func newDay() DayStats {
	return DayStats{Arrived: map[string]int{}, Done: map[string]int{}}
}

func seenFile() string { return filepath.Join(exeDir, "seen.json") }

func loadSeen() *SeenStore {
	b, _ := os.ReadFile(seenFile())
	return decodeSeen(b)
}

func decodeSeen(b []byte) *SeenStore {
	s := newSeen()
	_ = json.Unmarshal(b, s)
	if s.Arrived == nil {
		s.Arrived = map[string]int64{}
	}
	if s.Sig == nil {
		s.Sig = map[string]string{}
	}
	if s.SigAt == nil {
		s.SigAt = map[string]int64{}
	}
	if s.Late == nil {
		s.Late = map[string]int64{}
	}
	if s.Last == nil {
		s.Last = map[string]Transport{}
	}
	if s.Missing == nil {
		s.Missing = map[string]int{}
	}
	if s.Departed == nil {
		s.Departed = map[string]departedHit{}
	}
	if s.InKho == nil {
		s.InKho = map[string]inKho{}
	}
	if s.Day.Done == nil {
		s.Day.Done = map[string]int{}
	}
	if s.Day.Arrived == nil {
		s.Day.Arrived = map[string]int{}
	}
	// seen.json bản cũ (≤2.2.0) chưa có sigAt → coi như vừa thấy, 3 ngày sau mới dọn
	now := time.Now().UnixMilli()
	for k := range s.Sig {
		if _, ok := s.SigAt[k]; !ok {
			s.SigAt[k] = now
		}
	}
	return s
}

// cleanSeen: quên chuyến quá 3 ngày — cả chuyến đã đến lẫn chuyến
// biến mất khỏi Sắp đến mà không bao giờ vào kho (huỷ, đổi kho...).
func cleanSeen(s *SeenStore) {
	cut := time.Now().AddDate(0, 0, -3).UnixMilli()
	for k, t := range s.Arrived {
		if t < cut {
			delete(s.Arrived, k)
			delete(s.Departed, k)
			delete(s.Sig, k)
			delete(s.SigAt, k)
		}
	}
	for k := range s.Sig {
		if t, ok := s.SigAt[k]; !ok || t < cut {
			delete(s.Sig, k)
			delete(s.SigAt, k)
		}
	}
	for k := range s.SigAt {
		if _, ok := s.Sig[k]; !ok {
			delete(s.SigAt, k)
		}
	}
	for k := range s.Late {
		if _, ok := s.Sig[k]; !ok {
			delete(s.Late, k) // đã vào kho hoặc biến mất
		}
	}
	for k := range s.Last {
		if _, ok := s.Sig[k]; !ok {
			delete(s.Last, k)
		}
	}
	for k := range s.Missing {
		if _, ok := s.Sig[k]; !ok {
			delete(s.Missing, k)
		}
	}
}

func markScan() {
	now := time.Now()
	lastScanUnix.Store(now.Unix())
	b, _ := json.Marshal(map[string]int64{"lastScan": now.Unix()})
	_ = os.WriteFile(filepath.Join(exeDir, "lastscan.json"), b, 0600)
}

func lastScanTime() time.Time {
	if b, err := os.ReadFile(filepath.Join(exeDir, "lastscan.json")); err == nil {
		var v struct {
			LastScan int64 `json:"lastScan"`
		}
		if json.Unmarshal(b, &v) == nil && v.LastScan > 0 {
			return time.Unix(v.LastScan, 0)
		}
	}
	if v := lastScanUnix.Load(); v > 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}

// ---------------------------------------------------------------- giờ / ca

// parseHHMM: "HH:MM" → số phút trong ngày.
func parseHHMM(s string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, false
	}
	hh, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	mm, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if e1 != nil || e2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// có hàng trong xe = có ít nhất 1 kiện
func hasCargo(it Transport) bool {
	return totalPkg(it.WillDropOff) != 0
}

func hhmmToMin(s string) int {
	if m, ok := parseHHMM(s); ok {
		return m
	}
	return -1
}

func inSpan(nowMin, start, end int) bool {
	if start == end {
		return true
	}
	if start < end {
		return nowMin >= start && nowMin < end
	}
	return nowMin >= start || nowMin < end
}

func inWatchWindow(now time.Time, cfg Config) bool {
	from := hhmmToMin(cfg.watchFrom())
	to := hhmmToMin(cfg.watchTo())
	if from < 0 || to < 0 {
		return true
	}
	return inSpan(now.Hour()*60+now.Minute(), from, to)
}

// ---------------------------------------------------------------- ca

type Shift struct {
	Name  string
	Start int
	End   int
}

func parseShifts(s string) []Shift {
	var out []Shift
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		segs := strings.SplitN(p, "-", 2)
		if len(segs) != 2 {
			continue
		}
		st, ok1 := parseHHMM(segs[0])
		en, ok2 := parseHHMM(segs[1])
		if !ok1 || !ok2 {
			continue
		}
		out = append(out, Shift{
			Name:  fmt.Sprintf("%02d:%02d-%02d:%02d", st/60, st%60, en/60, en%60),
			Start: st,
			End:   en,
		})
	}
	return out
}

func currentShift(now time.Time, cfg Config) (Shift, bool) {
	nm := now.Hour()*60 + now.Minute()
	var best *Shift
	bestLen := -1
	shifts := parseShifts(cfg.shiftsRaw())
	for i := range shifts {
		sh := shifts[i]
		if !inSpan(nm, sh.Start, sh.End) {
			continue
		}
		length := sh.End - sh.Start
		if length < 0 {
			length += 24 * 60
		}
		if length > bestLen {
			bestLen = length
			best = &shifts[i]
		}
	}
	if best == nil {
		return Shift{}, false
	}
	return *best, true
}

// ---------------------------------------------------------------- vòng quét

// resumeWithin: bật lại trong khoảng này kể từ lần quét cuối thì "tiếp tục" —
// báo bù mọi thay đổi trong lúc tắt (tin đã gộp nên không bị dội).
// Quá lâu (hoặc chưa từng chạy) thì lần đầu chỉ ghi nhớ, không báo.
const resumeWithin = 24 * time.Hour

// canResume: seen.json còn mới và có dữ liệu.
func canResume(seen *SeenStore, last, now time.Time) bool {
	if last.IsZero() || now.Sub(last) > resumeWithin || now.Before(last) {
		return false
	}
	return seen.ScanAt > 0 || len(seen.Arrived)+len(seen.Sig) > 0
}

func watch() {
	if updateInProgress() {
		return
	}
	cfg := loadCfg()
	if !cfgReady(cfg) {
		fmt.Println("Chưa đủ cấu hình. Mở exe (không tham số) để cài trước.")
		waitEnter()
		return
	}
	release, err := acquireWatchLock()
	if err != nil {
		fmt.Println("⚠️ " + err.Error())
		fmt.Println("   Đang có tiến trình khác theo dõi. Xem mục 5 để dừng nó.")
		waitEnter()
		return
	}
	defer release()
	if updateInProgress() {
		return
	}
	reason := restartReason(fileExists(runningFlagPath()) && !stopIntended(), lastScanTime(), bootTime())
	clearStopIntent()
	markRunning()
	defer clearRunning() // thoát đúng cách → không tính là bị tắt ngang
	ensureTasks(cfg)
	refreshStopShortcut()
	watchStop := make(chan struct{})
	startBot(watchStop) // nghe lệnh /xe, /app, /tat… trong lúc theo dõi
	startAutomaticUpdates(watchStop)
	startLiveQueue()
	defer close(watchStop)
	if m, ok := restartNotice(cfg, reason, lastScanTime(), time.Now()); ok {
		logf("[guard] Lần trước kết thúc bất thường (%s) → báo vào nhóm", reason)
		enqueue(m)
	}

	fmt.Printf("👀 Theo dõi mỗi %d phút (nhanh %s) | Khung %s–%s | Ca: %s | Báo trễ: %s\n",
		cfg.liveMin(), minOrOff(cfg.fastMin()), cfg.watchFrom(), cfg.watchTo(), cfg.shiftsRaw(), minOrOff(cfg.lateMin()))
	if !inWatchWindow(time.Now(), cfg) {
		fmt.Printf("⚠️ %s nằm ngoài khung → chờ tới %s mới quét.\n",
			time.Now().Format("15:04"), cfg.watchFrom())
	}
	logf("[watch] Bắt đầu: mỗi %d phút (nhanh %s), khung %s-%s, ca %s, báo trễ %s",
		cfg.liveMin(), minOrOff(cfg.fastMin()), cfg.watchFrom(), cfg.watchTo(), cfg.shiftsRaw(), minOrOff(cfg.lateMin()))

	seen := loadCommittedSeen(cfg)
	publishTripCatalog(cfg.LocationID, seen.History)
	last := lastScanTime()
	if seen.ScanAt > 0 {
		last = time.Unix(seen.ScanAt, 0)
	}
	location := cfg.LocationID
	// silent = chưa có gốc để so. Chỉ hạ cờ khi quét THÀNH CÔNG, nếu không lần
	// quét đầu lỗi sẽ làm lần sau coi mọi chuyến là mới.
	silent := !canResume(seen, last, time.Now())
	opening := true // tin mở đầu (👀 hoặc ▶️) chưa gửi
	if silent {
		logf("[watch] Chưa có dữ liệu gần đây → lần quét đầu chỉ ghi nhớ")
	} else {
		logf("[watch] Tiếp tục từ lần quét %s → báo bù thay đổi", last.Format("02/01 15:04"))
	}
	curShift := ""
	var request *chatTarget
	for {
		if updateInProgress() && waitForNextWatch(time.Second, nil) {
			return
		}
		cfg = loadCfg()
		if !cfgReady(cfg) {
			finishBotScan(cfg, request, "Máy kho chưa sẵn sàng quét.")
			request = nil
			logf("[watch] Mất cấu hình, chờ 1 phút")
			if waitForNextWatch(time.Minute, nil) {
				return
			}
			continue
		}
		if cfg.liveMin() <= 0 {
			finishBotScan(cfg, request, "Theo dõi đã tắt.")
			logf("[watch] Đã tắt theo dõi trong config")
			return
		}
		if request == nil {
			select {
			case queued := <-botScanQueue:
				request = &queued
			default:
			}
		}
		now := time.Now()
		if cfg.LocationID != location {
			seen, location = newSeen(), cfg.LocationID
			silent, opening, curShift = true, true, ""
			setSnapshot(nil, nil, time.Time{})
			publishTripCatalog(location, nil)
		}
		var up []Transport
		inWin := inWatchWindow(now, cfg)
		if request != nil && (!inWin || cfg.Paused) {
			finishBotScan(cfg, request, "App đang tạm dừng hoặc ngoài khung giờ quét.")
			request = nil
		}
		if summaryDue(cfg, seen.Day, now, inWin) {
			// hết khung giờ (hoặc máy bật lại sau giờ / qua ngày) → tổng kết một lần
			day := seen.Day
			seen.Day = newDay()
			if err := commitScan(cfg, seen, []outMsg{quiet(summaryMsg(cfg, day, now))}, now); err != nil {
				seen.Day = day
				logf("[watch] %v", err)
			}
		}
		if inWin && !cfg.Paused {
			arr, upList, msgs, ok := safePoll(&cfg, seen, silent)
			up = upList
			fullScan := ok && latestScanHealth().result == scanHealthy
			sh, hasShift := currentShift(now, cfg)
			if fullScan && opening {
				var notice string
				if silent {
					notice = startMsg(cfg, arr, up)
				} else {
					notice = resumeMsg(cfg, last, len(msgs), arr, up)
				}
				msgs = append([]outMsg{quiet(notice)}, msgs...)
			}
			if fullScan && !opening && hasShift && curShift != sh.Name {
				msgs = append(msgs, quiet(shiftMsg(cfg, sh.Name, arr, up)))
			}
			if fullScan {
				seen.ScanAt = now.Unix()
			}
			if ok {
				if err := commitScan(cfg, seen, msgs, now); err != nil {
					logf("[watch] %v", err)
					ok, fullScan = false, false
					recordScanHealth(scanStore)
					seen = loadCommittedSeen(cfg)
				} else {
					publishTripCatalog(cfg.LocationID, seen.History)
				}
			} else {
				seen = loadCommittedSeen(cfg)
			}
			if fullScan {
				markScan()
				setWarehouseSnapshot(arr, upList, seen.Departed, time.Now(), cfg.LocationID)
				opening, silent, curShift = false, false, sh.Name
			}

			if request != nil {
				finishBotScan(cfg, request, botScanResult(fullScan, arr, upList))
				request = nil
			}
			// quét lỗi: không đổi ca, không gửi tóm tắt rỗng — vòng sau thử lại
		}
		if waitForNextWatch(nextSleep(cfg, up, time.Now()), &request) {
			logf("[update] Tạm dừng theo dõi để thay EXE")
			return
		}

	}
}

// safePoll: một vòng quét lỗi không được làm chết cả tiến trình.
func safePoll(cfg *Config, seen *SeenStore, silent bool) (arr, up []Transport, msgs []outMsg, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			recordScanHealth(scanFailed)
			logf("[watch] LỖI NGHIÊM TRỌNG: %v", r)
			alert(*cfg, "panic", "⚠️ <b>Vòng quét bị lỗi</b>\n"+esc(fmt.Sprint(r))+"\nApp vẫn chạy tiếp.")
			ok = false
		}
	}()
	return pollLive(cfg, seen, silent)
}

// ---------------------------------------------------------------- lỗi mạng kéo dài

// netAlertAfter: số lần quét lỗi mạng liên tiếp mới báo (≈ 15 phút với chu kỳ 5 phút).
const netAlertAfter = 3

var (
	netMu    sync.Mutex
	netFails int
	netSince time.Time
)

// netFailed: ghi 1 lần lỗi mạng; trả về số phút đã lỗi và có tới lúc báo chưa.
func netFailed(now time.Time) (time.Duration, bool) {
	netMu.Lock()
	defer netMu.Unlock()
	if netFails == 0 {
		netSince = now
	}
	netFails++
	return now.Sub(netSince), netFails >= netAlertAfter
}

// netRecovered: quét thành công → xoá bộ đếm, trả về đã gián đoạn bao lâu (0 nếu không lỗi).
func netRecovered(now time.Time) time.Duration {
	netMu.Lock()
	defer netMu.Unlock()
	var d time.Duration
	if netFails > 0 {
		d = now.Sub(netSince)
	}
	netFails = 0
	return d
}

// netReason: lỗi kỹ thuật → câu dễ hiểu.
func netReason(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "no such host"), strings.Contains(s, "network is unreachable"),
		strings.Contains(s, "connection refused"), strings.Contains(s, "connectex"), strings.Contains(s, "dial tcp"):
		return "máy kho mất mạng (không kết nối được Internet)"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline"):
		return "mạng chậm hoặc máy chủ GHN không phản hồi"
	case strings.Contains(s, "http 5"), strings.Contains(s, "502"), strings.Contains(s, "503"), strings.Contains(s, "504"):
		return "máy chủ GHN đang lỗi"
	}
	return "lỗi kết nối tới GHN"
}

func netAlertMsg(err error, d time.Duration) string {
	return "⚠️ <b>Không kết nối được GHN " + durVi(d.Round(time.Minute)) + "</b>\n" +
		"Nguyên nhân: " + netReason(err) + ".\n" +
		"App vẫn tự thử lại mỗi vòng quét. Nếu kéo dài, kiểm tra mạng máy kho.\n" +
		"Có kết nối lại sẽ báo ✅ và bù các thay đổi."
}

// ---------------------------------------------------------------- cảnh báo không spam

// Mỗi loại lỗi chỉ nhắn tối đa 1 lần / alertEvery. Trước đây SESSION hết hạn
// lúc đêm sẽ nhắn mỗi 5 phút cho tới sáng.
const alertEvery = time.Hour

var (
	alertMu   sync.Mutex
	alertLast = map[string]time.Time{}
)

// alertDue: true nếu loại cảnh báo này chưa gửi trong alertEvery.
func alertDue(key string, now time.Time) bool {
	alertMu.Lock()
	defer alertMu.Unlock()
	if t, ok := alertLast[key]; ok && now.Sub(t) < alertEvery {
		return false
	}
	alertLast[key] = now
	return true
}

func alert(cfg Config, key, text string) {
	if !alertDue(key, time.Now()) {
		logf("[watch] Bỏ qua cảnh báo trùng (%s)", key)
		return
	}
	queueDelivery(cfg, loud(text))
}

// alertsClear: quét thành công → xoá trạng thái lỗi; trả về true nếu trước đó có lỗi
// (để báo "đã kết nối lại").
func alertsClear() bool {
	alertMu.Lock()
	defer alertMu.Unlock()
	had := false
	for k := range alertLast {
		if k != "panic" {
			had = true
		}
		delete(alertLast, k)
	}
	return had
}

// ---------------------------------------------------------------- so sánh 1 vòng quét

type etaChange struct {
	Trip Transport
	From time.Time
	To   time.Time
}

type pkgChange struct {
	It       Transport
	Old, New string
}

// scanDiff: việc cần báo sau một vòng quét.
type scanDiff struct {
	Arrived         []Transport // vừa vào kho
	NewTrips        []Transport // lần đầu thấy ở Sắp đến
	ETA             []etaChange
	Changes         []pkgChange       // số kiện đổi
	Late            []Transport       // quá giờ dự kiến > lateMin mà chưa vào kho
	LateMap         map[string]string // mã chuyến → link vị trí xe (điền trong pollLive)
	HistoryDeparted []departedHit
	Departed        []departedHit // biến khỏi Sắp đến, tìm thấy ở "Đã rời kho" (sót lúc trong kho)
	Gone            []Transport   // biến mất, không thấy ở đâu (huỷ / đổi kho / đổi lịch)
	Stuck           []Transport   // trong kho quá handoverMin chưa bàn giao xong
	StuckMin        int
}

// isLate: xe đã quá giờ dự kiến hơn lateMin phút.
func isLate(it Transport, now time.Time, lateMin int) bool {
	if lateMin <= 0 {
		return false
	}
	eta, ok := parseISO(it.ExpectedArrivalTime)
	return ok && now.Sub(eta) >= time.Duration(lateMin)*time.Minute
}

// diffScan: hàm thuần — cập nhật seen và trả về những gì cần báo.
// silent = lần quét gốc (chỉ ghi nhớ, không báo).
func diffScan(seen *SeenStore, arr, up []Transport, now time.Time, silent bool, lateMin int) scanDiff {
	var d scanDiff
	ms := now.UnixMilli()

	inWarehouse := map[string]bool{}
	for _, it := range arr {
		code := it.PartnerTransportationCode
		if code == "" {
			continue
		}
		inWarehouse[code] = true
		_, was := seen.Arrived[code]
		seen.Arrived[code] = ms
		delete(seen.Sig, code) // chốt: không báo số kiện nữa
		delete(seen.SigAt, code)
		delete(seen.Late, code)
		delete(seen.Last, code)
		delete(seen.Missing, code)
		if !was && !silent {
			d.Arrived = append(d.Arrived, it)
			seen.Day.Arrived[code] = totalPkg(it.WillDropOff)
		}
	}

	lateNow := 0
	for _, it := range up {
		code := it.PartnerTransportationCode
		if code == "" || inWarehouse[code] {
			continue
		}
		if _, done := seen.Arrived[code]; done {
			continue // đã báo vào kho rồi → không báo lại dù GHN còn để ở Sắp đến
		}
		sig := pkgSig(it.WillDropOff)
		old, seenBefore := seen.Sig[code]
		if prev, ok := seen.Last[code]; ok && !silent {
			from, hasFrom := parseISO(prev.ExpectedArrivalTime)
			to, hasTo := parseISO(it.ExpectedArrivalTime)
			if hasFrom && hasTo && (to.Sub(from) >= 15*time.Minute || from.Sub(to) >= 15*time.Minute ||
				to.In(warehouseZone).YearDay() != from.In(warehouseZone).YearDay()) {
				d.ETA = append(d.ETA, etaChange{Trip: it, From: from, To: to})
				delete(seen.Late, code)
			}
		}
		seen.SigAt[code] = ms
		seen.Last[code] = it
		delete(seen.Missing, code)
		quiet := silent || !seenBefore // xe mới: tin 🔵 đã ghi "trễ N phút" rồi
		switch {
		case !seenBefore:
			seen.Sig[code] = sig
			if !silent {
				d.NewTrips = append(d.NewTrips, it)
			}
		case old != sig:
			seen.Sig[code] = sig
			if !silent {
				d.Changes = append(d.Changes, pkgChange{It: it, Old: old, New: sig})
			}
		}
		if isLate(it, now, lateMin) {
			lateNow++
			if _, told := seen.Late[code]; !told {
				seen.Late[code] = ms
				if !quiet {
					d.Late = append(d.Late, it)
					seen.Day.Late++
				}
			}
		}
	}
	seen.Day.LastUp = len(up)
	seen.Day.LateNow = lateNow
	return d
}

// departedHit: chuyến sót lúc trong kho, đã thấy ở "Đã rời kho".
type departedHit struct {
	It  Transport
	Dep DepartedTrip
}

// vanishAfter: số lần quét liên tiếp không thấy chuyến thì mới coi là biến mất
// (tránh báo nhầm khi GHN trả thiếu một lần).
const vanishAfter = 2

// findVanished: hàm thuần — chuyến đang theo dõi (có chữ ký) mà vanishAfter lần quét
// liên tiếp không có ở cả Sắp đến lẫn Đã đến. Giữ ứng viên cho đến khi GHN
// xác nhận hoặc chuyến quá hạn, kể cả lúc tra danh sách Đã rời kho lỗi.
func findVanished(seen *SeenStore, arr, up []Transport, now time.Time) []Transport {
	present := map[string]bool{}
	for _, it := range arr {
		present[it.PartnerTransportationCode] = true
	}
	for _, it := range up {
		present[it.PartnerTransportationCode] = true
	}
	y, m, dd := now.In(warehouseZone).Date()
	var out []Transport
	for code := range seen.Sig {
		if present[code] {
			delete(seen.Missing, code)
			continue
		}
		last, ok := seen.Last[code]
		if !ok {
			continue // dữ liệu bản cũ chưa có thông tin chuyến → để cleanSeen tự dọn
		}
		if eta, ok := parseISO(last.ExpectedArrivalTime); ok {
			ey, em, ed := eta.In(warehouseZone).Date()
			if ey != y || em != m || ed != dd {
				// Giữ xe qua giao ngày để tra lại tab Đã rời kho.
				if !eta.Before(now) || now.Sub(eta) > 24*time.Hour {
					continue
				}
			}
		}
		if count := seen.Missing[code]; count >= 0 && count < vanishAfter {
			seen.Missing[code]++
		}
		if seen.Missing[code] == vanishAfter || seen.Missing[code] < 0 {
			out = append(out, last)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PartnerTransportationCode < out[j].PartnerTransportationCode })
	return out
}

// departedMsg: xe đã vào rồi rời kho giữa 2 lần quét — vẫn là tin 🟠 (có chuông).
func departedMsg(h departedHit) string {
	it := h.It
	msg := "🟠 <b>XE ĐÃ ĐẾN KHO</b>\n" +
		"<i>(xe vào rồi rời kho nhanh giữa 2 lần quét)</i>\n" +
		tripHead(it) + "\n"
	if t := string(h.Dep.ArrivalTime); t != "" {
		msg += "🕐 Đến kho lúc: <b>" + fmtTime(t) + "</b>\n"
	}
	if t := string(h.Dep.DepartureTime); t != "" {
		msg += "🚪 Rời kho lúc: " + fmtTime(t) + "\n"
	}
	return msg + driverLine(it) + "\n" + cargoLine(it)
}

// goneMsg: chuyến biến mất, không thấy ở Đã đến / Đã rời kho — tin im lặng.
func goneMsg(list []Transport) string {
	var b strings.Builder
	if len(list) == 1 {
		it := list[0]
		b.WriteString("❔ <b>CHUYẾN KHÔNG CÒN Ở \"SẮP ĐẾN KHO\"</b>\n")
		b.WriteString(tripHead(it) + "\n")
		b.WriteString("🕐 Dự kiến đến: " + fmtTime(it.ExpectedArrivalTime) + "\n")
		b.WriteString(fmt.Sprintf("📦 Cần nhận: %d kiện\n", totalPkg(it.WillDropOff)))
	} else {
		b.WriteString(fmt.Sprintf("❔ <b>%d CHUYẾN KHÔNG CÒN Ở \"SẮP ĐẾN KHO\"</b>\n", len(list)))
		for i, it := range list {
			if i >= 10 {
				b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(list)-i))
				break
			}
			b.WriteString(listItem(it, "Dự kiến đến "+fmtTimeShort(it.ExpectedArrivalTime)))
		}
	}
	b.WriteString("Không thấy ở \"Đã đến kho\" lẫn \"Đã rời kho\" — có thể bị huỷ, đổi kho hoặc đổi lịch. Kiểm tra trên web nếu cần.")
	return b.String()
}

// arrivedGroupMin: từ ngần này xe vào kho cùng vòng thì gộp 1 tin
// (vd. sáng ra bù các xe vào trong đêm). Ít hơn thì vẫn báo chi tiết từng xe.
const arrivedGroupMin = 4

// diffMessages: dựng tin từ kết quả so sánh (gộp khi nhiều chuyến).
// Chuông: 🟠 xe vào kho, 🔵 xe mới, ⏰ xe trễ. Im lặng: 🔄 đổi số kiện.
func diffMessages(d scanDiff, now time.Time) []outMsg {
	var out []outMsg
	if len(d.Arrived) >= arrivedGroupMin {
		for i := 0; i < len(d.Arrived); i += 15 {
			out = append(out, loud(arrivedListMsg(d.Arrived[i:min(i+15, len(d.Arrived))])))
		}
	} else {
		for _, it := range d.Arrived {
			out = append(out, loud(arrivedMsg(it)))
		}
	}
	for _, h := range d.Departed {
		out = append(out, loud(departedMsg(h)))
	}
	switch len(d.NewTrips) {
	case 0:
	case 1:
		out = append(out, loud(firstMsgAt(d.NewTrips[0], now)))
	default:
		for i := 0; i < len(d.NewTrips); i += 8 {
			out = append(out, loud(newTripsMsgAt(d.NewTrips[i:min(i+8, len(d.NewTrips))], now)))
		}
	}
	switch len(d.Changes) {
	case 0:
	case 1:
		c := d.Changes[0]
		out = append(out, quiet(updateMsg(c.It, c.Old, c.New)))
	default:
		for i := 0; i < len(d.Changes); i += 10 {
			out = append(out, quiet(updatesMsg(d.Changes[i:min(i+10, len(d.Changes))])))
		}
	}
	for i := 0; i < len(d.ETA); i += 12 {
		var b strings.Builder
		b.WriteString("🕐 <b>ĐỔI GIỜ DỰ KIẾN ĐẾN</b>")
		for _, change := range d.ETA[i:min(i+12, len(d.ETA))] {
			fmt.Fprintf(&b, "\n• %s · %s → %s", esc(change.Trip.PartnerTransportationCode),
				change.From.In(warehouseZone).Format("02/01 15:04"), change.To.In(warehouseZone).Format("02/01 15:04"))
		}
		out = append(out, quiet(b.String()))
	}
	switch len(d.Late) {
	case 0:
	case 1:
		out = append(out, loud(lateMsg(d.Late[0], now, d.LateMap[d.Late[0].PartnerTransportationCode])))
	default:
		for i := 0; i < len(d.Late); i += 10 {
			out = append(out, loud(lateListMsg(d.Late[i:min(i+10, len(d.Late))], now, d.LateMap)))
		}
	}
	switch len(d.Stuck) {
	case 0:
	case 1:
		out = append(out, loud(stuckMsg(d.Stuck[0], now, d.StuckMin)))
	default:
		for i := 0; i < len(d.Stuck); i += 10 {
			out = append(out, loud(stuckListMsg(d.Stuck[i:min(i+10, len(d.Stuck))], now, d.StuckMin)))
		}
	}
	for i := 0; i < len(d.Gone); i += 10 {
		out = append(out, quiet(goneMsg(d.Gone[i:min(i+10, len(d.Gone))])))
	}
	return out
}

// nextSleep: có xe dự kiến tới trong 30 phút tới (hoặc trễ dưới 1 giờ)
// thì quét nhanh để báo 🟠 sớm; còn lại quét theo liveMin.
func nextSleep(cfg Config, up []Transport, now time.Time) time.Duration {
	normal := time.Duration(cfg.liveMin()) * time.Minute
	fast := cfg.fastMin()
	if fast <= 0 {
		return normal
	}
	for _, it := range up {
		eta, ok := parseISO(it.ExpectedArrivalTime)
		if !ok {
			continue
		}
		if d := eta.Sub(now); d <= 30*time.Minute && d >= -time.Hour {
			return time.Duration(fast) * time.Minute
		}
	}
	return normal
}

// pollLive: một vòng quét. Trả về các tin cần gửi (chưa gửi) — watch() quyết
// định thứ tự và tin mở đầu. ok=false khi không lấy được dữ liệu GHN.
func pollLive(cfg *Config, seen *SeenStore, silent bool) (arr, up []Transport, msgs []outMsg, ok bool) {
	if err := ensureSession(cfg); err != nil {
		recordScanHealth(scanSession)
		lastErr = err.Error()
		logf("[watch] SESSION: %v", err)
		alert(*cfg, "session", "⚠️ <b>Không lấy được SESSION GHN</b>\n"+esc(err.Error())+"\nMở app → mục 7 để lấy lại.")
		return nil, nil, nil, false
	}
	now := time.Now()

	var timing scanTiming
	started := time.Now()
	arrResp, err := fetchArrived(*cfg)
	if err != nil && isAuthErr(err) && cfg.autoSessionOn() {
		// GHN từ chối SESSION → lấy SESSION mới từ Chrome rồi thử lại ngay
		old := cfg.Session
		if bErr := tryBrowserSession(cfg, true); bErr == nil && cfg.Session != old {
			logf("[watch] SESSION bị từ chối → đã lấy SESSION mới từ trình duyệt, thử lại")
			arrResp, err = fetchArrived(*cfg)
		} else if bErr != nil {
			logf("[watch] SESSION bị từ chối, lấy từ trình duyệt lỗi: %v", bErr)
		}
	}
	timing.arrived = time.Since(started)
	if err != nil {
		if isAuthErr(err) {
			observeScan(scanSession, arrResp, nil, seen, timing)
		} else {
			observeScan(scanFailed, arrResp, nil, seen, timing)
		}
		lastErr = err.Error()
		logf("[watch] Lỗi đã đến: %v", err)
		if isAuthErr(err) {
			alert(*cfg, "auth", "⚠️ <b>SESSION GHN hết hạn</b>\n"+
				"App đã thử lấy SESSION mới từ Chrome nhưng không được.\n"+
				"Trên máy kho: mở Chrome → vào nhanh.ghn.vn → đăng nhập lại. App tự lấy ở lần quét sau.")
		} else if mins, due := netFailed(now); due {
			// lỗi mạng: chỉ báo khi kéo dài netAlertAfter lần quét liên tiếp — lỗi chớp nhoáng tự hết thì im
			alert(*cfg, "ghn", netAlertMsg(err, mins))
		}
		return nil, nil, nil, false
	}
	arr = arrResp.list()

	upOK := true
	var upResp *ArrivedResp
	if cfg.upcomingOn() {
		started = time.Now()
		upResp, err = fetchUpcoming(*cfg)
		timing.upcoming = time.Since(started)
		if err != nil {
			upOK = false
			lastErr = err.Error()
			logf("[watch] Lỗi sắp đến: %v", err)
		} else {
			up = upResp.list()
		}
	}

	if silent && !upOK {
		observeScan(scanPartial, arrResp, upResp, seen, timing)
		return arr, up, nil, false
	}
	if !seen.Day.Pending {
		seen.Day.Pending = true
		seen.Day.From = now.UnixMilli()
	}
	lastUp, lateNow := seen.Day.LastUp, seen.Day.LateNow
	d := diffScan(seen, arr, up, now, silent, cfg.lateMin())
	if !upOK {
		seen.Day.LastUp, seen.Day.LateNow = lastUp, lateNow
	}
	var gone []Transport
	if !silent && upOK && cfg.upcomingOn() {
		gone = findVanished(seen, arr, up, now)
	}
	known := pendingHistoryDepartures(seen, arr, now)
	if len(gone) > 0 || len(known) > 0 {
		started = time.Now()
		dep, err := fetchDeparted(*cfg)
		timing.departed = time.Since(started)
		if err != nil {
			logf("[watch] Lỗi tra Đã rời kho: %v", err)
		}
		if len(known) > 0 {
			seen.HistoryCheck = now.Unix()
			for _, trip := range known {
				if confirmed, ok := dep[trip.PartnerTransportationCode]; ok {
					d.HistoryDeparted = append(d.HistoryDeparted, departedHit{It: trip, Dep: confirmed})
				}
			}
		}
		for _, it := range gone {
			code := it.PartnerTransportationCode
			if t, ok := dep[code]; ok {
				seen.Arrived[code] = now.UnixMilli()
				delete(seen.Sig, code)
				delete(seen.SigAt, code)
				delete(seen.Late, code)
				delete(seen.Last, code)
				delete(seen.Missing, code)
				hit := departedHit{It: it, Dep: t}
				seen.Departed[code] = hit
				d.Departed = append(d.Departed, hit)
				seen.Day.Arrived[code] = totalPkg(it.WillDropOff)
				// đã rời kho = bàn giao xong; thời gian = đến → rời (nếu GHN có)
				mins := -1
				a, okA := parseISO(string(t.ArrivalTime))
				dp, okD := parseISO(string(t.DepartureTime))
				if okA && okD && !dp.Before(a) {
					mins = int(dp.Sub(a).Minutes())
				}
				seen.Day.Done[code] = mins
			} else if err == nil && seen.Missing[code] == vanishAfter {
				seen.Missing[code] = -1 // đã báo chưa thấy, vẫn chờ xác nhận
				d.Gone = append(d.Gone, it)
			}
		}
		if len(gone) > 0 {
			logf("[watch] Chuyến biến mất: %d (thấy ở Đã rời kho: %d)", len(gone), len(d.Departed))
		}
	}
	d.Stuck = trackHandover(seen, arr, now, silent, cfg.handoverMin())
	d.StuckMin = cfg.handoverMin()
	recordTripScan(seen, *cfg, arr, up, d, now)
	cleanSeen(seen)
	if upOK {
		observeScan(scanHealthy, arrResp, upResp, seen, timing)
	} else {
		observeScan(scanPartial, arrResp, upResp, seen, timing)
	}

	if upOK {
		lastErr = ""
	}
	if len(d.Late) > 0 {
		d.LateMap = map[string]string{}
		for i, it := range d.Late {
			if i >= 5 {
				break // tối đa 5 lệnh gọi / vòng
			}
			if u := fetchVehicleMap(*cfg, it.VehiclePlate); u != "" {
				d.LateMap[it.PartnerTransportationCode] = u
			}
		}
	}
	msgs = diffMessages(d, now)
	if !upOK {
		return arr, up, msgs, true
	}
	down := netRecovered(now)
	if alertsClear() && !silent {
		txt := "✅ <b>Đã kết nối lại GHN</b>"
		if down > 0 {
			txt += " sau " + durVi(down)
		}
		msgs = append([]outMsg{quiet(txt + " — tiếp tục theo dõi, đã bù các thay đổi.")}, msgs...)
	}
	return arr, up, msgs, true
}

// pkgSig: chữ ký số kiện để phát hiện thay đổi.
func pkgSig(w WillDropOff) string {
	return fmt.Sprintf("%d %d %d %d", w.NumOfPkg, w.NumOfPkgBulky, w.NumOfPkgFwd, w.NumOfPkgFragile)
}

func sigToPkg(sig string) WillDropOff {
	var w WillDropOff
	fmt.Sscanf(sig, "%d %d %d %d", &w.NumOfPkg, &w.NumOfPkgBulky, &w.NumOfPkgFwd, &w.NumOfPkgFragile)
	return w
}

// ---------------------------------------------------------------- tin nhắn

func tripHead(it Transport) string {
	line := "🚚 Chuyến: <b>" + esc(it.PartnerTransportationCode) + "</b>"
	t := typeOpsVi(it.TypeOps)
	switch {
	case !blank(it.RouteScheduleName):
		line += "\n🛣 Tuyến: " + esc(it.RouteScheduleName)
		if t != "-" {
			line += " (" + esc(t) + ")"
		}
	case t != "-":
		line += "\n🛣 Loại chuyến: " + esc(t)
	}
	return line
}

// blank: GHN để trống hoặc "-" khi chưa gán tài xế / xe.
func blank(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || s == "-" || s == "--"
}

// driverLine: tài xế + xe; chưa gán thì ghi rõ thay vì in "-".
func driverLine(it Transport) string {
	noDriver, noCar := blank(it.DriverName), blank(it.VehiclePlate)
	if noDriver && noCar && blank(it.DriverPhone) {
		return "👤 Tài xế, xe: chưa gán"
	}
	var lines []string
	if noDriver {
		lines = append(lines, "👤 Tài xế: chưa gán")
	} else {
		lines = append(lines, "👤 Tài xế: "+esc(strings.TrimSpace(it.DriverName)))
	}
	if !blank(it.DriverPhone) {
		lines = append(lines, "📞 SĐT: "+esc(strings.TrimSpace(it.DriverPhone)))
	}
	if noCar {
		lines = append(lines, "🚛 Xe: chưa gán")
	} else {
		v := "🚛 Xe: " + esc(strings.TrimSpace(it.VehiclePlate))
		if kg := kgStr(it.Capacity); kg != "" {
			v += " (" + kg + ")"
		}
		lines = append(lines, v)
	}
	return strings.Join(lines, "\n")
}

// kgStr: tải trọng "1.900 kg"; trống / 0 / "-" → "".
func kgStr(v interface{}) string {
	c := strings.TrimSpace(capStr(v))
	if blank(c) || c == "0" {
		return ""
	}
	n, err := strconv.Atoi(c)
	if err != nil {
		return esc(c) + " kg"
	}
	return thousands(n) + " kg"
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

// pkgDetail: "cồng kềnh 1 · cần rã 8 · chuyển tiếp 5" — đúng 3 nhóm như web GHN.
func pkgDetail(w WillDropOff) string {
	var p []string
	if w.NumOfPkg > 0 {
		p = append(p, fmt.Sprintf("  • Bao cần rã: %d", w.NumOfPkg))
	}
	if w.NumOfPkgBulky > 0 {
		p = append(p, fmt.Sprintf("  • Kiện cồng kềnh: %d", w.NumOfPkgBulky))
	}
	if w.NumOfPkgFwd > 0 {
		p = append(p, fmt.Sprintf("  • Bao chuyển tiếp: %d", w.NumOfPkgFwd))
	}
	return strings.Join(p, "\n")
}

func cargoLine(it Transport) string {
	n := totalPkg(it.WillDropOff)
	if n == 0 {
		return "📦 Cần nhận: chưa có kiện" + extraLines(it)
	}
	line := fmt.Sprintf("📦 Cần nhận: <b>%d</b> kiện", n)
	if d := pkgDetail(it.WillDropOff); d != "" {
		line += "\n" + d
	}
	return line + extraLines(it)
}

// extraLines: dễ vỡ (GHN đếm riêng, không cộng vào tổng) + nhóm hàng.
func extraLines(it Transport) string {
	out := ""
	if n := it.WillDropOff.NumOfPkgFragile; n > 0 {
		out += fmt.Sprintf("\n⚠️ <b>Có %d kiện dễ vỡ</b>", n)
	}
	if g := groupLine(it); g != "" {
		out += "\n" + g
	}
	return out
}

// groupLine: nhóm hàng của chuyến, như cột "Nhóm hàng" trên web.
func groupLine(it Transport) string {
	clean := func(in []string) []string {
		var out []string
		for _, x := range in {
			if !blank(x) {
				out = append(out, esc(strings.TrimSpace(x)))
			}
		}
		return out
	}
	if ex := clean(it.ExcludeSortGroupCodes); len(ex) > 0 {
		return "🏷 Ngoại trừ nhóm: " + strings.Join(ex, ", ")
	}
	if in := clean(it.IncludeSortGroupCodes); len(in) > 0 {
		return "🏷 Thuộc nhóm: " + strings.Join(in, ", ")
	}
	return ""
}

// etaLeft: "còn 40 phút" / "còn 1 giờ 5 phút" / "trễ 15 phút" so với giờ dự kiến.
func etaLeft(iso string, now time.Time) string {
	eta, ok := parseISO(iso)
	if !ok {
		return ""
	}
	diff := int(eta.Sub(now).Round(time.Minute).Minutes())
	if diff == 0 {
		return "sắp tới nơi"
	}
	abs := diff
	if abs < 0 {
		abs = -abs
	}
	txt := fmt.Sprintf("%d phút", abs%60)
	if h := abs / 60; h > 0 {
		txt = fmt.Sprintf("%d giờ", h)
		if abs%60 > 0 {
			txt += fmt.Sprintf(" %d phút", abs%60)
		}
	}
	if diff > 0 {
		return "còn " + txt
	}
	return "trễ " + txt
}

// etaShort: dạng gọn cho danh sách: "40'", "1h05", "trễ 15'".
func etaShort(iso string, now time.Time) string {
	eta, ok := parseISO(iso)
	if !ok {
		return ""
	}
	diff := int(eta.Sub(now).Round(time.Minute).Minutes())
	abs := diff
	if abs < 0 {
		abs = -abs
	}
	txt := fmt.Sprintf("%d'", abs)
	if abs >= 60 {
		txt = fmt.Sprintf("%dh%02d", abs/60, abs%60)
	}
	if diff < 0 {
		return "trễ " + txt
	}
	return txt
}

// firstMsg: lần đầu thấy chuyến trong "Sắp đến kho" (kể cả 0 kiện).
func firstMsg(it Transport) string { return firstMsgAt(it, time.Now()) }

func firstMsgAt(it Transport, now time.Time) string {
	return "🔵 <b>XE SẮP ĐẾN KHO</b>\n" +
		tripHead(it) + "\n" +
		etaBlock(it, now) + "\n" +
		driverLine(it) + "\n" +
		cargoLine(it)
}

// etaBlock: "🕐 Dự kiến đến: …" + dòng "⏳ Còn … / Đã trễ …".
func etaBlock(it Transport, now time.Time) string {
	out := "🕐 Dự kiến đến: " + fmtTime(it.ExpectedArrivalTime)
	if left := etaLeft(it.ExpectedArrivalTime, now); left != "" {
		switch {
		case strings.HasPrefix(left, "trễ "):
			out += "\n⏳ Đã trễ <b>" + strings.TrimPrefix(left, "trễ ") + "</b> so với dự kiến"
		case strings.HasPrefix(left, "còn "):
			out += "\n⏳ Còn <b>" + strings.TrimPrefix(left, "còn ") + "</b> nữa"
		default:
			out += "\n⏳ Sắp tới nơi"
		}
	}
	return out
}

// listItem: một xe trong tin gộp — 2 dòng: mã + tuyến, rồi chi tiết.
func listItem(it Transport, detail string) string {
	line := "• <b>" + esc(it.PartnerTransportationCode) + "</b>"
	if !blank(it.RouteScheduleName) {
		line += " — " + esc(it.RouteScheduleName)
	}
	return line + "\n   " + detail + "\n"
}

// newTripsMsg: nhiều chuyến cùng xuất hiện trong một vòng quét → một tin.
func newTripsMsg(list []Transport) string { return newTripsMsgAt(list, time.Now()) }

func newTripsMsgAt(list []Transport, now time.Time) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔵 <b>%d XE MỚI SẮP ĐẾN KHO</b>\n", len(list)))
	for i, it := range list {
		if i >= 8 {
			b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(list)-i))
			break
		}
		d := "Dự kiến đến " + fmtTimeShort(it.ExpectedArrivalTime)
		if l := etaLeft(it.ExpectedArrivalTime, now); l != "" {
			d += " (" + l + ")"
		}
		d += " · " + strconv.Itoa(totalPkg(it.WillDropOff)) + " kiện"
		b.WriteString(listItem(it, d))
	}
	return strings.TrimRight(b.String(), "\n")
}

// updatesMsg: nhiều chuyến đổi số kiện trong cùng vòng quét → một tin.
func updatesMsg(list []pkgChange) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔄 <b>CẬP NHẬT SỐ KIỆN — %d CHUYẾN</b>\n", len(list)))
	for i, c := range list {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(list)-i))
			break
		}
		o, n := totalPkg(sigToPkg(c.Old)), totalPkg(sigToPkg(c.New))
		d := fmt.Sprintf("Cần nhận: %d → <b>%d</b> kiện", o, n)
		if diff := n - o; diff > 0 {
			d += fmt.Sprintf(" (+%d)", diff)
		} else if diff < 0 {
			d += fmt.Sprintf(" (%d)", diff)
		}
		b.WriteString(listItem(c.It, d))
	}
	return strings.TrimRight(b.String(), "\n")
}

// updateMsg: số kiện thay đổi (báo mỗi lần đổi, tới khi xe vào kho).
func updateMsg(it Transport, oldSig, newSig string) string {
	o, nw := sigToPkg(oldSig), sigToPkg(newSig)
	line := func(label string, a, b int) string {
		if a == b {
			return ""
		}
		return fmt.Sprintf("  • %s: %d → <b>%d</b>\n", label, a, b)
	}
	body := line("Bao cần rã", o.NumOfPkg, nw.NumOfPkg) +
		line("Kiện cồng kềnh", o.NumOfPkgBulky, nw.NumOfPkgBulky) +
		line("Bao chuyển tiếp", o.NumOfPkgFwd, nw.NumOfPkgFwd) +
		line("Kiện dễ vỡ", o.NumOfPkgFragile, nw.NumOfPkgFragile)
	if body == "" {
		body = "  • Số kiện không đổi\n"
	}
	return "🔄 <b>CẬP NHẬT SỐ KIỆN</b>\n" +
		tripHead(it) + "\n" +
		"🕐 Dự kiến đến: " + fmtTime(it.ExpectedArrivalTime) + "\n" +
		driverLine(it) + "\n" +
		"📦 Thay đổi:\n" + body +
		fmt.Sprintf("📦 Tổng cần nhận: %d → <b>%d</b> kiện", totalPkg(o), totalPkg(nw))
}

// arrivedMsg: xe đã vào kho — chốt, không báo nữa.
func arrivedMsg(it Transport) string {
	msg := "🟠 <b>XE ĐÃ ĐẾN KHO</b>\n" +
		tripHead(it) + "\n" +
		"🕐 Đến kho lúc: <b>" + fmtTime(it.ArrivalTime) + "</b>"
	if lt := lateText(it); lt != "" {
		msg += "\n⏱ " + esc(lt) + " so với dự kiến (" + fmtTimeShort(it.ExpectedArrivalTime) + ")"
	}
	if st := statusVi(it.StopSessionStatus); st != "-" {
		msg += "\n📋 Bàn giao: " + esc(st)
	}
	return msg + "\n" + driverLine(it) + "\n" + cargoLine(it)
}

// startMsg: tin duy nhất khi mới bật theo dõi (chưa có dữ liệu cũ).
func startMsg(cfg Config, arr, up []Transport) string {
	msg := "👀 <b>BẮT ĐẦU THEO DÕI</b>\n" +
		"🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n" +
		fmt.Sprintf("🟠 Trong kho: <b>%d</b> • 🔵 Sắp đến: <b>%d</b>\n", len(arr), len(up))
	msg += "Mỗi chuyến: báo lần đầu khi xuất hiện → báo khi số kiện đổi → dừng khi xe vào kho."
	return msg
}

// resumeMsg: bật lại khi còn dữ liệu cũ → các tin bù đi ngay sau tin này.
func resumeMsg(cfg Config, last time.Time, n int, arr, up []Transport) string {
	msg := "▶️ <b>TIẾP TỤC THEO DÕI</b>\n" +
		"🏭 Kho: <code>" + esc(cfg.LocationID) + "</code> • quét lần cuối " + last.Format("02/01 15:04") + "\n" +
		fmt.Sprintf("🟠 Trong kho: <b>%d</b> • 🔵 Sắp đến: <b>%d</b>\n", len(arr), len(up))
	if n > 0 {
		msg += fmt.Sprintf("Bù %d tin thay đổi trong lúc tắt ↓", n)
	} else {
		msg += "Không có thay đổi trong lúc tắt."
	}
	return msg
}

// arrivedListMsg: nhiều xe vào kho cùng vòng (thường là sáng ra, bù đêm) → 1 tin.
func arrivedListMsg(list []Transport) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🟠 <b>%d XE ĐÃ ĐẾN KHO</b>\n", len(list)))
	total := 0
	for i, it := range list {
		total += totalPkg(it.WillDropOff)
		if i >= 15 {
			continue
		}
		b.WriteString(listItem(it, "Đến kho lúc "+fmtTimeShort(it.ArrivalTime)+" · "+strconv.Itoa(totalPkg(it.WillDropOff))+" kiện"))
	}
	if len(list) > 15 {
		b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(list)-15))
	}
	b.WriteString(fmt.Sprintf("📦 Tổng cần nhận: <b>%d</b> kiện", total))
	return b.String()
}

// lateBy: "45 phút" / "1 giờ 10 phút" xe đã trễ.
func lateBy(it Transport, now time.Time) string {
	l := etaLeft(it.ExpectedArrivalTime, now)
	return strings.TrimPrefix(l, "trễ ")
}

// lateMsg: xe quá giờ dự kiến mà chưa vào kho — báo 1 lần, kèm SĐT tài xế để gọi
// và link vị trí xe hiện tại (nếu GHN có toạ độ).
func lateMsg(it Transport, now time.Time, mapURL string) string {
	msg := "⏰ <b>XE TRỄ " + strings.ToUpper(esc(lateBy(it, now))) + " — CHƯA VÀO KHO</b>\n" +
		tripHead(it) + "\n" +
		"🕐 Dự kiến đến: " + fmtTime(it.ExpectedArrivalTime) + "\n" +
		driverLine(it) + "\n" +
		cargoLine(it)
	if mapURL != "" {
		msg += "\n📍 <a href=\"" + esc(mapURL) + "\">Xem xe đang ở đâu</a>"
	}
	return msg
}

func lateListMsg(list []Transport, now time.Time, maps map[string]string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("⏰ <b>%d XE TRỄ — CHƯA VÀO KHO</b>\n", len(list)))
	for i, it := range list {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(list)-i))
			break
		}
		d := "Dự kiến đến " + fmtTimeShort(it.ExpectedArrivalTime) + ", đã trễ " + esc(lateBy(it, now))
		if !blank(it.DriverPhone) {
			d += "\n   📞 SĐT: " + esc(strings.TrimSpace(it.DriverPhone))
		}
		if u := maps[it.PartnerTransportationCode]; u != "" {
			d += " · <a href=\"" + esc(u) + "\">📍 vị trí xe</a>"
		}
		b.WriteString(listItem(it, d))
	}
	return strings.TrimRight(b.String(), "\n")
}

// summaryMsg: 📊 tổng kết khi hết khung giờ. Cũng là tín hiệu "app còn sống":
// hôm nào không thấy tin này là app đã chết.
func summaryMsg(cfg Config, d DayStats, now time.Time) string {
	pkg := 0
	for _, n := range d.Arrived {
		pkg += n
	}
	from, to := "-", now.Format("15:04")
	if d.From > 0 {
		f := time.UnixMilli(d.From)
		from = f.Format("02/01 15:04")
		if f.YearDay() != now.YearDay() {
			to = now.Format("02/01 15:04")
		}
	}
	msg := "📊 <b>TỔNG KẾT</b> " + from + " → " + to + "\n" +
		"🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n" +
		fmt.Sprintf("🟠 Xe vào kho: <b>%d</b> • 📦 <b>%d</b> kiện\n", len(d.Arrived), pkg)
	if h := handoverSummary(d); h != "" {
		msg += h + "\n"
	}
	if cfg.lateMin() > 0 {
		msg += fmt.Sprintf("⏰ Đã báo trễ: <b>%d</b> xe\n", d.Late)
	}
	msg += fmt.Sprintf("🔵 Còn ở Sắp đến: <b>%d</b>", d.LastUp)
	if d.LateNow > 0 {
		msg += fmt.Sprintf(" (trễ: %d)", d.LateNow)
	}
	msg += "\n✅ App vẫn chạy"
	if !fullDay(cfg) {
		msg += " — quét lại lúc " + cfg.watchFrom()
	}
	return msg
}

// fullDay: khung theo dõi cả ngày (không có lúc "hết khung").
func fullDay(cfg Config) bool {
	from, to := hhmmToMin(cfg.watchFrom()), hhmmToMin(cfg.watchTo())
	if from < 0 || to < 0 || from == to {
		return true
	}
	span := to - from
	if span < 0 {
		span += 24 * 60
	}
	return span >= 24*60-30 // vd. 00:00–23:59
}

// summaryDue: lúc gửi 📊. Bình thường = vừa ra khỏi khung giờ.
// Khung cả ngày thì gửi khi qua ngày mới; phòng hờ: phiên kéo dài quá 20 giờ.
func summaryDue(cfg Config, d DayStats, now time.Time, inWindow bool) bool {
	if !d.Pending || d.From == 0 {
		return false
	}
	from := time.UnixMilli(d.From)
	if !inWindow && !fullDay(cfg) {
		return true
	}
	if fullDay(cfg) && from.YearDay() != now.YearDay() {
		return true
	}
	return now.Sub(from) > 20*time.Hour
}

// shiftMsg: tóm tắt đầu ca — lặp lại mỗi ca.
func shiftMsg(cfg Config, name string, arr, up []Transport) string {
	var b strings.Builder
	b.WriteString("🕐 <b>BẮT ĐẦU CA " + esc(name) + "</b>\n")
	b.WriteString("🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n")
	b.WriteString(fmt.Sprintf("🟠 Đang trong kho: <b>%d</b> xe\n", len(arr)))
	b.WriteString(fmt.Sprintf("🔵 Sắp đến kho: <b>%d</b> xe\n", len(up)))
	for i, it := range up {
		if i >= 6 {
			b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(up)-i))
			break
		}
		b.WriteString(listItem(it, "Dự kiến đến "+fmtTimeShort(it.ExpectedArrivalTime)+" · "+strconv.Itoa(totalPkg(it.WillDropOff))+" kiện"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------------------------------------------------------------- gửi tin

// outMsg: một tin cần gửi. Quiet = tin phụ (🔄 🕐 📊 ▶️ ✅): vẫn hiện trong nhóm
// nhưng không rung/chuông, để điện thoại chỉ kêu khi xe đến / xe trễ / có lỗi.
type outMsg struct {
	Text    string
	Quiet   bool
	drained chan struct{}
}

func loud(t string) outMsg  { return outMsg{Text: t} }
func quiet(t string) outMsg { return outMsg{Text: t, Quiet: true} }

var liveQueue = make(chan outMsg)
var liveOnce sync.Once
var deliveryReady = make(chan struct{}, 1)

func wakeDelivery() {
	select {
	case deliveryReady <- struct{}{}:
	default:
	}
}

func startLiveQueue() {
	liveOnce.Do(func() {
		go func() {
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for {
				select {
				case m := <-liveQueue:
					if m.drained != nil {
						close(m.drained)
						continue
					}
				case <-deliveryReady:
					if retryOutbox(loadCfg(), time.Now()) {
						time.Sleep(tgGap)
						wakeDelivery()
					}
				case <-tick.C:
					wakeDelivery()
				}
			}
		}()
	})
}

func enqueue(m outMsg) {
	queueDelivery(loadCfg(), m)
}

func queueDelivery(cfg Config, m outMsg) {
	stageDelivery(cfg, m, time.Now())
	wakeDelivery()
}

// ---------------------------------------------------------------- tiến trình nền

func watchLockPath() string { return filepath.Join(exeDir, "watch.lock") }

type watchLock struct {
	PID     int    `json:"pid"`
	Since   string `json:"since"`
	Account string `json:"account,omitempty"`
	Start   int64  `json:"start,omitempty"` // giờ tạo tiến trình (chống PID bị cấp lại)
}

func readWatchLock() (watchLock, bool) {
	var l watchLock
	b, err := os.ReadFile(watchLockPath())
	if err != nil || json.Unmarshal(b, &l) != nil || l.PID <= 0 {
		return watchLock{}, false
	}
	return l, true
}

func acquireWatchLock() (func(), error) {
	if running, pid, since := watchRunning(); running {
		return nil, fmt.Errorf("đã có tiến trình theo dõi (PID %d, từ %s)", pid, since)
	}
	me := watchLock{PID: os.Getpid(), Since: time.Now().Format("02/01 15:04:05"), Account: updateAccount()}
	if st, ok := procStartTime(me.PID); ok {
		me.Start = st
	}
	b, _ := json.Marshal(me)
	if err := os.WriteFile(watchLockPath(), b, 0600); err != nil {
		logf("[watch] Không tạo được watch.lock (%v) — chạy không khoá", err)
		return func() {}, nil
	}
	return func() {
		// chỉ xoá nếu lock vẫn là của mình
		if l, ok := readWatchLock(); ok && l.PID == me.PID {
			_ = os.Remove(watchLockPath())
		}
	}, nil
}

// lockAlive: PID trong lock còn sống VÀ đúng là tiến trình đã ghi lock.
// Windows hay cấp lại PID cho chương trình khác; chỉ dựa vào PID thì menu
// báo nhầm ĐANG CHẠY và lệnh dừng có thể giết nhầm chương trình đó.
func lockAlive(l watchLock) bool {
	if !pidAlive(l.PID) {
		return false
	}
	if l.Start == 0 {
		return true // lock bản cũ (≤2.3.0) chưa có giờ tạo
	}
	cur, ok := procStartTime(l.PID)
	if !ok {
		return true // không đọc được (quyền) → tin PID
	}
	return cur == l.Start
}

func watchRunning() (bool, int, string) {
	l, ok := readWatchLock()
	if !ok || l.PID == os.Getpid() {
		return false, 0, ""
	}
	if !lockAlive(l) {
		return false, 0, ""
	}
	return true, l.PID, l.Since
}

func stopWatch(pid int) {
	if pid <= 0 {
		return
	}
	// kiểm lại ngay trước khi giết: đúng tiến trình theo dõi mới giết
	if l, ok := readWatchLock(); !ok || l.PID != pid || !lockAlive(l) {
		logf("[stop] PID %d không còn là tiến trình theo dõi — không giết", pid)
		_ = os.Remove(watchLockPath())
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
	time.Sleep(800 * time.Millisecond)
	if !pidAlive(pid) {
		_ = os.Remove(watchLockPath())
	}
}

// startHiddenWatch: chạy --watch ẩn, tách khỏi cửa sổ hiện tại.
func startHiddenWatch() error {
	if updateInProgress() {
		return fmt.Errorf("đang cài bản cập nhật, hãy đợi cài xong")
	}
	clearStopIntent() // bật bằng tay → tự bật lại hoạt động trở lại
	exe := exePath()
	if runtime.GOOS != "windows" {
		cmd := exec.Command(exe, "--watch")
		detach(cmd)
		return cmd.Start()
	}
	content := "CreateObject(\"Wscript.Shell\").Run \"\"\"" + exe + "\"\" --watch\", 0, False\r\n"
	if err := os.WriteFile(watchVbsPath(), []byte(content), 0644); err != nil {
		return err
	}
	return exec.Command("wscript.exe", watchVbsPath()).Start()
}

func watchVbsPath() string { return filepath.Join(exeDir, "run-watch.vbs") }

func menuLive() {
	cfg := loadCfg()
	if running, pid, since := watchRunning(); running {
		fmt.Printf("🟢 Đang theo dõi — PID %d (từ %s).\n", pid, since)
		if t := lastScanTime(); !t.IsZero() {
			fmt.Printf("   Lần quét cuối: %s\n", t.Format("02/01 15:04:05"))
		}
		fmt.Println("   Đóng cửa sổ này KHÔNG dừng app.")
		if cfg.Paused && askYes("⏸ Đang tạm dừng báo (lệnh /tat). Báo lại ngay? (Y/n)", "Y") {
			cfg.Paused = false
			_ = saveCfg(cfg)
			fmt.Println("▶️ Đã bật lại báo.")
		}
		if askYes("Dừng theo dõi? (sẽ báo vào nhóm)", "N") {
			cmdStop()
		}
		waitEnter()
		return
	}

	fmt.Println("🔴 Chưa có tiến trình theo dõi nào đang chạy.")
	min := ask("Quét mỗi bao nhiêu phút? (1–60)", "5")
	n, err := strconv.Atoi(strings.TrimSpace(min))
	if err != nil || n < 1 || n > 60 {
		n = 5
	}
	cfg.LiveMin = intPtr(n)
	if err := saveCfg(cfg); err != nil {
		fmt.Println("❌ Lỗi lưu:", err)
		waitEnter()
		return
	}
	fmt.Printf("✅ Quét mỗi %d phút.\n", n)
	if !inWatchWindow(time.Now(), cfg) {
		fmt.Printf("⚠️ %s NGOÀI khung %s–%s → chờ tới %s mới quét (đổi ở mục 8).\n",
			time.Now().Format("15:04"), cfg.watchFrom(), cfg.watchTo(), cfg.watchFrom())
	}
	if askYes("Chạy nền ngay (ẩn — đóng cửa sổ vẫn chạy)? (Y/n)", "Y") {
		if err := startHiddenWatch(); err != nil {
			fmt.Println("❌ Không chạy nền được:", err)
			waitEnter()
			return
		}
		time.Sleep(2 * time.Second)
		if ok, pid, _ := watchRunning(); ok {
			fmt.Printf("▶️ Đang chạy nền — PID %d. Đóng cửa sổ này vẫn chạy.\n", pid)
			fmt.Println("   Dừng: mục 5, hoặc Ctrl+Alt+S (tạo ở mục 11).")
		} else {
			fmt.Println("⚠️ Chưa thấy tiến trình nền — chạy --doctor để xem.")
		}
		waitEnter()
		return
	}
	if askYes("Chạy trong cửa sổ này để xem log? (đóng cửa sổ là dừng) (y/N)", "N") {
		watch()
		return
	}
	waitEnter()
}

// cmdStop: dừng tiến trình nền và báo vào nhóm.
func cmdStop() bool {
	cfg := loadCfg()
	running, pid, since := watchRunning()
	if !running {
		fmt.Println("Không có tiến trình nào đang chạy.")
		return false
	}
	if !requirePIN("dừng theo dõi") {
		waitEnter()
		return false
	}
	fmt.Printf("Dừng PID %d (từ %s)...\n", pid, since)
	setStopIntent() // trước khi tắt, để lớp tự bật lại không bật lên
	stopWatch(pid)
	if l, ok := readWatchLock(); ok && l.PID == pid && lockAlive(l) {
		clearStopIntent()
		fmt.Println("❌ Không dừng được — mở Task Manager kết thúc ghn-tele.")
		return false
	}
	_ = os.Remove(watchLockPath())
	msg := "⏹ <b>ĐÃ DỪNG THEO DÕI</b>\n🏭 Kho: <code>" + esc(cfg.LocationID) + "</code>\n" +
		"Dừng lúc " + time.Now().Format("02/01 15:04") + " (đã nhập đúng PIN).\nBật lại: mở app → mục 5."
	if err := sendTelegram(cfg, msg); err != nil {
		fmt.Println("⚠️ Không gửi được thông báo Telegram:", err)
	} else {
		fmt.Println("✅ Đã báo vào nhóm Telegram.")
	}
	logf("[stop] Dừng theo dõi PID %d", pid)
	return true
}

// makeStopShortcut: shortcut Desktop, phím tắt Ctrl+Alt+S.
func makeStopShortcut() {
	if runtime.GOOS != "windows" {
		fmt.Println("Chỉ tạo phím tắt được trên Windows.")
		waitEnter()
		return
	}
	vbs := filepath.Join(exeDir, "stop-watch.vbs")
	content := "CreateObject(\"Wscript.Shell\").Run \"\"\"" + exePath() + "\"\" --stop\", 1, False\r\n" // 1 = hiện cửa sổ để nhập PIN
	if err := os.WriteFile(vbs, []byte(content), 0644); err != nil {
		fmt.Println("❌ Không tạo được stop-watch.vbs:", err)
		waitEnter()
		return
	}
	lnk := filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "Dung GHN Tele.lnk")
	ps := fmt.Sprintf(`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');`+
		`$s.TargetPath='wscript.exe';$s.Arguments='"%s"';$s.Hotkey='CTRL+ALT+S';$s.Save()`, lnk, vbs)
	if out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).CombinedOutput(); err != nil {
		fmt.Println("❌ Không tạo được shortcut:", err, string(out))
		waitEnter()
		return
	}
	fmt.Println("✅ Đã tạo shortcut ngoài Desktop: Dung GHN Tele")
	fmt.Println("   Ctrl+Alt+S ở bất kỳ đâu = mở cửa sổ hỏi PIN để dừng (có báo vào nhóm).")
	waitEnter()
}

// ---------------------------------------------------------------- tự chạy khi đăng nhập

func installWatchTask() {
	if runtime.GOOS != "windows" {
		fmt.Println("(không phải Windows) cài tự chạy chỉ dùng được trên Windows")
		waitEnter()
		return
	}
	cfg := loadCfg()
	cfg.AutoStart = boolPtr(true)
	_ = saveCfg(cfg)
	mode := "đăng nhập Windows (chạy ẩn bằng tài khoản đang đăng nhập)"
	channelActive := updateChannelConfigured()
	out, err := createTasks(exePath(), cfg.autoSessionOn() || channelActive)
	if err != nil && cfg.autoSessionOn() && !channelActive {
		mode = "khởi động máy (SYSTEM, sẽ KHÔNG tự lấy được SESSION)"
		out, err = createTasks(exePath(), false)
	}
	if err != nil {
		fmt.Printf("❌ Lỗi tạo lịch: %v\n%s\n", err, out)
		fmt.Println("Gợi ý: đặt exe vào C:\\GHN-Tele\\ rồi thử lại.")
		waitEnter()
		return
	}
	fmt.Println("✅ Đã cài tự chạy khi " + mode + ".")
	fmt.Println("✅ Tự bật lại: cứ 5 phút kiểm tra, bị tắt ngang thì bật lại và báo vào nhóm.")

	running, _, _ := watchRunning()
	if !running && askYes("Chạy nền NGAY bây giờ? (Y/n)", "Y") {
		if err := startHiddenWatch(); err != nil {
			fmt.Println("❌ Không chạy nền được:", err)
		} else {
			time.Sleep(2 * time.Second)
			if ok, pid, _ := watchRunning(); ok {
				fmt.Printf("✅ Đang chạy nền (PID %d).\n", pid)
			} else {
				fmt.Println("⚠️ Chưa thấy tiến trình — chạy --doctor để xem.")
			}
		}
	}
	waitEnter()
}

// menuAutoStart: mục 6 — cài/cập nhật hoặc gỡ tự chạy (đã qua PIN).
func menuAutoStart() {
	switch ask("1 = cài / cập nhật tự chạy + tự bật lại • 2 = gỡ tự chạy", "1") {
	case "2":
		removeWatchTask()
	default:
		installWatchTask()
	}
}

func removeWatchTask() {
	deleteTasks()
	cfg := loadCfg()
	cfg.AutoStart = boolPtr(false)
	_ = saveCfg(cfg)
	fmt.Println("Đã gỡ tự chạy + tự bật lại. App đang chạy (nếu có) vẫn chạy tới khi dừng.")
	waitEnter()
}
