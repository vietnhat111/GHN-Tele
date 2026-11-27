package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	autoAttemptFile = "update-auto-attempt.json"
	autoInterval    = 15 * time.Minute
	autoBuffer      = 10 * time.Minute
	autoRetryDelay  = time.Hour
)

// Giữ đủ thời gian ngoài khung để dừng, cài đặt và bật lại theo dõi.
func automaticUpdateWindow(now time.Time, cfg Config) bool {
	if inWatchWindow(now, cfg) {
		return false
	}
	start := hhmmToMin(cfg.watchFrom())
	current := now.Hour()*60 + now.Minute()
	minutes := (start - current + 24*60) % (24 * 60)
	remaining := time.Duration(minutes)*time.Minute -
		time.Duration(now.Second())*time.Second - time.Duration(now.Nanosecond())
	return remaining >= autoBuffer
}

func automaticUpdateSettings(now time.Time, cfg Config) bool {
	return cfgReady(cfg) && cfg.liveMin() > 0 && !stopIntended() && automaticUpdateWindow(now, cfg)
}

func automaticUpdateReady(now time.Time) bool {
	channel, err := loadUpdateChannel()
	return err == nil && channel.AutoInstall && automaticUpdateSettings(now, loadCfg())
}

type autoAttempt struct {
	Version string `json:"version"`
	RetryAt int64  `json:"retryAt,omitempty"`
}

// Bản lỗi kiểm tra khởi động được bỏ qua. Lỗi thay EXE do tệp đang mở
// có thể thử lại sau thời gian chờ để tránh lặp khởi động lại.
func autoVersionAllowed(candidate string, now time.Time) bool {
	b, err := readUpdateFile(filepath.Join(exeDir, autoAttemptFile), 1024)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var last autoAttempt
	if err != nil || json.Unmarshal(b, &last) != nil {
		return false
	}
	newer, err := newerVersion(candidate, last.Version)
	return err == nil && (newer || (candidate == last.Version && last.RetryAt > 0 && now.Unix() >= last.RetryAt))
}

func recordAutoAttempt(stage, version string, retryAt int64) error {
	b, err := json.Marshal(autoAttempt{Version: version, RetryAt: retryAt})
	if err != nil {
		return err
	}
	path := filepath.Join(stage, autoAttemptFile)
	if err := os.WriteFile(path, b, 0600); err != nil {
		return err
	}
	return replaceUpdateFile(path, filepath.Join(exeDir, autoAttemptFile))
}

func startAutomaticUpdates(stop <-chan struct{}) {
	go func() {
		tick := time.NewTicker(autoInterval)
		defer tick.Stop()
		for {
			tryAutomaticUpdate(stop)
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
}

func tryAutomaticUpdate(stop <-chan struct{}) {
	if !automaticUpdateReady(time.Now()) || allowUpdateAccount() != nil || updateInProgress() {
		return
	}
	r, manifest, newer, err := checkUpdate()
	if err != nil {
		logf("[update] Kiểm tra tự động: %v", err)
		return
	}
	if !newer || !autoVersionAllowed(r.Version, time.Now()) {
		return
	}
	dir, err := stageUpdate(r, manifest)
	if err != nil {
		logf("[update] Tải tự động v%s: %v", r.Version, err)
		return
	}
	select {
	case <-stop:
		_ = os.RemoveAll(dir)
		return
	default:
	}
	if !automaticUpdateReady(time.Now()) {
		_ = os.RemoveAll(dir)
		return
	}
	if err := startStagedUpdate(dir, loadCfg().TelegramPersonal, true); err != nil {
		logf("[update] Không mở được trình cài tự động: %v", err)
	}
}

func updateOptions() bool {
	channel, err := loadUpdateChannel()
	if err != nil {
		fmt.Println("Cập nhật:", err)
		return false
	}
	fmt.Println("Tự cập nhật ngoài giờ quét:", onOff(channel.AutoInstall))
	switch ask("1 = Kiểm tra bản mới · 2 = Bật/tắt tự cập nhật · Enter = quay lại", "") {
	case "1":
		return menuUpdate()
	case "2":
		if !requirePIN("đổi tự cập nhật") {
			return false
		}
		channel.AutoInstall = !channel.AutoInstall
		if err := saveUpdateChannel(channel); err != nil {
			fmt.Println("Cập nhật:", err)
			return false
		}
		fmt.Println("Tự cập nhật ngoài giờ quét:", onOff(channel.AutoInstall))
	}
	return false
}

func saveUpdateChannel(channel updateChannel) error {
	b, err := json.Marshal(channel)
	if err != nil {
		return err
	}
	return writeAtomicFile(filepath.Join(exeDir, channelFile), append(b, '\n'))
}

func writeAtomicFile(path string, parts ...[]byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	for _, part := range parts {
		if _, err = f.Write(part); err != nil {
			break
		}
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replaceUpdateFile(f.Name(), path)
}
