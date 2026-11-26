package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const (
	version = "2.16.5"

	appTitle  = "GHN Đã đến kho → Telegram"
	cfgName   = "config.json"
	logName   = "ghn-tele.log"
	httpTimeo = 20 * time.Second
	tgGap     = 1200 * time.Millisecond
	maxRows   = 20
	maxFetch  = 300
	tgMaxLen  = 3900
	tgRetries = 3
)

// biến (không phải const) để test trỏ sang server giả
var (
	apiBase = "https://inside-prd-api.ghn.vn"
	tgBase  = "https://api.telegram.org"
)

type WillDropOff struct {
	NumOfPkg        int `json:"numOfPkg"`
	NumOfPkgFwd     int `json:"numOfPkgFwd"`
	NumOfPkgBulky   int `json:"numOfPkgBulky"`
	NumOfPkgFragile int `json:"numOfPkgFragile"`
}

type Transport struct {
	PartnerTransportationCode string      `json:"partnerTransportationCode"`
	RouteScheduleName         string      `json:"routeScheduleName"`
	TypeOps                   string      `json:"typeOps"`
	VehiclePlate              string      `json:"vehiclePlate"`
	Capacity                  interface{} `json:"capacity"`
	DriverName                string      `json:"driverName"`
	DriverPhone               string      `json:"driverPhone"`
	ArrivalTime               string      `json:"arrivalTime"`
	ExpectedArrivalTime       string      `json:"expectedArrivalTime"`
	ExpectedDepartureTime     string      `json:"expectedDepartureTime"`
	StopSessionStatus         string      `json:"stopSessionStatus"`
	WillDropOff               WillDropOff `json:"willDropOff"`
	NumberOfRemainingStops    int         `json:"numberOfRemainingStops"`
	IncludeSortGroupCodes     []string    `json:"includeSortGroupCodes"` // "Thuộc các nhóm"
	ExcludeSortGroupCodes     []string    `json:"excludeSortGroupCodes"` // "Ngoại trừ các nhóm"
}

// DepartedTrip: một dòng ở tab "Đã rời kho" (GET transportation-transporting-list).
type DepartedTrip struct {
	PartnerTransportationCode string   `json:"partnerTransportationCode"`
	Code                      string   `json:"code"`
	ArrivalTime               flexTime `json:"arrivalTime"`
	DepartureTime             flexTime `json:"departureTime"`
	PartnerVehicleNumber      string   `json:"partnerVehicleNumber"`
	PartnerStaffName          string   `json:"partnerStaffName"`
}

// flexTime: GHN có thể trả giờ dạng chuỗi ISO, số mili-giây hoặc null → luôn về chuỗi ISO.
type flexTime string

func (f *flexTime) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexTime(s)
		return nil
	}
	var n float64
	if json.Unmarshal(b, &n) == nil && n > 0 {
		*f = flexTime(time.UnixMilli(int64(n)).Format(time.RFC3339))
		return nil
	}
	*f = ""
	return nil
}

// fetchDeparted: tab "Đã rời kho". Trang web gọi GET kèm locationId/locationType/source
// trên query string (newRequestInside tự gắn). Chỉ gọi khi có chuyến biến mất.
func fetchDeparted(c Config) (map[string]DepartedTrip, error) {
	out := map[string]DepartedTrip{}
	const limit, maxRows = 50, 200
	for offset := 0; offset < maxRows; offset += limit {
		q := url.Values{}
		q.Set("getTotal", "true")
		q.Set("offset", strconv.Itoa(offset))
		q.Set("limit", strconv.Itoa(limit))
		q.Set("locationId", c.LocationID)
		q.Set("locationType", "GHN_HUB")
		q.Set("source", "INSIDE_WEB_APP/ktc-van-tai/transport/transporting")
		code, data, err := httpJSON("GET", apiBase+"/tms/v1/transportation-transporting-list?"+q.Encode(), map[string]string{
			"Authorization": "Bearer " + c.Session,
			"Origin":        "https://nhanh.ghn.vn",
			"Referer":       "https://nhanh.ghn.vn/ktc-van-tai/transport/transporting",
		}, nil)
		if err != nil {
			return out, err
		}
		var r struct {
			Status  string         `json:"status"`
			Message string         `json:"message"`
			Total   int            `json:"total"`
			Data    []DepartedTrip `json:"data"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return out, fmt.Errorf("Đã rời kho: phản hồi lạ (http %d)", code)
		}
		if r.Status != "OK" {
			return out, fmt.Errorf("Đã rời kho: %s (http %d)", r.Message, code)
		}
		for _, t := range r.Data {
			if t.PartnerTransportationCode != "" {
				out[t.PartnerTransportationCode] = t
			}
		}
		if len(r.Data) < limit || (r.Total > 0 && offset+len(r.Data) >= r.Total) {
			break
		}
	}
	return out, nil
}

// fetchVehicleMap: vị trí xe hiện tại (API nút bản đồ ở tab Sắp đến) → link Google Maps.
// Lỗi / không có toạ độ → "".
func fetchVehicleMap(c Config, plate string) string {
	if blank(plate) {
		return ""
	}
	code, data, err := httpJSON("POST", apiBase+"/tms/v1/transportation/get-vehicle-location", map[string]string{
		"Authorization": "Bearer " + c.Session,
		"Origin":        "https://nhanh.ghn.vn",
		"Referer":       "https://nhanh.ghn.vn/ktc-van-tai/transport/initialization",
	}, map[string]string{"vehiclePlate": strings.TrimSpace(plate)})
	if err != nil || code != 200 {
		return ""
	}
	var r struct {
		Status string `json:"status"`
		Data   []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &r) != nil || r.Status != "OK" || len(r.Data) == 0 {
		return ""
	}
	la, lo := r.Data[0].Latitude, r.Data[0].Longitude
	if la == 0 && lo == 0 {
		return ""
	}
	return fmt.Sprintf("https://maps.google.com/?q=%.6f,%.6f", la, lo)
}

type ArrivedData struct {
	TotalWillDropOff WillDropOff `json:"totalWillDropOff"`
	List             []Transport `json:"list"`
}

type ArrivedResp struct {
	Status  string        `json:"status"`
	Message string        `json:"message"`
	Total   int           `json:"total"`
	Data    []ArrivedData `json:"data"`
}

func (r *ArrivedResp) list() []Transport {
	if r == nil || len(r.Data) == 0 {
		return nil
	}
	return r.Data[0].List
}

func (r *ArrivedResp) totalWill() WillDropOff {
	if r == nil || len(r.Data) == 0 {
		return WillDropOff{}
	}
	return r.Data[0].TotalWillDropOff
}

type Config struct {
	Session          string `json:"session"`
	LocationID       string `json:"locationId"`
	TelegramToken    string `json:"telegramToken"`
	TelegramChat     string `json:"telegramChat"`         // nhóm (số âm) hoặc @kenh
	TelegramThread   int    `json:"telegramThread"`       // topic của nhóm forum
	TelegramPersonal string `json:"telegramPersonalChat"` // chat riêng của anh
	NotifyMode       string `json:"notifyMode"`           // group | personal | both
	Limit            int    `json:"limit"`
	NotifyUpcoming   *bool  `json:"notifyUpcoming"`
	LiveMin          *int   `json:"liveMin"`
	WatchFrom        string `json:"watchFrom"`
	WatchTo          string `json:"watchTo"`
	Shifts           string `json:"shifts"`
	AutoSession      *bool  `json:"autoSession"`
	PinHash          string `json:"pinHash,omitempty"` // PIN dừng/sửa (trống = mặc định 1234)
	AIEnabled        bool   `json:"aiEnabled,omitempty"`
	Paused           bool   `json:"paused,omitempty"`      // tạm dừng báo (lệnh /tat)
	HandoverMin      *int   `json:"handoverMin,omitempty"` // báo xe kẹt bàn giao quá N phút (0 = tắt)
	ManagerName      string `json:"managerName,omitempty"` // tên hiển thị người quản lý (/quanly)
	ManagerUser      string `json:"managerUser,omitempty"` // @username Telegram của chủ app (tự lấy)
	AutoStart        *bool  `json:"autoStart,omitempty"`   // false = đã gỡ tự chạy ở mục 6
	ChromeDir        string `json:"chromeDir"`
	LateMin          *int   `json:"lateMin"` // báo xe trễ quá N phút (0 = tắt)
	FastMin          *int   `json:"fastMin"` // quét nhanh khi có xe sắp tới (0 = tắt)
}

// lateMin: quá giờ dự kiến bao nhiêu phút thì báo ⏰ (mặc định 30, 0 = tắt).
func (c Config) lateMin() int {
	if c.LateMin == nil {
		return 30
	}
	if *c.LateMin < 0 {
		return 0
	}
	return *c.LateMin
}

// fastMin: chu kỳ quét khi có xe sắp tới trong 30 phút (mặc định 2, 0 = tắt).
// Không bao giờ chậm hơn liveMin.
func (c Config) fastMin() int {
	f := 2
	if c.FastMin != nil {
		f = *c.FastMin
	}
	if f <= 0 || f >= c.liveMin() {
		return 0
	}
	return f
}

// shiftsOff: anh không muốn tin đầu ca.
func (c Config) shiftsOff() bool {
	switch strings.ToLower(strings.TrimSpace(c.Shifts)) {
	case "off", "tắt", "tat", "0", "-", "khong", "không":
		return true
	}
	return false
}

func (c Config) notifyMode() string {
	m := strings.ToLower(strings.TrimSpace(c.NotifyMode))
	if m != "group" && m != "personal" && m != "both" {
		if c.TelegramChat != "" && c.TelegramPersonal != "" {
			return "both"
		}
		if c.TelegramPersonal != "" && c.TelegramChat == "" {
			return "personal"
		}
		return "group"
	}
	return m
}

type chatTarget struct {
	chat   string
	thread int
}

// targets: nơi nhận tin theo chế độ đã chọn (bỏ qua nơi chưa có ID).
func (c Config) targets() []chatTarget {
	var out []chatTarget
	mode := c.notifyMode()
	if (mode == "group" || mode == "both") && c.TelegramChat != "" {
		out = append(out, chatTarget{c.TelegramChat, c.TelegramThread})
	}
	if (mode == "personal" || mode == "both") && c.TelegramPersonal != "" {
		out = append(out, chatTarget{c.TelegramPersonal, 0})
	}
	if len(out) == 0 && c.TelegramChat != "" {
		out = append(out, chatTarget{c.TelegramChat, c.TelegramThread})
	}
	return out
}

func (c Config) groupLabel() string {
	switch c.notifyMode() {
	case "personal":
		return "chat riêng"
	case "both":
		return "nhóm + chat riêng"
	}
	return "nhóm"
}

func (c Config) shiftsRaw() string {
	if c.shiftsOff() {
		return "tắt"
	}
	t := strings.TrimSpace(c.Shifts)
	if t == "" {
		return "05:00-08:00,11:00-14:00,18:00-21:00"
	}
	return t
}

func (c Config) watchFrom() string {
	t := strings.TrimSpace(c.WatchFrom)
	if t == "" {
		return "05:00"
	}
	return t
}

func (c Config) watchTo() string {
	t := strings.TrimSpace(c.WatchTo)
	if t == "" {
		return "21:00"
	}
	return t
}

// arriveSoonMin: báo trước khi xe đến N phút. 0 = không báo trước.

// autoStartOn: tự chạy + tự bật lại (mặc định bật; mục 6 gỡ thì tắt).
func (c Config) autoStartOn() bool { return c.AutoStart == nil || *c.AutoStart }

func (c Config) autoSessionOn() bool {
	return c.AutoSession == nil || *c.AutoSession
}

func (c Config) liveMin() int {
	if c.LiveMin == nil {
		return 5
	}
	if *c.LiveMin < 0 {
		return 0
	}
	if *c.LiveMin > 60 {
		return 60
	}
	return *c.LiveMin
}

func (c Config) liveOn() bool {
	return c.liveMin() > 0
}

func (c Config) upcomingOn() bool {
	return c.NotifyUpcoming == nil || *c.NotifyUpcoming
}

func boolPtr(b bool) *bool { return &b }

func intPtr(i int) *int { return &i }

var (
	exeDir  string
	cfgPath string
	logPath string
	stdin   *bufio.Reader
	lastErr string
)

func main() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Println("Lỗi:", err)
		waitEnter()
		return
	}
	exePath, _ = filepath.Abs(exePath)
	exeDir = filepath.Dir(exePath)
	cfgPath = filepath.Join(exeDir, cfgName)
	logPath = filepath.Join(exeDir, logName)
	stdin = bufio.NewReader(os.Stdin)

	if len(os.Args) > 1 && os.Args[1] == "--apply-staged-update" {
		if err := applyStagedUpdate(exeDir, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(appTitle, "v"+version)
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("cmd", "/c", "chcp 65001 >nul").Run()
	}

	switch arg := ""; {
	case len(os.Args) > 1:
		arg = os.Args[1]
		switch arg {
		case "--check-update":
			r, _, newer, err := checkUpdate()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if newer {
				fmt.Println("Có bản v" + r.Version)
			} else {
				fmt.Println("Kênh cập nhật chưa có bản mới hơn v" + version + ".")
			}
			return
		case "--update":
			menuUpdate()
			return
		case "--update-keygen", "--update-github-init":
			if len(os.Args) != 4 {
				fmt.Fprintln(os.Stderr, "Cú pháp: --update-keygen <khóa-riêng> <URL-update.json>")
				fmt.Fprintln(os.Stderr, "Hoặc: --update-github-init <khóa-riêng> <chủ-repo/tên-repo>")
				os.Exit(2)
			}
			manifestURL := os.Args[3]
			useGitHub := arg == "--update-github-init"
			if useGitHub {
				var err error
				manifestURL, err = githubUpdateURL(os.Args[3])
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(2)
				}
			}
			if err := updateKeygen(os.Args[2], manifestURL, useGitHub); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "--update-sign":
			if len(os.Args) != 8 && len(os.Args) != 9 {
				fmt.Fprintln(os.Stderr, "Cú pháp: --update-sign <khóa-riêng> <phiên-bản> <hệ-điều-hành> <kiến-trúc> <tệp-exe> <URL-tải> [ghi-chú]")
				os.Exit(2)
			}
			if err := signUpdate(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "--run-once", "-r":
			runOnce()
			return
		case "--watch", "-w":
			watch()
			return
		case "--install-watch":
			installWatchTask()
			return
		case "--remove-watch":
			if requirePIN("gỡ tự chạy") {
				removeWatchTask()
			}
			return
		case "--ensure":
			cmdEnsure()
			return
		case "--demo", "-d":
			runDemo()
			return
		case "--help", "-h", "/?":
			printHelp()
			return
		case "--session", "-s":
			refreshSessionCLI()
			return
		case "--stop":
			cmdStop()
			return
		case "--setup":
			if !cfgReady(loadCfg()) || requirePIN("cài lại") {
				wizard()
			}
			return
		case "--doctor":
			doctor()
			return
		case "--debug-session":
			arg := ""
			if len(os.Args) > 2 {
				arg = os.Args[2]
			}
			debugSession(arg)
			return
		}
	}
	menu()
}

// ------------------------------------------------------------------ config

// cleanChatID: bỏ khoảng trắng / dấu nháy khi dán Chat ID.
// Telegram trả ID nhóm dạng số âm (-1001234567890); nếu lưu kèm dấu nháy
// thì sendMessage báo "chat not found".
func cleanChatID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'")
	return strings.TrimSpace(s)
}

func loadCfg() Config {
	var c Config
	b, err := os.ReadFile(cfgPath)
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Limit <= 0 {
		c.Limit = 50
	}
	c.Session = strings.TrimSpace(c.Session)
	c.LocationID = strings.TrimSpace(c.LocationID)
	c.TelegramToken = strings.TrimSpace(c.TelegramToken)
	c.TelegramChat = cleanChatID(c.TelegramChat)
	c.TelegramPersonal = cleanChatID(c.TelegramPersonal)
	return c
}

var cfgWriteMu sync.Mutex

func saveCfg(c Config) error {
	cfgWriteMu.Lock()
	defer cfgWriteMu.Unlock()
	return writeCfg(c)
}

func editCfgFile(change func(*Config)) error {
	return editCfgFileChecked(func(c *Config) error {
		change(c)
		return nil
	})
}

func editCfgFileChecked(change func(*Config) error) error {
	cfgWriteMu.Lock()
	defer cfgWriteMu.Unlock()
	c := loadCfg()
	if err := change(&c); err != nil {
		return err
	}
	return writeCfg(c)
}

func writeCfg(c Config) error {
	c.Session = strings.TrimSpace(c.Session)
	c.LocationID = strings.TrimSpace(c.LocationID)
	c.TelegramToken = strings.TrimSpace(c.TelegramToken)
	c.TelegramChat = cleanChatID(c.TelegramChat)
	c.TelegramPersonal = cleanChatID(c.TelegramPersonal)
	if c.Limit <= 0 {
		c.Limit = 50
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(cfgPath, append(b, '\n'))
}

func cfgReady(c Config) bool {
	if c.LocationID == "" || c.TelegramToken == "" {
		return false
	}
	if c.TelegramChat == "" && c.TelegramPersonal == "" {
		return false
	}
	return c.Session != "" || c.autoSessionOn()
}

func ask(label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := stdin.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func askYes(label, def string) bool {
	v := strings.ToLower(ask(label, def))
	if v == "" {
		v = strings.ToLower(def)
	}
	return strings.HasPrefix(v, "y")
}

func waitEnter() {
	fmt.Println()
	fmt.Print("(Nhấn Enter để tiếp tục...)")
	_, _ = stdin.ReadString('\n')
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// esc: escape ký tự HTML cho parse_mode=HTML của Telegram.
// Thiếu bước này, tên tài xế / tuyến / biển số có & hoặc < sẽ làm Telegram
// trả 400 "can't parse entities" và TOÀN BỘ tin bị mất.
func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ------------------------------------------------------------------ log

func logf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	line := time.Now().Format("2006-01-02 15:04:05") + " " + msg
	fmt.Println(line)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
	if fi, e := f.Stat(); e == nil && fi.Size() > 1024*1024 {
		f.Close()
		_ = os.Rename(logPath, logPath+".old")
	}
}

// ------------------------------------------------------------------ http

func httpJSON(method, url string, headers map[string]string, body interface{}) (int, []byte, error) {
	return httpJSONTimeout(method, url, headers, body, httpTimeo)
}

func httpJSONTimeout(method, url string, headers map[string]string, body interface{}, timeout time.Duration) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	cli := &http.Client{Timeout: timeout}
	resp, err := cli.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

// ------------------------------------------------------------------ jwt / session

// jwtExpiredAt: đọc hạn SESSION từ payload JWT.
// Trả về ok=false khi KHÔNG đọc được hạn (token rác / không phải JWT) —
// trước đây các nhánh lỗi trả về true nên menu hiện "hết hạn 01/01 00:00".
func jwtExpiredAt(session string) (time.Time, bool) {
	parts := strings.Split(session, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1] + strings.Repeat("=", (4-len(parts[1])%4)%4))
		if err != nil {
			return time.Time{}, false
		}
	}
	var p struct {
		Expired json.RawMessage `json:"expired"`
		Exp     json.RawMessage `json:"exp"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return time.Time{}, false
	}
	raw := p.Expired
	if len(raw) == 0 {
		raw = p.Exp
	}
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
			if t, e := time.Parse(layout, s); e == nil {
				return t, true
			}
		}
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		if n > 1e12 {
			return time.UnixMilli(n), true
		}
		if n > 1e9 {
			return time.Unix(n, 0), true
		}
	}
	return time.Time{}, false
}

func extendSession(c *Config) error {
	code, data, err := httpJSON("GET", apiBase+"/iam/v1/session/extended", map[string]string{
		"Authorization": "Bearer " + c.Session,
		"Origin":        "https://nhanh.ghn.vn",
	}, nil)
	if err != nil {
		return err
	}
	var resp struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &resp)
	if code != 200 || resp.Status != "OK" || resp.Message == "" {
		return fmt.Errorf("extend session thất bại (http %d) %s", code, resp.Message)
	}
	c.Session = resp.Message
	if err := editCfgFile(func(latest *Config) { latest.Session = c.Session }); err != nil {
		return err
	}
	logf("[session] Đã gia hạn SESSION")
	return nil
}

func ensureSession(c *Config) error {
	if c.Session == "" {
		if c.autoSessionOn() {
			if err := tryBrowserSession(c, false); err == nil {
				return nil
			}
		}
		return errors.New("chưa có SESSION")
	}
	exp, ok := jwtExpiredAt(c.Session)
	if !ok {
		return nil
	}
	left := time.Until(exp)
	if left > 4*time.Hour {
		return nil
	}
	logf("[session] Còn %.0f phút, thử gia hạn...", left.Minutes())
	err := extendSession(c)
	if err == nil {
		return nil
	}
	if c.autoSessionOn() {
		// GHN không cho gia hạn → thử lấy SESSION mới từ trình duyệt
		logf("[session] Gia hạn lỗi (%v) — thử lấy từ trình duyệt", err)
		if bErr := tryBrowserSession(c, false); bErr == nil {
			return nil
		}
	}
	if left > time.Minute {
		// SESSION vẫn còn hạn (gia hạn lỗi thường do mạng chập chờn) → dùng tiếp, vòng sau thử lại
		logf("[session] Chưa gia hạn được, SESSION còn %.0f phút — dùng tiếp", left.Minutes())
		return nil
	}
	return err
}

func isAuthErr(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "token") || strings.Contains(s, "401") || strings.Contains(s, "403") || strings.Contains(s, "unauthorized")
}

// ------------------------------------------------------------------ GHN API

func fetchTransports(c Config, kind string) (*ArrivedResp, error) {
	if c.LocationID == "" {
		return nil, errors.New("chưa có mã kho (locationId)")
	}
	path := "/tms/v1/transportation/get-arrived-transports"
	referer := "https://nhanh.ghn.vn/ktc-van-tai/transport/arrival"
	source := "INSIDE_WEB_APP/ktc-van-tai/transport/arrival"
	extra := map[string]interface{}{}
	if kind == "upcoming" {
		path = "/tms/v1/transportation/get-upcoming-transports"
		referer = "https://nhanh.ghn.vn/ktc-van-tai/transport/initialization"
		source = "INSIDE_WEB_APP/ktc-van-tai/transport/initialization"
		now := time.Now()
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
		extra["startTime"] = start.Format(time.RFC3339)
		extra["endTime"] = end.Format(time.RFC3339)
	}

	limit := c.Limit
	if limit <= 0 {
		limit = 50
	}
	var out *ArrivedResp
	offset := 0
	for {
		body := map[string]interface{}{
			"offset":                    offset,
			"limit":                     limit,
			"partnerTransportationCode": "",
			"vehiclePlate":              "",
			"locationId":                c.LocationID,
			"locationType":              "GHN_HUB",
			"source":                    source,
		}
		if kind == "arrived" {
			body["stopSessionStatus"] = ""
		}
		for k, v := range extra {
			body[k] = v
		}
		code, data, err := httpJSON("POST", apiBase+path, map[string]string{
			"Authorization": "Bearer " + c.Session,
			"Origin":        "https://nhanh.ghn.vn",
			"Referer":       referer,
		}, body)
		if err != nil {
			return nil, err
		}
		var resp ArrivedResp
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("phản hồi không phải JSON (http %d): %s", code, truncate(string(data), 200))
		}
		if resp.Status != "OK" {
			return nil, fmt.Errorf("%s (http %d)", resp.Message, code)
		}
		if out == nil {
			out = &resp
		} else {
			out.Total = resp.Total
			if len(out.Data) == 0 && len(resp.Data) > 0 {
				out.Data = resp.Data
			} else if len(resp.Data) > 0 {
				out.Data[0].List = append(out.Data[0].List, resp.Data[0].List...)
				if out.Data[0].TotalWillDropOff == (WillDropOff{}) {
					out.Data[0].TotalWillDropOff = resp.Data[0].TotalWillDropOff
				}
			}
		}
		got := len(resp.list())
		if got < limit || offset+got >= maxFetch || (resp.Total > 0 && offset+got >= resp.Total) {
			break
		}
		offset += limit
	}
	return out, nil
}

func fetchArrived(c Config) (*ArrivedResp, error) {
	return fetchTransports(c, "arrived")
}

func fetchUpcoming(c Config) (*ArrivedResp, error) {
	return fetchTransports(c, "upcoming")
}

func statusVi(s string) string {
	switch s {
	case "ARRIVED":
		return "Chưa bàn giao"
	case "TRANSFERRED":
		return "Đã bàn giao"
	case "TRANSFERRING":
		return "Đang bàn giao"
	}
	if s == "" {
		return "-"
	}
	return s
}

func typeOpsVi(s string) string {
	switch s {
	case "pickup":
		return "Lấy"
	case "delivery":
		return "Giao"
	case "delivery_and_pickup":
		return "Giao và Lấy"
	case "sorting":
		return "Phân loại"
	}
	if s == "" {
		return "-"
	}
	return s
}

func parseISO(iso string) (time.Time, bool) {
	iso = strings.TrimSpace(iso)
	if iso == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func fmtTime(iso string) string {
	t, ok := parseISO(iso)
	if !ok {
		if strings.TrimSpace(iso) == "" {
			return "-"
		}
		return iso
	}
	return t.Local().Format("02/01/2006 - 15:04")
}

func fmtTimeShort(iso string) string {
	t, ok := parseISO(iso)
	if !ok {
		if strings.TrimSpace(iso) == "" {
			return "-"
		}
		return iso
	}
	return t.Local().Format("15:04")
}

func lateText(item Transport) string {
	a, ok1 := parseISO(item.ArrivalTime)
	e, ok2 := parseISO(item.ExpectedArrivalTime)
	if !ok1 || !ok2 {
		return ""
	}
	diff := int(a.Sub(e).Minutes())
	if diff == 0 {
		return "Đến đúng giờ"
	}
	abs := diff
	if abs < 0 {
		abs = -abs
	}
	h := abs / 60
	m := abs % 60
	unit := fmt.Sprintf("%d phút", m)
	if h > 0 {
		if m > 0 {
			unit = fmt.Sprintf("%d giờ %d phút", h, m)
		} else {
			unit = fmt.Sprintf("%d giờ", h)
		}
	}
	if diff > 0 {
		return "Đến trễ " + unit
	}
	return "Đến sớm " + unit
}

func totalPkg(w WillDropOff) int {
	return w.NumOfPkg + w.NumOfPkgBulky + w.NumOfPkgFwd
}

func capStr(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case string:
		return x
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// ------------------------------------------------------------------ tin Telegram

// ------------------------------------------------------------------ telegram

// tgPermanent: lỗi gửi lại cũng vô ích (chat not found, bot bị chặn, sai token...).
type tgPermanent struct{ error }

func isPermanent(err error) bool {
	var p tgPermanent
	return errors.As(err, &p)
}

func tgCall(token, method string, params map[string]interface{}) error {
	var last error
	for try := 0; try < tgRetries; try++ {
		code, data, err := httpJSON("POST", tgBase+"/bot"+token+"/"+method, nil, params)
		if err != nil {
			last = err
			time.Sleep(time.Duration(try+1) * 2 * time.Second)
			continue
		}
		var resp struct {
			Ok          bool   `json:"ok"`
			Description string `json:"description"`
			Parameters  struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.Unmarshal(data, &resp)
		if code == 200 && resp.Ok {
			return nil
		}
		last = fmt.Errorf("Telegram: %s (http %d)", resp.Description, code)
		if code == 429 { // bị giới hạn tần suất → chờ đúng retry_after
			wait := resp.Parameters.RetryAfter
			if wait <= 0 {
				wait = 3
			}
			time.Sleep(time.Duration(wait) * time.Second)
			continue
		}
		if code >= 500 { // lỗi tạm thời của Telegram
			time.Sleep(time.Duration(try+1) * 2 * time.Second)
			continue
		}
		return tgPermanent{last} // 400/401/403: thử lại cũng vô ích
	}
	return last
}

// cutByte: vị trí cắt an toàn theo biên ký tự UTF-8 (không cắt giữa 1 chữ).
func cutByte(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return n
}

// splitMsg: Telegram giới hạn 4096 ký tự/tin → cắt thành nhiều phần,
// ưu tiên cắt ở cuối dòng để không làm vỡ thẻ HTML.
func splitMsg(text string) []string {
	if len(text) <= tgMaxLen {
		return []string{text}
	}
	var out []string
	var cur strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		for len(line) > tgMaxLen {
			n := cutByte(line, tgMaxLen)
			if n == 0 {
				n = tgMaxLen
			}
			out = append(out, line[:n])
			line = line[n:]
		}
		if cur.Len() > 0 && cur.Len()+len(line) > tgMaxLen {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// tgSendParams: tham số sendMessage. Nhóm forum (Topics) cần message_thread_id
// để tin vào đúng topic thay vì rơi vào "General". quiet = không rung/chuông.
func tgSendParams(chat string, thread int, text string, quiet ...bool) map[string]interface{} {
	p := map[string]interface{}{
		"chat_id":                  chat,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if thread > 0 {
		p["message_thread_id"] = thread
	}
	if len(quiet) > 0 && quiet[0] {
		p["disable_notification"] = true
	}
	return p
}

func sendTelegramAt(c Config, t chatTarget, text string, quiet bool) error {
	for i, part := range splitMsg(text) {
		if i > 0 {
			time.Sleep(tgGap)
		}
		if err := tgCall(c.TelegramToken, "sendMessage", tgSendParams(t.chat, t.thread, part, quiet)); err != nil {
			return err
		}
	}
	return nil
}

// sendTelegramTo: gửi tới từng nơi; trả về những nơi lỗi (để chỉ gửi lại nơi đó,
// không làm nơi đã nhận bị trùng tin).
func sendTelegramTo(c Config, targets []chatTarget, text string, quiet bool) (failed []chatTarget, errs []error) {
	for _, t := range targets {
		if err := sendTelegramAt(c, t, text, quiet); err != nil {
			failed = append(failed, t)
			errs = append(errs, fmt.Errorf("gửi tới %s: %w", t.chat, err))
		}
	}
	return
}

func sendTelegram(c Config, text string) error {
	_, errs := sendTelegramTo(c, c.targets(), text, false)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// ------------------------------------------------------------------ hành động

func runOnce() {
	cfg := loadCfg()
	if !cfgReady(cfg) {
		logf("[run-once] Chưa đủ cấu hình. Mở exe để cài.")
		return
	}
	if err := ensureSession(&cfg); err != nil {
		lastErr = err.Error()
		logf("[run-once] %v", err)
		_ = sendTelegram(cfg, "⚠️ <b>Không gia hạn được SESSION GHN</b>\n"+esc(err.Error())+"\nHãy mở lại app để cập nhật SESSION mới.")
		return
	}
	_ = sendReportNow(cfg)
}

// sendReportNow: báo cáo ngay 1 lần (--run-once).

func testTelegram(c Config) {
	if c.TelegramToken == "" || len(c.targets()) == 0 {
		fmt.Println("Chưa có Telegram Token / Chat ID (nhóm hoặc riêng)")
		waitEnter()
		return
	}
	err := sendTelegram(c, "🧪 <b>Tin thử từ GHN Đã đến kho → Telegram</b>\nNếu bạn thấy tin này nghĩa là kết nối ổn. ("+time.Now().Format("15:04:05")+")")
	if err != nil {
		fmt.Println("❌ Lỗi:", err)
	} else {
		fmt.Println("✅ Đã gửi tin thử. Kiểm tra Telegram.")
	}
	waitEnter()
}

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	abs, _ := filepath.Abs(p)
	return abs
}

// ---------------------------------------------------------------- chọn nơi nhận tin

type tgChat struct {
	ID        json.RawMessage `json:"id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Username  string          `json:"username"`
	FirstName string          `json:"first_name"`
	LastName  string          `json:"last_name"`
}

func (c tgChat) isGroup() bool {
	return c.Type == "group" || c.Type == "supergroup" || c.Type == "channel"
}

func (c tgChat) kindVi() string {
	switch c.Type {
	case "group":
		return "nhóm"
	case "supergroup":
		return "nhóm lớn"
	case "channel":
		return "kênh"
	}
	return "cá nhân"
}

func (c tgChat) name() string {
	if strings.TrimSpace(c.Title) != "" {
		return c.Title
	}
	if c.Username != "" {
		return "@" + c.Username
	}
	if n := strings.TrimSpace(c.FirstName + " " + c.LastName); n != "" {
		return n
	}
	return "(không tên)"
}

type tgIncoming struct {
	MessageID       int         `json:"message_id"`
	Date            int64       `json:"date"`
	From            *tgUser     `json:"from"`
	SenderChat      *tgChat     `json:"sender_chat"`
	ReplyTo         *tgIncoming `json:"reply_to_message"`
	Text            string      `json:"text"`
	Caption         string      `json:"caption"`
	MessageThreadID int         `json:"message_thread_id"`
	Chat            tgChat      `json:"chat"`
}

type tgUpdate struct {
	UpdateID    int64            `json:"update_id"`
	Message     *tgIncoming      `json:"message"`
	ChannelPost *tgIncoming      `json:"channel_post"`
	Callback    *tgCallbackQuery `json:"callback_query"`
}

func rawIDString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return cleanChatID(s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

func tgBotUsername(token string) string {
	code, data, err := httpJSON("GET", tgBase+"/bot"+token+"/getMe", nil, nil)
	if err != nil || code != 200 {
		return ""
	}
	var r struct {
		Ok     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &r) != nil || !r.Ok {
		return ""
	}
	return r.Result.Username
}

func fetchTgUpdates(token string) ([]tgUpdate, error) {
	code, data, err := httpJSON("POST", tgBase+"/bot"+token+"/getUpdates", nil, map[string]interface{}{
		"limit":           100,
		"timeout":         0,
		"allowed_updates": []string{"message", "channel_post"},
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Ok          bool       `json:"ok"`
		Description string     `json:"description"`
		Result      []tgUpdate `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("Telegram trả về dữ liệu lạ (http %d): %s", code, truncate(string(data), 160))
	}
	if code != 200 || !resp.Ok {
		return nil, fmt.Errorf("Telegram: %s (http %d)", resp.Description, code)
	}
	return resp.Result, nil
}

type chatChoice struct {
	chat   tgChat
	id     string
	thread int
	last   string
}

// scanChats: các chat gần đây đã nhắn cho bot (nhóm/kênh trước, mới nhất trước).
func scanChats(token string) ([]chatChoice, error) {
	ups, err := fetchTgUpdates(token)
	saved := loadBotState().Chats // app đang chạy thì bot đã "nhận" tin trước → lấy từ bản ghi
	if err != nil && len(saved) == 0 {
		return nil, err
	}
	var list []chatChoice
	idx := map[string]int{}
	for i := len(ups) - 1; i >= 0; i-- { // mới nhất trước
		inc := ups[i].Message
		if inc == nil {
			inc = ups[i].ChannelPost
		}
		if inc == nil {
			continue
		}
		id := rawIDString(inc.Chat.ID)
		if id == "" {
			continue
		}
		txt := strings.TrimSpace(inc.Text)
		if txt == "" {
			txt = strings.TrimSpace(inc.Caption)
		}
		if _, ok := idx[id]; ok {
			continue // đã có bản mới hơn (đang duyệt từ mới → cũ)
		}
		idx[id] = len(list)
		list = append(list, chatChoice{chat: inc.Chat, id: id, thread: inc.MessageThreadID, last: txt})
	}
	for _, c := range saved {
		if _, ok := idx[c.ID]; ok {
			continue
		}
		idx[c.ID] = len(list)
		list = append(list, chatChoice{chat: tgChat{ID: json.RawMessage(strconv.Quote(c.ID)), Type: c.Type, Title: c.Name},
			id: c.ID, thread: c.Thread, last: c.Last})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].chat.isGroup() && !list[j].chat.isGroup() })
	return list, nil
}

func findChatID() {
	cfg := loadCfg()
	if cfg.TelegramToken == "" {
		fmt.Println("Cần Bot Token trước. Vào mục sửa cấu hình (1).")
		waitEnter()
		return
	}
	if u := tgBotUsername(cfg.TelegramToken); u != "" {
		fmt.Println("Bot đang dùng: @" + u)
	} else {
		fmt.Println("⚠️ Không gọi được getMe — kiểm tra lại Bot Token.")
	}
	fmt.Println()
	fmt.Println("Để bot gửi được vào NHÓM:")
	fmt.Println("  1. Mở nhóm cần nhận tin → Add member → thêm bot ở trên")
	fmt.Println("  2. Gõ /start trong nhóm (bot luôn nhận được lệnh này)")
	fmt.Println("     Nếu bot bật Privacy mà vẫn không thấy nhóm: @BotFather → /setprivacy → Disable")
	fmt.Println("  3. Quay lại đây, Enter để quét")
	fmt.Print("\nEnter để quét tin nhắn gần đây... ")
	_, _ = stdin.ReadString('\n')

	list, err := scanChats(cfg.TelegramToken)
	if err != nil {
		fmt.Println("❌", err)
		waitEnter()
		return
	}

	if len(list) == 0 {
		fmt.Println()
		fmt.Println("Chưa thấy chat nào. Kiểm tra:")
		fmt.Println("  • Bot đã được thêm vào nhóm chưa?")
		fmt.Println("  • Đã gửi /start hoặc 1 tin trong nhóm chưa?")
		fmt.Println("  • Bot có bị bật Privacy không? (BotFather → /setprivacy → Disable)")
		waitEnter()
		return
	}

	hasGroup := false
	fmt.Println()
	for i, c := range list {
		icon := "👤"
		if c.chat.isGroup() {
			icon = "👥"
			hasGroup = true
		}
		fmt.Printf("  %2d) %s [%s] %s — %s\n", i+1, icon, c.chat.kindVi(), c.chat.name(), c.id)
		if c.last != "" {
			fmt.Printf("      tin cuối: %s\n", truncate(strings.ReplaceAll(c.last, "\n", " "), 60))
		}
	}
	if !hasGroup {
		fmt.Println()
		fmt.Println("⚠️ Chưa thấy nhóm nào. Thêm bot vào nhóm rồi gõ /start trong nhóm, sau đó quét lại.")
	}

	fmt.Println()
	sel := ask(fmt.Sprintf("Chọn số để nhận tin (1-%d)", len(list)), "1")
	n, err := strconv.Atoi(strings.TrimSpace(sel))
	if err != nil || n < 1 || n > len(list) {
		fmt.Println("❌ Số không hợp lệ.")
		waitEnter()
		return
	}
	ch := list[n-1]
	if ch.chat.isGroup() {
		cfg.TelegramChat = ch.id
		cfg.TelegramThread = ch.thread
		fmt.Printf("✅ Nhóm nhận tin: «%s» (%s)\n", ch.chat.name(), ch.id)
	} else {
		cfg.TelegramPersonal = ch.id
		fmt.Printf("✅ Chat riêng nhận tin: «%s» (%s)\n", ch.chat.name(), ch.id)
	}

	// chọn nơi nhận
	hasG := cfg.TelegramChat != ""
	hasP := cfg.TelegramPersonal != ""
	switch {
	case hasG && hasP:
		fmt.Println("Báo vào: 1 nhóm · 2 chat riêng · 3 cả hai")
		choice := ask("Chọn", "3")
		switch choice {
		case "1":
			cfg.NotifyMode = "group"
		case "2":
			cfg.NotifyMode = "personal"
		default:
			cfg.NotifyMode = "both"
		}
	case hasG:
		cfg.NotifyMode = "group"
	default:
		cfg.NotifyMode = "personal"
	}
	if err := saveCfg(cfg); err != nil {
		fmt.Println("❌ Không lưu được config:", err)
		waitEnter()
		return
	}
	fmt.Println("→ Sẽ báo vào:", cfg.groupLabel())
	fmt.Println("   (chọn thêm nơi còn lại ở mục 4 lần nữa, rồi chọn 'cả hai')")
	if askYes("Gửi tin thử vào đây luôn? (Y/n)", "Y") {
		if err := sendTelegram(cfg, "🧪 <b>Tin thử</b> — đã nối được tới "+esc(ch.chat.name())+" ("+time.Now().Format("15:04:05")+")"); err != nil {
			fmt.Println("❌", err)
			fmt.Println("   'chat not found' = bot đã bị mời khỏi nhóm, hoặc chọn nhầm chat.")
		} else {
			fmt.Println("✅ Đã gửi. Kiểm tra nhóm trên Telegram.")
		}
	}
	waitEnter()
}

// setArriveSoon: đổi số phút báo trước khi xe đến (0 = tắt báo trước).

// menuSession: bật/tắt tự lấy SESSION từ trình duyệt + lấy ngay.
func menuSession() {
	cfg := loadCfg()
	state := "tắt"
	def := "N"
	if cfg.autoSessionOn() {
		state, def = "bật", "Y"
	}
	fmt.Println("Tự lấy SESSION GHN từ Chrome/Edge/Cốc Cốc khi hết hạn: " + state)
	fmt.Println("(app đọc file Local Storage của trình duyệt, không sửa gì; cần đã đăng nhập nhanh.ghn.vn)")
	cfg.AutoSession = boolPtr(askYes("Bật tự lấy SESSION? (Y/n)", def))
	if err := saveCfg(cfg); err != nil {
		fmt.Println("❌ Lỗi lưu:", err)
	}
	fmt.Printf("→ Tự lấy SESSION: %v\n", cfg.autoSessionOn())
	if askYes("Lấy SESSION từ trình duyệt ngay bây giờ? (Y/n)", "Y") {
		if err := tryBrowserSession(&cfg, true); err != nil {
			fmt.Println("❌", err)
			fmt.Println("   Xem chi tiết bằng: ghn-tele.exe --debug-session")
		} else {
			fmt.Println("✅ Đã lấy và lưu SESSION mới.")
		}
	}
	waitEnter()
}

// menuBot: bật/tắt bot 2 chiều.

func onOff(b bool) string {
	if b {
		return "bật"
	}
	return "tắt"
}

func boolStr(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

func printHelp() {
	fmt.Println(appTitle + " v" + version)
	fmt.Println()
	fmt.Println("Cách dùng:")
	fmt.Println("  ghn-tele.exe                  mở bảng điều khiển")
	fmt.Println("  ghn-tele.exe --watch, -w      chạy theo dõi (app tự bật khi cần)")
	fmt.Println("  ghn-tele.exe --setup          cài đặt nhanh (tự lấy SESSION, mã kho, nhóm)")
	fmt.Println("  ghn-tele.exe --stop           dừng theo dõi đang chạy (cần PIN, báo vào nhóm)")
	fmt.Println("  ghn-tele.exe --ensure         không chạy thì bật lại (Task Scheduler gọi mỗi 5 phút)")
	fmt.Println("  ghn-tele.exe --run-once, -r   gửi báo cáo ngay 1 lần")
	fmt.Println("  ghn-tele.exe --session, -s    lấy SESSION GHN từ Chrome")
	fmt.Println("  ghn-tele.exe --doctor         kiểm tra toàn bộ")
	fmt.Println("  ghn-tele.exe --demo, -d       xem trước tin nhắn")
	fmt.Println("  ghn-tele.exe --install-watch  cài tự chạy khi đăng nhập")
	fmt.Println("  ghn-tele.exe --remove-watch   bỏ tự chạy")
	fmt.Println("  ghn-tele.exe --check-update   kiểm tra bản mới từ kênh đã cấu hình")
	fmt.Println("  ghn-tele.exe --update         kiểm tra và cài bản mới")
	fmt.Println("  ghn-tele.exe --version, -v")
	fmt.Println()
	fmt.Println("Dữ liệu cạnh exe: config.json · seen.json · ghn-tele.log")
}

// pickChats: tự chọn nơi nhận từ danh sách chat bot thấy được.
// Nhóm: lấy nhóm mới nhắn gần nhất (cần hỏi nếu có nhiều nhóm).
// Riêng: lấy chat riêng mới nhất (thường là anh đã nhắn /start cho bot).
func pickChats(list []chatChoice) (group, personal *chatChoice, nGroups, nPriv int) {
	for i := range list {
		c := &list[i]
		if c.chat.isGroup() {
			nGroups++
			if group == nil {
				group = c
			}
		} else {
			nPriv++
			if personal == nil {
				personal = c
			}
		}
	}
	return
}

// wizardSession: lấy SESSION + mã kho từ trình duyệt; thất bại thì cho thử lại / dán tay.
func wizardSession(cfg *Config) {
	fmt.Println("── Bước 1/3: SESSION GHN + mã kho (tự lấy từ Chrome/Edge/Cốc Cốc)")
	for {
		err := tryBrowserSession(cfg, true)
		if err == nil {
			fmt.Printf("✅ Đã lấy SESSION • kho %s\n", cfg.LocationID)
			if exp, ok := jwtExpiredAt(cfg.Session); ok {
				fmt.Printf("   SESSION còn %.0f giờ — app tự gia hạn / lấy lại khi gần hết.\n", time.Until(exp).Hours())
			}
			return
		}
		fmt.Println("⚠️ Chưa lấy được:", err)
		fmt.Println("   → Mở Chrome trên máy này, đăng nhập nhanh.ghn.vn, vào trang KTC – Vận tải, rồi quay lại.")
		switch strings.ToLower(ask("Enter = thử lại • d = dán tay • b = bỏ qua", "")) {
		case "d":
			cfg.Session = ask("SESSION", cfg.Session)
			if cfg.LocationID == "" {
				fmt.Println("Mã kho: F12 → Console → copy(localStorage.getItem('CURRENT_HUB_SORTING'))")
				cfg.LocationID = ask("Mã kho", "")
			}
			return
		case "b":
			return
		}
	}
}

// wizardToken: hỏi Bot Token tới khi Telegram xác nhận đúng.
func wizardToken(cfg *Config) string {
	fmt.Println()
	fmt.Println("── Bước 2/3: Bot Telegram (tạo ở @BotFather → /newbot → copy token)")
	for {
		tok := strings.TrimSpace(ask("Bot Token", cfg.TelegramToken))
		if tok == "" {
			return ""
		}
		if u := tgBotUsername(tok); u != "" {
			cfg.TelegramToken = tok
			fmt.Println("✅ Bot: @" + u)
			return u
		}
		fmt.Println("❌ Token sai hoặc không có mạng — dán lại (Enter trống = bỏ qua).")
	}
}

// wizardChats: quét chat bot nhận được, tự chọn nhóm / chat riêng.
func wizardChats(cfg *Config, bot string) {
	fmt.Println()
	fmt.Println("── Bước 3/3: Nơi nhận tin")
	fmt.Println("   • Nhóm: thêm @" + bot + " vào nhóm, gõ /start trong nhóm")
	fmt.Println("   • Chat riêng (tuỳ chọn): mở @" + bot + ", bấm Start")
	for {
		ask("Làm xong thì Enter để quét", "")
		list, err := scanChats(cfg.TelegramToken)
		if err != nil {
			fmt.Println("❌", err)
		}
		group, personal, nG, nP := pickChats(list)
		if nG > 1 {
			fmt.Println("Thấy nhiều nhóm:")
			var groups []chatChoice
			for _, c := range list {
				if c.chat.isGroup() {
					groups = append(groups, c)
					fmt.Printf("  %d) %s — %s\n", len(groups), c.chat.name(), c.id)
				}
			}
			if n, e := strconv.Atoi(ask("Chọn nhóm", "1")); e == nil && n >= 1 && n <= len(groups) {
				group = &groups[n-1]
			}
		}
		if nP > 1 {
			fmt.Printf("Chat riêng mới nhất: %s — %s\n", personal.chat.name(), personal.id)
		}
		if group != nil {
			cfg.TelegramChat, cfg.TelegramThread = group.id, group.thread
			fmt.Printf("✅ Nhóm: «%s»\n", group.chat.name())
		}
		if personal != nil && askYes(fmt.Sprintf("Báo cả vào chat riêng «%s»? (Y/n)", personal.chat.name()), "Y") {
			cfg.TelegramPersonal = personal.id
			fmt.Printf("✅ Chat riêng: «%s»\n", personal.chat.name())
		}
		if cfg.TelegramChat != "" || cfg.TelegramPersonal != "" {
			switch {
			case cfg.TelegramChat != "" && cfg.TelegramPersonal != "":
				cfg.NotifyMode = "both"
			case cfg.TelegramChat != "":
				cfg.NotifyMode = "group"
			default:
				cfg.NotifyMode = "personal"
			}
			return
		}
		fmt.Println("⚠️ Chưa thấy chat nào. Bot đã ở trong nhóm và đã gõ /start chưa?")
		fmt.Println("   Nhóm vẫn không hiện: @BotFather → /setprivacy → chọn bot → Disable.")
		if strings.ToLower(ask("Enter = quét lại • b = bỏ qua (chọn sau ở mục 4)", "")) == "b" {
			return
		}
	}
}

// wizard: cài đặt gần như tự động — chỉ cần dán Bot Token và bấm Enter.
func wizard() {
	fmt.Println("===== CÀI ĐẶT NHANH =====")
	fmt.Println("Chỉ cần: Chrome đã đăng nhập nhanh.ghn.vn + Bot Token. Còn lại app tự làm.")
	fmt.Println()
	cfg := loadCfg()
	if cfg.Limit <= 0 {
		cfg.Limit = 50
	}
	wizardSession(&cfg)
	_ = saveCfg(cfg)

	bot := wizardToken(&cfg)
	_ = saveCfg(cfg)
	if bot != "" {
		wizardChats(&cfg, bot)
	}
	if cfg.LiveMin == nil {
		cfg.LiveMin = intPtr(5)
	}
	if err := saveCfg(cfg); err != nil {
		fmt.Println("❌ Không lưu được:", err)
		waitEnter()
		return
	}

	fmt.Println()
	fmt.Println("── Thiết lập mặc định (đổi ở mục 1 / mục 8):")
	fmt.Printf("   Quét %d phút (nhanh %s khi có xe sắp tới) • khung %s–%s\n", cfg.liveMin(), minOrOff(cfg.fastMin()), cfg.watchFrom(), cfg.watchTo())
	fmt.Printf("   Ca: %s • báo xe trễ: %s\n", cfg.shiftsRaw(), minOrOff(cfg.lateMin()))

	if !cfgReady(cfg) {
		fmt.Println()
		fmt.Println("⚠️ Còn thiếu:", strings.Join(cfgMissing(cfg), ", "), "— chạy lại mục 1 khi sẵn sàng.")
		waitEnter()
		return
	}
	if err := sendTelegram(cfg, "🧪 <b>Kết nối thành công</b> — kho <code>"+esc(cfg.LocationID)+"</code> sẽ báo vào đây."); err != nil {
		fmt.Println("❌ Gửi tin thử lỗi:", err)
	} else {
		fmt.Println("✅ Đã gửi tin thử — xem Telegram.")
	}
	fmt.Println()
	if askYes("Bật theo dõi ngay + tự chạy mỗi khi đăng nhập Windows? (Y/n)", "Y") {
		autoStart()
	}
	waitEnter()
}

// cfgMissing: những gì còn thiếu để chạy được (để báo rõ).
func cfgMissing(c Config) []string {
	var m []string
	if c.LocationID == "" {
		m = append(m, "mã kho")
	}
	if c.Session == "" && !c.autoSessionOn() {
		m = append(m, "SESSION")
	}
	if c.TelegramToken == "" {
		m = append(m, "Bot Token")
	}
	if c.TelegramChat == "" && c.TelegramPersonal == "" {
		m = append(m, "nơi nhận tin")
	}
	return m
}

// autoStart: cài tự chạy (Windows) + bật chạy nền ngay, không hỏi thêm.
func autoStart() {
	cfg := loadCfg()
	cfg.AutoStart = boolPtr(true)
	_ = saveCfg(cfg)
	if runtime.GOOS == "windows" {
		if out, err := createTasks(exePath(), cfg.autoSessionOn() || updateChannelConfigured()); err != nil {
			fmt.Printf("⚠️ Chưa cài được tự chạy: %v %s\n   Thử lại ở mục 6.\n", err, strings.TrimSpace(out))
		} else {
			fmt.Println("✅ Đã cài tự chạy khi đăng nhập Windows + tự bật lại mỗi 5 phút nếu bị tắt.")
		}
	}
	if running, pid, _ := watchRunning(); running {
		fmt.Printf("✅ Đang chạy sẵn (PID %d).\n", pid)
		return
	}
	if err := startHiddenWatch(); err != nil {
		fmt.Println("❌ Không chạy nền được:", err)
		return
	}
	time.Sleep(2 * time.Second)
	if ok, pid, _ := watchRunning(); ok {
		fmt.Printf("▶️ Đang theo dõi (chạy nền, PID %d). Đóng cửa sổ này vẫn chạy.\n", pid)
	} else {
		fmt.Println("⚠️ Chưa thấy tiến trình — chạy --doctor để xem.")
	}
}

func editCfg() {
	cfg := loadCfg()
	fmt.Println("Enter để giữ nguyên giá trị trong [ ]")
	if askYes("Đổi mã PIN dừng/sửa app? (y/N)", "N") {
		changePIN(&cfg)
	}
	cfg.ManagerName = ask("Tên người quản lý (hiện trong /gioithieu, bấm vào mở Telegram của chủ app)", cfg.ManagerName)
	cfg.Session = ask("SESSION", cfg.Session)
	cfg.LocationID = ask("Mã kho", cfg.LocationID)
	cfg.TelegramToken = ask("Bot Token", cfg.TelegramToken)
	oldChat := cfg.TelegramChat
	cfg.TelegramChat = ask("Chat ID nhóm / @kenh", cfg.TelegramChat)
	if cleanChatID(cfg.TelegramChat) != oldChat {
		cfg.TelegramThread = 0
	}
	cfg.TelegramPersonal = ask("Chat ID riêng (Enter = không dùng)", cfg.TelegramPersonal)
	fmt.Println("Báo vào: 1 nhóm · 2 chat riêng · 3 cả hai")
	cur := "1"
	switch cfg.notifyMode() {
	case "personal":
		cur = "2"
	case "both":
		cur = "3"
	}
	switch ask("Chọn", cur) {
	case "2":
		cfg.NotifyMode = "personal"
	case "3":
		cfg.NotifyMode = "both"
	default:
		cfg.NotifyMode = "group"
	}
	cfg.WatchFrom = ask("Theo dõi từ giờ", cfg.watchFrom())
	cfg.WatchTo = ask("Theo dõi đến giờ", cfg.watchTo())
	if n, err := strconv.Atoi(ask("Quét mỗi (phút)", strconv.Itoa(cfg.liveMin()))); err == nil && n >= 1 && n <= 60 {
		cfg.LiveMin = intPtr(n)
	}
	cfg.Shifts = ask("Danh sách ca ('tắt' = không báo đầu ca)", cfg.shiftsRaw())
	if n, err := strconv.Atoi(ask("Báo xe trễ quá bao nhiêu phút (0 = tắt)", strconv.Itoa(cfg.lateMin()))); err == nil && n >= 0 {
		cfg.LateMin = intPtr(n)
	}
	if n, err := strconv.Atoi(ask("Quét nhanh mỗi (phút) khi có xe sắp tới trong 30' (0 = tắt)", strconv.Itoa(cfg.fastMin()))); err == nil && n >= 0 {
		cfg.FastMin = intPtr(n)
	}
	if n, err := strconv.Atoi(ask("Báo xe trong kho quá bao nhiêu phút chưa bàn giao xong (0 = tắt)", strconv.Itoa(cfg.handoverMin()))); err == nil && n >= 0 {
		cfg.HandoverMin = intPtr(n)
	}
	if err := saveCfg(cfg); err != nil {
		fmt.Println("❌ Lỗi lưu:", err)
	} else {
		fmt.Println("✅ Đã lưu.")
	}
	waitEnter()
}

func menuWindow() {
	cfg := loadCfg()
	fmt.Println("Ngoài khung này app nghỉ, không gọi API. Cả ngày: 00:00 và 23:59.")
	from := ask("Theo dõi TỪ giờ", cfg.watchFrom())
	to := ask("Theo dõi ĐẾN giờ", cfg.watchTo())
	if hhmmToMin(from) < 0 || hhmmToMin(to) < 0 {
		fmt.Println("❌ Giờ không hợp lệ (dạng HH:MM).")
		waitEnter()
		return
	}
	fmt.Println("Ca: mỗi ca bắt đầu sẽ gửi tóm tắt xe sắp đến (cách nhau dấu phẩy). Gõ 'tắt' để bỏ tin đầu ca.")
	shifts := ask("Danh sách ca", cfg.shiftsRaw())
	if !(Config{Shifts: shifts}).shiftsOff() && len(parseShifts(shifts)) == 0 {
		fmt.Println("❌ Danh sách ca không hợp lệ, giữ ca cũ.")
		waitEnter()
		return
	}
	cfg.WatchFrom = from
	cfg.WatchTo = to
	cfg.Shifts = shifts
	if err := saveCfg(cfg); err != nil {
		fmt.Println("❌ Lỗi lưu:", err)
	} else {
		fmt.Printf("✅ Khung %s–%s • ca %s\n", cfg.watchFrom(), cfg.watchTo(), cfg.shiftsRaw())
		fmt.Println("Đang chạy nền thì tự áp dụng ở vòng quét sau, không cần bật lại.")
	}
	waitEnter()
}

func showStatus() {
	cfg := loadCfg()
	fmt.Println("Config:", cfgPath)
	fmt.Println("Log   :", logPath)
	running, pid, since := watchRunning()
	if running {
		fmt.Printf("Theo dõi: 🟢 ĐANG CHẠY (PID %d, từ %s)\n", pid, since)
	} else {
		fmt.Println("Theo dõi: 🔴 CHƯA CHẠY (vào mục 5)")
	}
	if t := lastScanTime(); !t.IsZero() {
		fmt.Printf("Lần quét cuối: %s\n", t.Format("02/01 15:04:05"))
	}
	if exp, ok := jwtExpiredAt(cfg.Session); ok {
		fmt.Printf("SESSION: còn %.1f giờ (hết hạn %s)\n", time.Until(exp).Hours(), exp.Local().Format("02/01 15:04"))
	} else {
		fmt.Println("SESSION: không đọc được hạn")
	}
	fmt.Printf("Kho: %s • Nhận tin: %s\n", cfg.LocationID, cfg.groupLabel())
	fmt.Printf("  nhóm: %s • riêng: %s\n", orDash(cfg.TelegramChat), orDash(cfg.TelegramPersonal))
	fmt.Printf("Quét %d phút (nhanh %s) • khung %s–%s\n", cfg.liveMin(), minOrOff(cfg.fastMin()), cfg.watchFrom(), cfg.watchTo())
	fmt.Printf("Báo xe trễ: %s\n", minOrOff(cfg.lateMin()))
	fmt.Printf("Ca: %s\n", cfg.shiftsRaw())
	if lastErr != "" {
		fmt.Println("Lỗi gần nhất:", lastErr)
	}
	fmt.Println("Chi tiết đầy đủ: ghn-tele.exe --doctor")
	waitEnter()
}

func testGHN(c Config) {
	if err := ensureSession(&c); err != nil {
		fmt.Println("❌ SESSION:", err)
		waitEnter()
		return
	}
	arr, err := fetchArrived(c)
	if err != nil {
		fmt.Println("❌ Đã đến kho:", err)
		waitEnter()
		return
	}
	fmt.Printf("✅ Đã đến kho: %d chuyến\n", len(arr.list()))
	for i, it := range arr.list() {
		if i >= 3 {
			break
		}
		fmt.Printf("  %s | %s | %s | %s | %d kiện\n",
			it.PartnerTransportationCode, it.RouteScheduleName, it.VehiclePlate,
			it.DriverName, totalPkg(it.WillDropOff))
	}
	if c.upcomingOn() {
		up, err := fetchUpcoming(c)
		if err != nil {
			fmt.Println("❌ Sắp đến kho:", err)
		} else {
			fmt.Printf("✅ Sắp đến kho: %d chuyến\n", len(up.list()))
			for i, it := range up.list() {
				if i >= 3 {
					break
				}
				fmt.Printf("  %s | %s | %s | %s | %d kiện\n",
					it.PartnerTransportationCode, it.RouteScheduleName, it.VehiclePlate,
					it.DriverName, totalPkg(it.WillDropOff))
			}
		}
	}
	waitEnter()
}

func reportUpcoming(up *ArrivedResp) string {
	var b strings.Builder
	b.WriteString("🔵 <b>SẮP ĐẾN KHO</b>\n")
	list := up.list()
	if len(list) == 0 {
		b.WriteString("Không có chuyến nào.\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("Số chuyến: <b>%d</b>\n\n", len(list)))
	for i, it := range list {
		if i >= maxRows {
			b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(list)-i))
			break
		}
		b.WriteString("• " + fmtTimeShort(it.ExpectedArrivalTime) + " <b>" + esc(it.PartnerTransportationCode) + "</b>")
		if it.VehiclePlate != "" {
			b.WriteString(" • " + esc(it.VehiclePlate))
		}
		b.WriteString(fmt.Sprintf(" • %d kiện\n", totalPkg(it.WillDropOff)))
	}
	return b.String()
}

func reportArrived(arr *ArrivedResp) string {
	var b strings.Builder
	b.WriteString("🟠 <b>ĐÃ ĐẾN KHO</b>\n")
	list := arr.list()
	if len(list) == 0 {
		b.WriteString("Không có chuyến nào.\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("Số chuyến: <b>%d</b>\n\n", len(list)))
	for i, it := range list {
		if i >= maxRows {
			b.WriteString(fmt.Sprintf("… và %d chuyến khác\n", len(list)-i))
			break
		}
		b.WriteString("• " + fmtTimeShort(it.ArrivalTime) + " <b>" + esc(it.PartnerTransportationCode) + "</b>")
		if it.VehiclePlate != "" {
			b.WriteString(" • " + esc(it.VehiclePlate))
		}
		b.WriteString(fmt.Sprintf(" • %d kiện", totalPkg(it.WillDropOff)))
		if st := statusVi(it.StopSessionStatus); st != "-" {
			b.WriteString(" • " + esc(st))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func sendReportNow(cfg Config) error {
	nUp := 0
	if cfg.upcomingOn() {
		up, err := fetchUpcoming(cfg)
		if err != nil {
			lastErr = err.Error()
			logf("[report] Lỗi sắp đến: %v", err)
			return err
		}
		nUp = len(up.list())
		if err := sendTelegram(cfg, reportUpcoming(up)); err != nil {
			return err
		}
		time.Sleep(tgGap)
	}
	arr, err := fetchArrived(cfg)
	if err != nil {
		lastErr = err.Error()
		logf("[report] Lỗi đã đến: %v", err)
		return err
	}
	if err := sendTelegram(cfg, reportArrived(arr)); err != nil {
		return err
	}
	lastErr = ""
	logf("[report] Đã gửi báo cáo: %d sắp đến, %d đã đến", nUp, len(arr.list()))
	return nil
}

func menu() {
	for {
		cfg := loadCfg()
		fmt.Println()
		fmt.Println("==============================================")
		fmt.Println("  " + appTitle + "  v" + version)
		fmt.Println("==============================================")
		if cfgReady(cfg) {
			runTxt := "CHƯA CHẠY"
			if running, pid, _ := watchRunning(); running {
				runTxt = fmt.Sprintf("ĐANG CHẠY (PID %d)", pid)
				if cfg.Paused {
					runTxt += " — ⏸ TẠM DỪNG BÁO (/bat để bật)"
				}
			}
			scan := "chưa quét"
			if t := lastScanTime(); !t.IsZero() {
				scan = t.Format("15:04:05")
			}
			fmt.Printf("  Kho %s → %s\n", cfg.LocationID, cfg.groupLabel())
			fmt.Printf("  Theo dõi: %s • quét %d phút • lần cuối %s\n", runTxt, cfg.liveMin(), scan)
			fmt.Printf("  Khung %s–%s • ca %s\n", cfg.watchFrom(), cfg.watchTo(), cfg.shiftsRaw())
		} else {
			fmt.Println("  Chưa đủ cấu hình — chọn 1 để cài")
		}
		fmt.Println("----------------------------------------------")
		fmt.Println("  1. Cài đặt nhanh / sửa cấu hình")
		fmt.Println("  2. Gửi tin thử Telegram")
		fmt.Println("  3. Test lấy dữ liệu GHN")
		fmt.Println("  4. Chọn nhóm / chat nhận tin")
		fmt.Println("  5. Chạy / dừng theo dõi")
		fmt.Println("  6. Tự chạy + tự bật lại khi bị tắt")
		fmt.Println("  7. Lấy SESSION từ Chrome")
		fmt.Println("  8. Khung giờ theo dõi + danh sách ca")
		fmt.Println("  9. Xem trạng thái")
		fmt.Println(" 10. Demo tin nhắn Telegram")
		fmt.Println(" 11. Tạo phím tắt dừng Ctrl+Alt+S")
		fmt.Println(" 12. Cập nhật phần mềm")
		fmt.Println("  0. Thoát")
		fmt.Print("Chọn: ")
		line, rerr := stdin.ReadString('\n')
		if rerr != nil && strings.TrimSpace(line) == "" {
			return
		}
		switch strings.TrimSpace(line) {
		case "1":
			switch {
			case !cfgReady(loadCfg()):
				wizard() // lần cài đầu: chưa cần PIN
			case !requirePIN("sửa cấu hình"):
			case ask("1 = sửa từng mục • 2 = cài nhanh lại (tự lấy lại SESSION, kho, nhóm)", "1") != "2":
				editCfg()
			default:
				wizard()
			}
		case "2":
			testTelegram(loadCfg())
		case "3":
			testGHN(loadCfg())
		case "4":
			if requirePIN("đổi nơi nhận tin") {
				findChatID()
			}
		case "5":
			menuLive()
		case "6":
			if requirePIN("đổi tự chạy") {
				menuAutoStart()
			}
		case "7":
			menuSession()
		case "8":
			if requirePIN("đổi khung giờ") {
				menuWindow()
			}
		case "9":
			showStatus()
		case "10":
			runDemo()
		case "11":
			makeStopShortcut()
		case "12":
			if updateOptions() {
				return
			}
			waitEnter()
		case "0", "q", "Q":
			return
		default:
			fmt.Println("Chọn số trong menu.")
		}
	}
}

func minOrOff(n int) string {
	if n <= 0 {
		return "tắt"
	}
	return fmt.Sprintf("%d phút", n)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
