package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	updateDirPrefix = ".ghn-update-"
	updateLock      = "update.lock"
	updateResult    = "update-result.txt"
	updateCandidate = "candidate.exe"
)

func updateAccount() string {
	u, err := osuser.Current()
	if err != nil {
		return ""
	}
	return u.Uid
}

func allowUpdateAccount() error {
	account := updateAccount()
	switch account {
	case "", "S-1-5-18", "S-1-5-19", "S-1-5-20":
		return errors.New("hãy chạy app bằng tài khoản Windows của anh để cài cập nhật")
	default:
		return nil
	}
}

func openUpdateLock(create bool) (*os.File, bool, error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(filepath.Join(exeDir, updateLock), flags, 0600)
	if !create && errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	ok, err := tryUpdateFileLock(f, create)
	if !ok || err != nil {
		f.Close()
		return nil, ok, err
	}
	return f, true, nil
}

func updateInProgress() bool {
	f, free, err := openUpdateLock(false)
	if f != nil {
		f.Close()
	}
	return err == nil && !free
}

func updateHelperName() string {
	if runtime.GOOS == "windows" {
		return "apply.exe"
	}
	return "apply"
}

func copyUpdateFile(from, to string, mode os.FileMode) error {
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeUpdateFile(to, mode, func(w io.Writer) error {
		_, err := io.Copy(w, f)
		return err
	})
}

func cleanupUpdateStages() {
	dirs, _ := filepath.Glob(filepath.Join(exeDir, updateDirPrefix+"*"))
	for _, dir := range dirs {
		if !fileExists(filepath.Join(dir, "finished")) {
			continue
		}
		err := os.Remove(filepath.Join(dir, updateHelperName()))
		if err == nil || errors.Is(err, os.ErrNotExist) {
			_ = os.RemoveAll(dir)
		}
	}
}

func stageUpdate(r updateRelease, manifest []byte) (dir string, err error) {
	if err := allowUpdateAccount(); err != nil {
		return "", err
	}
	if l, ok := readWatchLock(); ok && lockAlive(l) && l.Account != updateAccount() {
		return "", errors.New("tiến trình theo dõi đang chạy bằng tài khoản khác; hãy cập nhật từ tài khoản chạy app")
	}
	cleanupUpdateStages()
	dir, err = os.MkdirTemp(exeDir, updateDirPrefix)
	if err != nil {
		return "", err
	}
	stagedDir := dir
	defer func() {
		if err != nil {
			_ = os.RemoveAll(stagedDir)
		}
	}()
	resp, err := openUpdateURL(context.Background(), r.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := writeUpdateFile(filepath.Join(dir, updateCandidate), 0700, func(w io.Writer) error {
		return copyUpdatePayload(w, resp.Body, r)
	}); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0600); err != nil {
		return "", err
	}
	if err := copyUpdateFile(exePath(), filepath.Join(dir, updateHelperName()), 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func startStagedUpdate(dir, chat string, automatic bool) error {
	pid := os.Getpid()
	start, _ := procStartTime(pid)
	args := []string{"--apply-staged-update", filepath.Base(exePath()), strconv.Itoa(pid), strconv.FormatInt(start, 10), chat}
	if automatic {
		args = append(args, "auto")
	}
	cmd := exec.Command(filepath.Join(dir, updateHelperName()), args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func menuUpdate() bool {
	if b, err := readUpdateFile(filepath.Join(exeDir, updateResult), 4096); err == nil {
		fmt.Print(string(b))
	}
	fmt.Println("Đang kiểm tra bản cập nhật cho v" + version + "…")
	r, manifest, newer, err := checkUpdate()
	if err != nil {
		fmt.Println("Cập nhật:", err)
		return false
	}
	if !newer {
		fmt.Println("Kênh cập nhật chưa có bản mới hơn v" + version + ".")
		return false
	}
	fmt.Println("Có bản v" + r.Version)
	if r.Notes != "" {
		fmt.Println(r.Notes)
	}
	if !askYes("Tải và cài bản này? (y/N)", "N") || !requirePIN("cài bản cập nhật") {
		return false
	}
	fmt.Println("Đang tải và xác minh gói cập nhật…")
	dir, err := stageUpdate(r, manifest)
	if err == nil {
		err = startStagedUpdate(dir, "", false)
	}
	if err != nil {
		fmt.Println("Cập nhật:", err)
		return false
	}
	fmt.Println("Đã bàn giao gói cập nhật. Mở lại app sau khi cài xong và xem kết quả tại mục 12.")
	return true
}

func fileUpdateHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, executableLimit+1))
	if err == nil && n > executableLimit {
		err = errors.New("tệp thực thi vượt giới hạn")
	}
	return hash.Sum(nil), err
}

func loadStagedUpdate(dir string) (updateRelease, error) {
	c, err := loadUpdateChannel()
	if err != nil {
		return updateRelease{}, err
	}
	b, err := readUpdateFile(filepath.Join(dir, "manifest.json"), manifestLimit)
	if err != nil {
		return updateRelease{}, err
	}
	r, err := verifyUpdate(b, c.PublicKey)
	if err != nil {
		return r, err
	}
	if newer, err := newerVersion(r.Version, version); err != nil || !newer {
		return r, errors.New("gói phải mới hơn phiên bản đang cài")
	}
	f, err := os.Open(filepath.Join(dir, updateCandidate))
	if err != nil {
		return r, err
	}
	defer f.Close()
	return r, copyUpdatePayload(io.Discard, f, r)
}

func applyStagedUpdate(stage string, args []string) error {
	automatic := len(args) == 5 && args[4] == "auto"
	if (len(args) != 4 && !automatic) || filepath.Base(args[0]) != args[0] || args[0] == "." || args[0] == ".." ||
		!strings.HasPrefix(filepath.Base(stage), updateDirPrefix) || args[0] == "" {
		return errors.New("tham số cài cập nhật không hợp lệ")
	}
	if runtime.GOOS == "windows" && (!strings.EqualFold(filepath.Ext(args[0]), ".exe") || strings.ContainsAny(args[0], ":/\\")) {
		return errors.New("tên EXE không hợp lệ")
	}
	if err := allowUpdateAccount(); err != nil {
		return err
	}
	parent, e1 := strconv.Atoi(args[1])
	start, e2 := strconv.ParseInt(args[2], 10, 64)
	if e1 != nil || e2 != nil || parent <= 0 || parent == os.Getpid() {
		return errors.New("tiến trình yêu cầu cập nhật không hợp lệ")
	}

	// Cấu hình, trạng thái và EXE chính nằm cạnh thư mục staging.
	exeDir = filepath.Dir(stage)
	cfgPath = filepath.Join(exeDir, cfgName)
	logPath = filepath.Join(exeDir, logName)
	defer os.WriteFile(filepath.Join(stage, "finished"), nil, 0600)

	r, err := loadStagedUpdate(stage)
	if err != nil {
		return reportUpdate(stage, args[3], err, "")
	}
	if automatic {
		if !automaticUpdateReady(time.Now()) || !autoVersionAllowed(r.Version, time.Now()) {
			return nil
		}
	} else if err := probeUpdate(filepath.Join(stage, updateCandidate), r.Version); err != nil {
		return reportUpdate(stage, args[3], errors.New("gói đã ký không qua kiểm tra khởi động"), "")
	}
	target := filepath.Join(exeDir, args[0])
	watcher, _ := readWatchLock()
	wasWatching := lockAlive(watcher)
	if wasWatching && watcher.Account != updateAccount() {
		return reportUpdate(stage, args[3], errors.New("tiến trình theo dõi chạy bằng tài khoản khác"), "")
	}
	lock, free, err := openUpdateLock(true)
	for deadline := time.Now().Add(time.Second); err == nil && !free && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		lock, free, err = openUpdateLock(true)
	}
	if err != nil {
		return reportUpdate(stage, args[3], err, "")
	}
	if !free {
		return reportUpdate(stage, args[3], errors.New("đang cài một bản cập nhật khác"), "")
	}

	installErr := installStagedUpdate(stage, target, r, watchLock{PID: parent, Start: start}, watcher, automatic)
	lock.Close()
	startMessage := ""
	if wasWatching && !stopIntended() && cfgReady(loadCfg()) {
		startMessage = resumeAfterUpdate(target)
	}
	if installErr != nil {
		return reportUpdate(stage, args[3], installErr, startMessage)
	}
	return reportUpdate(stage, args[3], nil, updateSuccessNotice(r, startMessage))
}

func installStagedUpdate(stage, target string, r updateRelease, parent, previousWatcher watchLock, automatic bool) error {
	oldHash, err := fileUpdateHash(target)
	if err != nil {
		return err
	}
	helperHash, err := fileUpdateHash(exePath())
	if err != nil {
		return err
	}
	if !bytes.Equal(oldHash, helperHash) {
		return errors.New("EXE đã thay đổi kể từ khi tải; hãy kiểm tra cập nhật lại")
	}

	deadline := time.Now().Add(3 * time.Minute)
	for {
		// Trên Linux, tiến trình đã thoát có thể còn là zombie trong lúc cha thu hồi.
		watcher, _ := readWatchLock()
		if !activeUpdateProcess(parent) && !activeUpdateProcess(previousWatcher) && !activeUpdateProcess(watcher) {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("tiến trình đang dùng EXE chưa thoát; hãy thử lại")
		}
		time.Sleep(250 * time.Millisecond)
	}

	if automatic {
		if !automaticUpdateReady(time.Now()) {
			return errors.New("đã hoãn cập nhật tự động vì khung giờ hoặc cài đặt đã đổi")
		}
		if !autoVersionAllowed(r.Version, time.Now()) {
			return errors.New("phiên bản này đã được thử cài tự động")
		}
		if err := recordAutoAttempt(stage, r.Version, time.Now().Add(autoRetryDelay).Unix()); err != nil {
			return fmt.Errorf("lưu trạng thái cập nhật tự động: %w", err)
		}
		if err := probeUpdate(filepath.Join(stage, updateCandidate), r.Version); err != nil {
			return errors.Join(errors.New("gói đã ký không qua kiểm tra khởi động"), recordAutoAttempt(stage, r.Version, 0))
		}
	}

	backup := target + ".previous"
	tmp := filepath.Join(stage, "previous")
	if err := copyUpdateFile(target, tmp, 0700); err != nil {
		return fmt.Errorf("sao lưu bản hiện tại: %w", err)
	}
	if err := replaceUpdateFile(tmp, backup); err != nil {
		return fmt.Errorf("lưu bản sao EXE: %w", err)
	}
	if err := replaceUpdateFile(filepath.Join(stage, updateCandidate), target); err != nil {
		return fmt.Errorf("thay EXE: %w", err)
	}
	if automatic {
		if err := recordAutoAttempt(stage, r.Version, 0); err != nil {
			logf("[update] Không lưu được trạng thái thử bản v%s: %v", r.Version, err)
		}
	}
	if err := probeUpdate(target, r.Version); err != nil {
		restore := filepath.Join(stage, "restore")
		restoreErr := copyUpdateFile(backup, restore, 0700)
		if restoreErr == nil {
			restoreErr = replaceUpdateFile(restore, target)
		}
		if restoreErr != nil {
			return fmt.Errorf("bản mới không mở được và không tự phục hồi được; bản sao ở %s: %w", filepath.Base(backup), restoreErr)
		}
		return errors.New("bản mới không qua kiểm tra khởi động; đã khôi phục EXE trước đó")
	}
	return nil
}

func probeUpdate(path, expected string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	detach(cmd)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != appTitle+" v"+expected {
		return errors.New("phiên bản thực thi không khớp")
	}
	return nil
}

func resumeAfterUpdate(target string) string {
	if running, _, _ := watchRunning(); running {
		return " Theo dõi đang chạy."
	}
	cmd := exec.Command(target, "--ensure")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return " Chưa bật lại theo dõi: " + err.Error()
	}
	_ = cmd.Process.Release()
	for end := time.Now().Add(8 * time.Second); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		if running, _, _ := watchRunning(); running {
			return " Đã bật lại theo dõi."
		}
		if stopIntended() {
			return " Giữ trạng thái đã dừng theo dõi."
		}
	}
	return " Chưa xác nhận được theo dõi đã chạy. Kiểm tra mục 9."
}

func reportUpdate(stage, chat string, err error, status string) error {
	if err != nil {
		status = "Lỗi cập nhật: " + err.Error() + status
	}
	logf("[update] %s", status)
	text := time.Now().Format("02/01/2006 15:04:05") + " · " + status + "\n"
	tmp := filepath.Join(stage, "result")
	if writeErr := os.WriteFile(tmp, []byte(text), 0600); writeErr == nil {
		if writeErr := replaceUpdateFile(tmp, filepath.Join(exeDir, updateResult)); writeErr != nil {
			logf("[update] Không lưu được kết quả: %v", writeErr)
		}
	}
	cfg := loadCfg()
	if err == nil && chat == "" {
		chat = cfg.TelegramPersonal
	}
	if chat != "" && chat == cfg.TelegramPersonal {
		message := esc(status)
		if err == nil {
			message = telegramUpdateNotice(status)
		}
		_ = sendTelegramAt(cfg, chatTarget{chat: chat}, message, true)
	}
	return err
}

// Chỉ rời vòng quét sau khi các tin đã xếp hàng gửi xong. Nếu phía cài
// đặt hết thời gian chờ hoặc thoát, vòng quét tiếp tục như bình thường.
func waitForNextWatch(delay time.Duration, request **chatTarget) bool {
	var wake <-chan chatTarget
	if request != nil {
		wake = botScanQueue
	}
	sleep := time.NewTimer(delay)
	defer sleep.Stop()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		if updateInProgress() {
			pending := make(chan struct{})
			startLiveQueue()
			select {
			case liveQueue <- outMsg{drained: pending}:
				for updateInProgress() {
					select {
					case <-pending:
						return updateInProgress()
					case <-poll.C:
					}
				}
			case <-poll.C:
			}
		}
		select {
		case <-sleep.C:
			return false
		case target := <-wake:
			*request = &target
			return false
		case <-poll.C:
		}
	}
}
