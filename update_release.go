package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	updateProduct   = "ghn-tele"
	channelFile     = "update-channel.json"
	manifestLimit   = 128 << 10
	executableLimit = 64 << 20
)

type updateChannel struct {
	ManifestURL string `json:"manifestUrl"`
	PublicKey   []byte `json:"publicKey"`
	AutoInstall bool   `json:"autoInstall,omitempty"`
}

type signedUpdate struct {
	Release   []byte `json:"release"`
	Signature []byte `json:"signature"`
}

type updateRelease struct {
	Product string `json:"product"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes,omitempty"`
}

var githubRepo = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*/[a-zA-Z0-9_.-]+$`)

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

func versionParts(s string) ([3]int, error) {
	var parts [3]int
	m := releaseVersion.FindStringSubmatch(s)
	if m == nil {
		return parts, fmt.Errorf("phiên bản phải có dạng 2.15.1")
	}
	for i, part := range m[1:] {
		parts[i], _ = strconv.Atoi(part)
	}
	return parts, nil
}

func newerVersion(candidate, current string) (bool, error) {
	a, err := versionParts(candidate)
	if err != nil {
		return false, err
	}
	b, err := versionParts(current)
	if err != nil {
		return false, err
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i], nil
		}
	}
	return false, nil
}

func updateHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("đường dẫn cập nhật phải là URL HTTPS hợp lệ")
	}
	return nil
}

func readUpdateFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readUpdateBytes(f, limit)
}

func readUpdateBytes(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("tệp cập nhật vượt giới hạn")
	}
	return b, err
}

func loadUpdateChannel() (updateChannel, error) {
	var c updateChannel
	b, err := readUpdateFile(filepath.Join(exeDir, channelFile), manifestLimit)
	if errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("chưa cấu hình kênh cập nhật (%s)", channelFile)
	}
	if err != nil {
		return c, err
	}
	if json.Unmarshal(b, &c) != nil || len(c.PublicKey) != ed25519.PublicKeySize {
		return c, errors.New("cấu hình kênh cập nhật hoặc khóa công khai không hợp lệ")
	}
	return c, updateHTTPS(c.ManifestURL)
}

func updateChannelConfigured() bool {
	_, err := loadUpdateChannel()
	return err == nil
}

func validateUpdateRelease(r updateRelease) error {
	hash, err := hex.DecodeString(r.SHA256)
	if r.Product != updateProduct || r.OS == "" || r.Arch == "" ||
		r.Size <= 0 || r.Size > executableLimit || err != nil || len(hash) != sha256.Size || len(r.Notes) > 1500 {
		return errors.New("thông tin gói phát hành không hợp lệ")
	}
	if _, err := versionParts(r.Version); err != nil {
		return err
	}
	return updateHTTPS(r.URL)
}

func verifyUpdate(manifest, key []byte) (updateRelease, error) {
	var envelope signedUpdate
	var release updateRelease
	if json.Unmarshal(manifest, &envelope) != nil || len(key) != ed25519.PublicKeySize ||
		!ed25519.Verify(ed25519.PublicKey(key), envelope.Release, envelope.Signature) {
		return release, errors.New("chữ ký bản cập nhật không hợp lệ")
	}
	if err := json.Unmarshal(envelope.Release, &release); err != nil {
		return release, errors.New("nội dung bản phát hành không hợp lệ")
	}
	if err := validateUpdateRelease(release); err != nil {
		return release, err
	}
	if release.OS != runtime.GOOS || release.Arch != runtime.GOARCH {
		return release, fmt.Errorf("bản phát hành dành cho %s/%s", release.OS, release.Arch)
	}
	return release, nil
}

var updateHTTP = &http.Client{
	Timeout: 3 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("quá nhiều lần chuyển hướng")
		}
		return updateHTTPS(req.URL.String())
	},
}

func openUpdateURL(ctx context.Context, raw string) (*http.Response, error) {
	if err := updateHTTPS(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("đường dẫn cập nhật không hợp lệ")
	}
	resp, err := updateHTTP.Do(req)
	if err != nil {
		// Không đưa URL có thể chứa thông tin riêng của máy chủ vào log hoặc Telegram.
		return nil, errors.New("không kết nối được máy chủ cập nhật qua HTTPS")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("máy chủ cập nhật trả HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func checkUpdate() (updateRelease, []byte, bool, error) {
	var r updateRelease
	c, err := loadUpdateChannel()
	if err != nil {
		return r, nil, false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := openUpdateURL(ctx, c.ManifestURL)
	if err != nil {
		return r, nil, false, err
	}
	defer resp.Body.Close()
	b, err := readUpdateBytes(resp.Body, manifestLimit)
	if err != nil {
		return r, nil, false, err
	}
	r, err = verifyUpdate(b, c.PublicKey)
	if err != nil {
		return r, nil, false, err
	}
	newer, err := newerVersion(r.Version, version)
	return r, b, newer, err
}

func copyUpdatePayload(dst io.Writer, src io.Reader, r updateRelease) error {
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(src, r.Size+1))
	if err != nil {
		return err
	}
	if n != r.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), r.SHA256) {
		return errors.New("kích thước hoặc SHA-256 của gói cập nhật không khớp")
	}
	return nil
}

func writeUpdateFile(path string, mode os.FileMode, write func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	err = write(f)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func githubUpdateURL(repo string) (string, error) {
	if !githubRepo.MatchString(repo) || strings.HasSuffix(repo, "/.") || strings.HasSuffix(repo, "/..") {
		return "", errors.New("repo GitHub phải có dạng ten-tai-khoan/ten-repo")
	}
	return "https://github.com/" + repo + "/releases/latest/download/update.json", nil
}

func updateKeygen(path, manifestURL string, autoInstall bool) error {
	if err := updateHTTPS(manifestURL); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := writeUpdateFile(path, 0600, func(w io.Writer) error {
		return json.NewEncoder(w).Encode(priv.Seed())
	}); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(updateChannel{ManifestURL: manifestURL, PublicKey: pub, AutoInstall: autoInstall})
}

func signUpdate(args []string) error {
	seedJSON, err := readUpdateFile(args[0], 1024)
	if err != nil {
		return err
	}
	var seed []byte
	if json.Unmarshal(seedJSON, &seed) != nil || len(seed) != ed25519.SeedSize {
		return errors.New("tệp khóa riêng không hợp lệ")
	}
	bin, err := os.Open(args[4])
	if err != nil {
		return err
	}
	defer bin.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(bin, executableLimit+1))
	if err != nil {
		return err
	}
	r := updateRelease{Product: updateProduct, Version: args[1], OS: args[2], Arch: args[3],
		URL: args[5], Size: n, SHA256: hex.EncodeToString(hash.Sum(nil))}
	if len(args) == 7 {
		r.Notes = args[6]
	}
	if err := validateUpdateRelease(r); err != nil {
		return err
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(signedUpdate{
		Release: payload, Signature: ed25519.Sign(ed25519.NewKeyFromSeed(seed), payload),
	})
}
