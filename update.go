package main

// Tự cập nhật qua GitHub Releases.
//
//   Bản phát hành = 2 tệp trên GitHub Releases: ghn-tele.exe + update.json.
//   update.json có chữ ký ed25519; khoá công khai gắn cứng trong exe (updatePubKey),
//   khoá bí mật do chủ app giữ, KHÔNG để trên GitHub → chiếm được GitHub cũng
//   không đẩy được bản giả xuống máy kho.
//
//   Vòng đời: kiểm tra mỗi giờ → tải + kiểm SHA-256 + chữ ký → bản mới tự kiểm tra
//   (--selftest) → chỉ cài NGOÀI khung giờ theo dõi (hoặc chủ app gõ /capnhat ngay)
//   → đổi tên exe đang chạy thành .previous, đặt bản mới vào đúng chỗ → bản mới
//   chạy tiếp vòng theo dõi → báo ✅. Bản mới bị tắt ngang ≥2 lần trước lần quét
//   thành công đầu tiên → tự quay về .previous và báo ⚠️.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// updatePubKey: khoá công khai ký bản phát hành (khoá bí mật: ghn-tele-update-key.secret, chủ app giữ).
var updatePubKey = "1kJod0de0yJ3CiQ/3oesPFQgwmAKxApSgJ9sNNVOUlM="

const (
	defaultUpdateRepo = "vietnhat111/GHN-Tele"
	updateAssetExe    = "ghn-tele.exe"
	updateAssetJSON   = "update.json"
	updateMaxExe      = 64 << 20
	updateCheckEvery  = time.Hour
	updateLeadBefore  = 15 * time.Minute // không cài khi còn < 15' là tới giờ theo dõi
	probationCrashes  = 2
)

var updateBaseURL = "https://github.com" // test trỏ sang server giả

// ---------------------------------------------------------------- cấu hình

func (c Config) updateRepo() string {
	if r := strings.Trim(strings.TrimSpace(c.UpdateRepo), "/"); r != "" {
		return r
	}
	return defaultUpdateRepo
}

func (c Config) autoUpdateOn() bool { return c.AutoUpdate == nil || *c.AutoUpdate }

// ---------------------------------------------------------------- manifest + chữ ký

type updateManifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes"`
	Sig     string `json:"sig"`
}

// payload: nội dung được ký — đủ mọi trường quyết định tải gì và hiện gì.
func (m updateManifest) payload() []byte {
	return []byte("ghn-tele-update-v1\n" + m.Version + "\n" + strconv.FormatInt(m.Size, 10) + "\n" +
		strings.ToLower(m.SHA256) + "\n" + m.URL + "\n" + m.Notes)
}

func verifyManifest(m updateManifest, pubB64 string) error {
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("khoá công khai cập nhật không hợp lệ")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Sig)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), m.payload(), sig) {
		return errors.New("chữ ký bản cập nhật KHÔNG hợp lệ — bỏ qua")
	}
	if _, err := semver(m.Version); err != nil {
		return err
	}
	if !strings.HasPrefix(m.URL, "https://") || m.Size <= 0 || m.Size > updateMaxExe || len(m.SHA256) != 64 {
		return errors.New("thông tin bản cập nhật không hợp lệ")
	}
	return nil
}

func semver(v string) ([3]int, error) {
	var out [3]int
	p := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(p) != 3 {
		return out, fmt.Errorf("phiên bản lạ: %q", v)
	}
	for i := range p {
		n, err := strconv.Atoi(p[i])
		if err != nil || n < 0 {
			return out, fmt.Errorf("phiên bản lạ: %q", v)
		}
		out[i] = n
	}
	return out, nil
}

// isNewer: a mới hơn b?
func isNewer(a, b string) bool {
	x, e1 := semver(a)
	y, e2 := semver(b)
	if e1 != nil || e2 != nil {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

var updateHTTP = &http.Client{Timeout: 10 * time.Minute} // GitHub chuyển hướng sang máy chủ tệp: cho theo

func httpGetLimited(url string, limit int64) ([]byte, error) {
	resp, err := updateHTTP.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errNoRelease
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tải %s: http %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("tệp tải về quá lớn")
	}
	return b, err
}

var errNoRelease = errors.New("chưa có bản phát hành nào")

func fetchManifest(cfg Config) (updateManifest, error) {
	var m updateManifest
	b, err := httpGetLimited(updateBaseURL+"/"+cfg.updateRepo()+"/releases/latest/download/"+updateAssetJSON, 64<<10)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, errors.New("update.json không đọc được")
	}
	return m, verifyManifest(m, updatePubKey)
}

// ---------------------------------------------------------------- trạng thái (file)

type updateState struct {
	From       string   `json:"from"`
	To         string   `json:"to"`
	Notes      string   `json:"notes"`
	At         int64    `json:"at"`
	Crashes    int      `json:"crashes"`
	Confirmed  bool     `json:"confirmed"`  // bản mới đã quét thành công 1 lần
	Announced  bool     `json:"announced"`  // đã báo ✅
	RolledBack bool     `json:"rolledBack"` // đã quay về bản cũ
	RBNotified bool     `json:"rbNotified"`
	Skip       []string `json:"skip"` // bản bỏ qua (chủ app chọn / bản lỗi)
}

func updateStatePath() string { return filepath.Join(exeDir, "update-state.json") }

func loadUpdateState() updateState {
	var st updateState
	if b, err := os.ReadFile(updateStatePath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveUpdateState(st updateState) {
	b, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(updateStatePath(), b, 0600)
}

func (st updateState) skipped(v string) bool {
	for _, s := range st.Skip {
		if s == v {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- bộ cập nhật (trong tiến trình theo dõi)

type updater struct {
	mu        sync.Mutex
	latest    updateManifest // bản mới nhất thấy trên GitHub
	checkedAt time.Time
	lastErr   string
	prepared  string // đường dẫn exe mới đã tải + kiểm tra, sẵn sàng cài
	prepVer   string
	notified  string // đã báo chủ app về bản này
	force     bool   // /capnhat ngay
	checkNow  chan struct{}
}

var upd = &updater{checkNow: make(chan struct{}, 1)}

func (u *updater) snapshot() (updateManifest, time.Time, string, string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.latest, u.checkedAt, u.lastErr, u.prepVer, u.force
}

func (u *updater) requestNow(force bool) {
	u.mu.Lock()
	if force {
		u.force = true
	}
	u.mu.Unlock()
	select {
	case u.checkNow <- struct{}{}:
	default:
	}
}

// startUpdater: kiểm tra định kỳ; tải và chuẩn bị bản mới ở nền.
func startUpdater(stop <-chan struct{}) {
	go func() {
		t := time.NewTimer(2 * time.Minute) // lần đầu sau khi bật 2 phút
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			case <-upd.checkNow:
			}
			upd.checkOnce(loadCfg())
			t.Reset(updateCheckEvery)
		}
	}()
}

func (u *updater) checkOnce(cfg Config) {
	if forceFlagTaken() {
		u.mu.Lock()
		u.force = true
		u.mu.Unlock()
	}
	m, err := fetchManifest(cfg)
	u.mu.Lock()
	u.checkedAt = time.Now()
	if err != nil {
		if errors.Is(err, errNoRelease) {
			u.lastErr = ""
		} else {
			u.lastErr = err.Error()
			logf("[update] Kiểm tra lỗi: %v", err)
		}
		u.mu.Unlock()
		return
	}
	u.latest, u.lastErr = m, ""
	already := u.prepVer == m.Version
	u.mu.Unlock()

	st := loadUpdateState()
	if !isNewer(m.Version, version) || st.skipped(m.Version) {
		u.mu.Lock()
		u.force = false // không có gì để cài → bỏ "cài ngay", tránh bản sau bị cài giữa giờ làm
		u.mu.Unlock()
		return
	}
	if already {
		return
	}
	if !cfg.autoUpdateOn() && !u.isForced() {
		u.notifyOnce(cfg, m, "Tự cập nhật đang TẮT — gõ <code>/capnhat ngay</code> để cài.")
		return
	}
	path, err := prepareUpdate(m)
	if err != nil {
		u.mu.Lock()
		u.lastErr = err.Error()
		u.mu.Unlock()
		logf("[update] Chuẩn bị v%s lỗi: %v", m.Version, err)
		alert(cfg, "update", "⚠️ <b>Chưa cài được bản v"+esc(m.Version)+"</b>\n"+esc(truncate(err.Error(), 200))+"\nBản hiện tại vẫn chạy bình thường; 1 giờ sau thử lại.")
		return
	}
	u.mu.Lock()
	u.prepared, u.prepVer = path, m.Version
	u.mu.Unlock()
	logf("[update] Đã tải + kiểm tra v%s, chờ giờ cài", m.Version)
	u.notifyOnce(cfg, m, "Sẽ tự cài lúc "+nextInstallText(cfg, time.Now())+" (ngoài giờ theo dõi).\n"+
		"<code>/capnhat ngay</code> cài luôn · <code>/capnhat bo</code> bỏ qua bản này")
}

func (u *updater) isForced() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.force
}

// notifyOnce: nhắn riêng chủ app (không có chat riêng thì vào nhóm, im lặng) — 1 lần mỗi bản.
func (u *updater) notifyOnce(cfg Config, m updateManifest, tail string) {
	u.mu.Lock()
	if u.notified == m.Version {
		u.mu.Unlock()
		return
	}
	u.notified = m.Version
	u.mu.Unlock()
	msg := "⬆️ <b>Có bản mới v" + esc(m.Version) + "</b> (đang dùng v" + version + ")\n"
	if n := strings.TrimSpace(m.Notes); n != "" {
		msg += esc(truncate(n, 600)) + "\n"
	}
	msg += tail
	t := chatTarget{chat: cfg.TelegramPersonal}
	if t.chat == "" {
		enqueue(quiet(msg))
		return
	}
	_ = sendTelegramAt(cfg, t, msg, true)
}

// prepareUpdate: tải exe, kiểm dung lượng + SHA-256, cho bản mới tự kiểm tra.
func prepareUpdate(m updateManifest) (string, error) {
	b, err := httpGetLimited(m.URL, updateMaxExe)
	if err != nil {
		return "", err
	}
	if int64(len(b)) != m.Size {
		return "", fmt.Errorf("dung lượng tải về sai (%d ≠ %d)", len(b), m.Size)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != strings.ToLower(m.SHA256) {
		return "", errors.New("mã kiểm tra SHA-256 không khớp — tệp hỏng hoặc bị sửa")
	}
	dir := filepath.Join(exeDir, "update-tmp")
	_ = os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "ghn-tele-"+m.Version+exeSuffix())
	if err := os.WriteFile(path, b, 0700); err != nil {
		return "", err
	}
	if err := runSelfTest(path, m.Version); err != nil {
		return "", fmt.Errorf("bản mới không qua tự kiểm tra: %w", err)
	}
	return path, nil
}

func exeSuffix() string {
	if strings.HasSuffix(strings.ToLower(exePath()), ".exe") {
		return ".exe"
	}
	return ""
}

func runSelfTest(path, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--selftest", exeDir)
	detach(cmd)
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, "SELFTEST OK v"+want) {
		if err == nil {
			err = errors.New("không báo OK")
		}
		return fmt.Errorf("%v: %s", err, truncate(lastLine(s), 160))
	}
	return nil
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// cmdSelfTest: bản mới tự kiểm tra với dữ liệu thật của máy kho — chỉ đọc, không gửi tin, không ghi tệp.
func cmdSelfTest(dir string) {
	exeDir, cfgPath = dir, filepath.Join(dir, cfgName)
	logPath = filepath.Join(os.TempDir(), "ghn-tele-selftest.log")
	fail := func(what string) {
		fmt.Println("SELFTEST FAIL:", what)
		os.Exit(1)
	}
	cfg := loadCfg()
	if !cfgReady(cfg) {
		fail("không đọc được cấu hình")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "seen.json")); err == nil {
		var s SeenStore
		if json.Unmarshal(b, &s) != nil {
			fail("không đọc được seen.json")
		}
	}
	if tgBotUsername(cfg.TelegramToken) == "" {
		fail("không gọi được Telegram")
	}
	fmt.Println("SELFTEST OK v" + version)
}

// ---------------------------------------------------------------- cài đặt

// installDue: đã có bản chuẩn bị và tới lúc cài chưa.
func installDue(cfg Config, now time.Time, forced bool) bool {
	if forced {
		return true
	}
	if !cfg.autoUpdateOn() {
		return false
	}
	return installWindow(cfg, now)
}

// installWindow: ngoài khung giờ theo dõi và còn ≥ 15' mới tới giờ bắt đầu.
// Khung cả ngày → cài lúc 01:00–04:00 (ít xe nhất).
func installWindow(cfg Config, now time.Time) bool {
	if fullDay(cfg) {
		h := now.Hour()
		return h >= 1 && h < 4
	}
	if inWatchWindow(now, cfg) {
		return false
	}
	return !inWatchWindow(now.Add(updateLeadBefore), cfg)
}

// nextInstallText: giờ cài dự kiến cho tin báo.
func nextInstallText(cfg Config, now time.Time) string {
	for i := 0; i < 48*12; i++ {
		t := now.Add(time.Duration(i*5) * time.Minute)
		if installWindow(cfg, t) {
			if t.YearDay() == now.YearDay() {
				return t.Format("15:04") + " hôm nay"
			}
			return t.Format("15:04") + " ngày " + t.Format("02/01")
		}
	}
	return "khi ngoài giờ theo dõi"
}

// takePrepared: lấy bản đã chuẩn bị nếu tới lúc cài.
func (u *updater) takePrepared(cfg Config, now time.Time) (string, string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.prepared == "" || !installDue(cfg, now, u.force) {
		return "", "", false
	}
	p, v := u.prepared, u.prepVer
	u.prepared, u.prepVer, u.force = "", "", false
	return p, v, true
}

// installUpdate: đổi exe đang chạy sang .previous, đặt bản mới vào đúng chỗ, chạy bản mới.
// Windows cho đổi tên exe đang chạy (không cho xoá/ghi đè) → cách này an toàn.
func installUpdate(newPath, newVer, notes string) error {
	cur := exePath()
	prev := cur + ".previous"
	_ = os.Remove(prev)
	if err := os.Rename(cur, prev); err != nil {
		return fmt.Errorf("không đổi tên exe hiện tại: %w", err)
	}
	if err := os.Rename(newPath, cur); err != nil {
		_ = os.Rename(prev, cur) // trả lại như cũ
		return fmt.Errorf("không đặt được exe mới: %w", err)
	}
	st := loadUpdateState()
	st.From, st.To, st.Notes, st.At = version, newVer, notes, time.Now().UnixMilli()
	st.Crashes, st.Confirmed, st.Announced, st.RolledBack, st.RBNotified = 0, false, false, false, false
	saveUpdateState(st)
	cmd := exec.Command(cur, "--finish-update", strconv.Itoa(os.Getpid()))
	detach(cmd)
	if err := cmd.Start(); err != nil {
		// không chạy được bản mới → quay lại bản cũ ngay
		_ = os.Rename(cur, cur+".bad")
		_ = os.Rename(prev, cur)
		return fmt.Errorf("không chạy được bản mới: %w", err)
	}
	logf("[update] Đã cài v%s (từ v%s), chuyển sang bản mới", newVer, version)
	return nil
}

// cmdFinishUpdate: bản mới chờ bản cũ thoát rồi nhận vòng theo dõi.
func cmdFinishUpdate(oldPID int) {
	for i := 0; i < 60 && pidAlive(oldPID); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	_ = os.RemoveAll(filepath.Join(exeDir, "update-tmp"))
	clearStopIntent()
	if running, _, _ := watchRunning(); running {
		return
	}
	watch()
}

// ---------------------------------------------------------------- thử việc + quay về

// updateOnStart: gọi đầu watch(). Trả về tin cần báo, và true nếu đã quay về bản cũ
// (tiến trình này phải thoát để bản cũ chạy).
func updateOnStart(cfg Config, reason string) ([]outMsg, bool) {
	st := loadUpdateState()
	var msgs []outMsg
	if st.RolledBack && !st.RBNotified && st.From == version {
		st.RBNotified = true
		saveUpdateState(st)
		msgs = append(msgs, loud("⚠️ <b>Bản v"+esc(st.To)+" chạy lỗi — đã tự quay về v"+version+"</b>\n"+
			"Bản v"+esc(st.To)+" sẽ không tự cài lại. Liên hệ: "+managerLink(cfg)))
		return msgs, false
	}
	if st.To != version || st.Confirmed {
		return nil, false
	}
	if reason == "killed" {
		st.Crashes++
		saveUpdateState(st)
		if st.Crashes >= probationCrashes && rollback(&st) {
			return nil, true
		}
	}
	if !st.Announced {
		st.Announced = true
		saveUpdateState(st)
		txt := "✅ <b>Đã cập nhật lên v" + version + "</b> (từ v" + esc(st.From) + ")"
		if n := strings.TrimSpace(st.Notes); n != "" {
			txt += "\n" + esc(truncate(n, 600))
		}
		msgs = append(msgs, quiet(txt))
	}
	return msgs, false
}

// confirmUpdate: lần quét thành công đầu tiên của bản mới → hết thử việc.
func confirmUpdate() {
	st := loadUpdateState()
	if st.To == version && !st.Confirmed {
		st.Confirmed = true
		saveUpdateState(st)
		logf("[update] v%s chạy ổn — hết thử việc", version)
	}
}

// rollback: đặt lại .previous và chạy nó. true = đã chuyển, tiến trình này nên thoát.
func rollback(st *updateState) bool {
	cur := exePath()
	prev := cur + ".previous"
	if !fileExists(prev) {
		logf("[update] Muốn quay về nhưng không có %s", filepath.Base(prev))
		return false
	}
	bad := cur + ".bad"
	_ = os.Remove(bad)
	if os.Rename(cur, bad) != nil {
		return false
	}
	if os.Rename(prev, cur) != nil {
		_ = os.Rename(bad, cur)
		return false
	}
	st.RolledBack = true
	if !st.skipped(st.To) {
		st.Skip = append(st.Skip, st.To)
	}
	saveUpdateState(*st)
	logf("[update] v%s bị tắt ngang %d lần → quay về v%s", st.To, st.Crashes, st.From)
	clearRunning()
	cmd := exec.Command(cur, "--finish-update", strconv.Itoa(os.Getpid()))
	detach(cmd)
	_ = cmd.Start()
	return true
}

// ---------------------------------------------------------------- lệnh /capnhat

func updateCommand(cfg Config, args []string, now time.Time) (string, string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	latest, checked, lastErr, prepVer, forced := upd.snapshot()
	switch sub {
	case "ngay":
		upd.requestNow(true)
		if prepVer != "" {
			return "⏳ Cài v" + esc(prepVer) + " trong vòng 5 phút — app sẽ báo ✅ khi xong.", ""
		}
		return "⏳ Đang kiểm tra GitHub; có bản mới sẽ tải, tự kiểm tra rồi cài luôn.", ""
	case "bo":
		if latest.Version == "" || !isNewer(latest.Version, version) {
			return "Không có bản mới nào để bỏ qua.", ""
		}
		st := loadUpdateState()
		if !st.skipped(latest.Version) {
			st.Skip = append(st.Skip, latest.Version)
			saveUpdateState(st)
		}
		upd.mu.Lock()
		if upd.prepVer == latest.Version {
			upd.prepared, upd.prepVer = "", ""
		}
		upd.mu.Unlock()
		return "⏭ Bỏ qua v" + esc(latest.Version) + ". Bản sau mới hơn vẫn tự cài.", ""
	case "tat":
		cfg.AutoUpdate = boolPtr(false)
		_ = saveCfg(cfg)
		return "⏸ Đã tắt tự cập nhật. Có bản mới bot vẫn báo; muốn cài gõ /capnhat ngay.",
			"⚙️ Tắt tự cập nhật app."
	case "bat":
		cfg.AutoUpdate = boolPtr(true)
		_ = saveCfg(cfg)
		upd.requestNow(false)
		return "▶️ Đã bật tự cập nhật (cài ngoài giờ theo dõi).", "⚙️ Bật tự cập nhật app."
	}
	var b strings.Builder
	b.WriteString("⬆️ <b>CẬP NHẬT APP</b>\n")
	b.WriteString("📦 Đang dùng: v" + version + "\n")
	switch {
	case lastErr != "":
		b.WriteString("⚠️ Lần kiểm tra cuối lỗi: " + esc(truncate(lastErr, 150)) + "\n")
	case latest.Version == "":
		b.WriteString("🔎 Chưa thấy bản phát hành nào trên GitHub\n")
	case isNewer(latest.Version, version):
		b.WriteString("🆕 Bản mới: v" + esc(latest.Version))
		if prepVer == latest.Version {
			b.WriteString(" — đã tải + kiểm tra, cài lúc " + nextInstallText(cfg, now))
		}
		b.WriteString("\n")
	default:
		b.WriteString("✅ Đang là bản mới nhất\n")
	}
	if !checked.IsZero() {
		b.WriteString("🕐 Kiểm tra lúc: " + checked.Format("15:04") + " (mỗi giờ)\n")
	}
	if cfg.autoUpdateOn() {
		b.WriteString("🔄 Tự cập nhật: BẬT (ngoài giờ theo dõi)")
	} else {
		b.WriteString("🔄 Tự cập nhật: TẮT")
	}
	if forced {
		b.WriteString("\n⏳ Đang chờ cài ngay")
	}
	b.WriteString("\n\n/capnhat ngay · /capnhat bo · /capnhat tat · /capnhat bat")
	return b.String(), ""
}

// --update trên máy kho: nhờ tiến trình đang chạy cài ngay (qua tệp cờ).
func forceFlagPath() string { return filepath.Join(exeDir, "update.force") }

func forceFlagTaken() bool {
	if fileExists(forceFlagPath()) {
		_ = os.Remove(forceFlagPath())
		return true
	}
	return false
}

func cmdUpdateNow() {
	cfg := loadCfg()
	fmt.Println("Đang kiểm tra bản mới trên GitHub (" + cfg.updateRepo() + ")...")
	m, err := fetchManifest(cfg)
	switch {
	case errors.Is(err, errNoRelease):
		fmt.Println("Chưa có bản phát hành nào.")
		return
	case err != nil:
		fmt.Println("❌", err)
		return
	case !isNewer(m.Version, version):
		fmt.Printf("✅ Đang là bản mới nhất (v%s).\n", version)
		return
	}
	fmt.Printf("🆕 Có v%s (đang dùng v%s).\n", m.Version, version)
	if running, _, _ := watchRunning(); running {
		_ = os.WriteFile(forceFlagPath(), []byte("1"), 0600)
		fmt.Println("App đang chạy nền — sẽ tự tải, kiểm tra và cài trong vòng 1 giờ (thường vài phút).")
		fmt.Println("Muốn nhanh hơn: nhắn bot /capnhat ngay.")
		return
	}
	path, err := prepareUpdate(m)
	if err != nil {
		fmt.Println("❌", err)
		return
	}
	cur := exePath()
	prev := cur + ".previous"
	_ = os.Remove(prev)
	if err := os.Rename(cur, prev); err != nil {
		fmt.Println("❌", err)
		return
	}
	if err := os.Rename(path, cur); err != nil {
		_ = os.Rename(prev, cur)
		fmt.Println("❌", err)
		return
	}
	st := loadUpdateState()
	st.From, st.To, st.Notes, st.At = version, m.Version, m.Notes, time.Now().UnixMilli()
	st.Crashes, st.Confirmed, st.Announced, st.RolledBack, st.RBNotified = 0, false, false, false, false
	saveUpdateState(st)
	fmt.Printf("✅ Đã cài v%s. Mở lại exe để dùng bản mới.\n", m.Version)
}

// ---------------------------------------------------------------- công cụ phát hành (máy người phát hành)

// --sign-update <khoá-bí-mật> <exe> <phiên-bản> [ghi chú...] → update.json cạnh exe.
func cmdSignUpdate(args []string) {
	if len(args) < 3 {
		fmt.Println("Dùng: ghn-tele --sign-update <khoá.secret> <ghn-tele.exe> <2.15.0> [ghi chú]")
		os.Exit(2)
	}
	keyB, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Println("❌", err)
		os.Exit(1)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyB)))
	if err != nil || len(seed) != ed25519.SeedSize {
		fmt.Println("❌ khoá bí mật không hợp lệ")
		os.Exit(1)
	}
	exe, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Println("❌", err)
		os.Exit(1)
	}
	ver := strings.TrimPrefix(args[2], "v")
	if _, err := semver(ver); err != nil {
		fmt.Println("❌", err)
		os.Exit(1)
	}
	sum := sha256.Sum256(exe)
	repo := os.Getenv("GHN_UPDATE_REPO")
	if repo == "" {
		repo = defaultUpdateRepo
	}
	m := updateManifest{
		Version: ver,
		URL:     "https://github.com/" + repo + "/releases/download/v" + ver + "/" + updateAssetExe,
		Size:    int64(len(exe)),
		SHA256:  hex.EncodeToString(sum[:]),
		Notes:   strings.Join(args[3:], " "),
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if pubB64 := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)); pubB64 != updatePubKey && os.Getenv("GHN_ALLOW_OTHER_KEY") != "1" {
		fmt.Println("❌ Khoá bí mật này KHÔNG khớp khoá công khai trong app — máy kho sẽ không cài được bản ký bằng nó.")
		fmt.Println("   Khoá công khai của khoá này:", pubB64)
		os.Exit(1)
	}
	m.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m.payload()))
	if err := verifyManifest(m, base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))); err != nil {
		fmt.Println("❌", err)
		os.Exit(1)
	}
	out := filepath.Join(filepath.Dir(args[1]), updateAssetJSON)
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(out, b, 0644); err != nil {
		fmt.Println("❌", err)
		os.Exit(1)
	}
	fmt.Println("✅", out)
}
