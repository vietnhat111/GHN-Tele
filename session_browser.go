package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// Lấy SESSION GHN từ localStorage của Chrome/Edge/Cốc Cốc đang đăng nhập.
// Chỉ đọc file leveldb, không sửa gì, không gửi dữ liệu đi đâu.

type browserDB struct {
	browser string
	profile string
	dir     string // thư mục leveldb
	mod     time.Time
}

type sessionCand struct {
	token   string
	hub     string
	browser string
	profile string
	mod     time.Time
}

func browserRoots() []string {
	var out []string
	add := func(p string) {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	switch runtime.GOOS {
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		add(filepath.Join(local, "Google", "Chrome", "User Data"))
		add(filepath.Join(local, "Microsoft", "Edge", "User Data"))
		add(filepath.Join(local, "BraveSoftware", "Brave-Browser", "User Data"))
		add(filepath.Join(local, "CocCoc", "Browser", "User Data"))
		add(filepath.Join(local, "Chromium", "User Data"))
		roaming := os.Getenv("APPDATA")
		add(filepath.Join(roaming, "Opera Software", "Opera Stable"))
	case "darwin":
		home, _ := os.UserHomeDir()
		base := filepath.Join(home, "Library", "Application Support")
		add(filepath.Join(base, "Google", "Chrome"))
		add(filepath.Join(base, "Microsoft Edge"))
		add(filepath.Join(base, "BraveSoftware", "Brave-Browser"))
		add(filepath.Join(base, "CocCoc", "Browser"))
		add(filepath.Join(base, "Chromium"))
	default:
		home, _ := os.UserHomeDir()
		base := filepath.Join(home, ".config")
		add(filepath.Join(base, "google-chrome"))
		add(filepath.Join(base, "chromium"))
		add(filepath.Join(base, "microsoft-edge"))
		add(filepath.Join(base, "BraveSoftware", "Brave-Browser"))
	}
	return out
}

func levelDBMod(dir string) time.Time {
	var newest time.Time
	ents, err := os.ReadDir(dir)
	if err != nil {
		return newest
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".log") && !strings.HasSuffix(e.Name(), ".ldb") && !strings.HasSuffix(e.Name(), ".sst") {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	return newest
}

// findBrowserDBs: mọi profile có Local Storage/leveldb.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func findBrowserDBs() []browserDB {
	var out []browserDB
	for _, root := range browserRoots() {
		ents, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		name := strings.ToLower(filepath.Base(root))
		browser := "Chrome"
		switch {
		case strings.Contains(name, "edge"):
			browser = "Edge"
		case strings.Contains(name, "brave"):
			browser = "Brave"
		case strings.Contains(name, "coccoc"):
			browser = "Cốc Cốc"
		case strings.Contains(name, "chromium"):
			browser = "Chromium"
		case strings.Contains(name, "opera"):
			browser = "Opera"
		}
		// Opera: profile nằm ngay ở thư mục gốc, không có "Default"
		if dir := filepath.Join(root, "Local Storage", "leveldb"); isDir(dir) {
			out = append(out, browserDB{browser: browser, profile: "(gốc)", dir: dir, mod: levelDBMod(dir)})
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			prof := e.Name()
			if prof != "Default" && !strings.HasPrefix(prof, "Profile ") && prof != "Guest Profile" {
				continue
			}
			dir := filepath.Join(root, prof, "Local Storage", "leveldb")
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
			out = append(out, browserDB{browser: browser, profile: prof, dir: dir, mod: levelDBMod(dir)})
		}
	}
	return out
}

// decodeLSValue: Chrome lưu chuỗi với 1 byte kiểu: 1 = Latin-1/UTF-8, 0 = UTF-16LE.
func decodeLSValue(v []byte) string {
	if len(v) == 0 {
		return ""
	}
	switch v[0] {
	case 1:
		return string(v[1:])
	case 0:
		u := make([]uint16, 0, (len(v)-1)/2)
		for i := 1; i+1 < len(v); i += 2 {
			u = append(u, uint16(v[i])|uint16(v[i+1])<<8)
		}
		return string(utf16.Decode(u))
	}
	return string(v)
}

// localStorageKeys: lọc các khoá của nhanh.ghn.vn.
func localStorageKeys(dir string) (map[string]string, []string, error) {
	all, warns, err := readLevelDB(dir)
	if err != nil {
		return nil, warns, err
	}
	out := map[string]string{}
	for k, v := range all {
		// khoá dạng: _https://nhanh.ghn.vn\x00\x01SESSION
		if !strings.Contains(k, "ghn.vn") {
			continue
		}
		i := strings.LastIndex(k, "\x00")
		if i < 0 || i+1 >= len(k) {
			continue
		}
		name := k[i+1:]
		if len(name) > 0 && (name[0] == 0 || name[0] == 1) {
			name = name[1:]
		}
		if name == "" {
			continue
		}
		out[name] = decodeLSValue(v)
	}
	return out, warns, nil
}

func browserSessionCandidates(cfg Config) []sessionCand {
	dbs := findBrowserDBs()
	if strings.TrimSpace(cfg.ChromeDir) != "" {
		dbs = append([]browserDB{{
			browser: "Chrome (chỉ định)",
			profile: "custom",
			dir:     cfg.ChromeDir,
			mod:     levelDBMod(cfg.ChromeDir),
		}}, dbs...)
	}
	sort.SliceStable(dbs, func(i, j int) bool { return dbs[i].mod.After(dbs[j].mod) })

	seen := map[string]bool{}
	var out []sessionCand
	for _, db := range dbs {
		vals, _, err := localStorageKeys(db.dir)
		if err != nil {
			continue
		}
		tok := strings.TrimSpace(vals["SESSION"])
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, sessionCand{
			token:   tok,
			hub:     strings.TrimSpace(vals["CURRENT_HUB_SORTING"]),
			browser: db.browser,
			profile: db.profile,
			mod:     db.mod,
		})
	}
	return out
}

func maskToken(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 16 {
		return strings.Repeat("•", len(s))
	}
	return s[:10] + "…" + s[len(s)-6:]
}

// validateSession: gọi GHN 1 lần với SESSION ứng viên.
// Dùng limit = maxFetch để fetchArrived chỉ phát 1 request.
func validateSession(cfg Config, session string) bool {
	c := cfg
	c.Session = session
	c.Limit = maxFetch
	resp, err := fetchArrived(c)
	return err == nil && resp != nil && resp.Status == "OK"
}

var lastBrowserTry time.Time

// tryBrowserSession: thử lấy SESSION từ trình duyệt và lưu nếu dùng được.
func tryBrowserSession(c *Config, force bool) error {
	if !force && time.Since(lastBrowserTry) < 5*time.Minute {
		return errors.New("vừa thử cách đây ít phút")
	}
	lastBrowserTry = time.Now()

	cands := browserSessionCandidates(*c)
	if len(cands) == 0 {
		return errors.New("không thấy SESSION của nhanh.ghn.vn trong profile Chrome/Edge/Cốc Cốc nào (đã đăng nhập bằng trình duyệt đó chưa?)")
	}
	if c.LocationID == "" {
		for _, cand := range cands {
			if cand.hub != "" {
				c.LocationID = cand.hub
				break
			}
		}
	}
	if c.LocationID == "" {
		return errors.New("chưa có mã kho và cũng không thấy CURRENT_HUB_SORTING trong trình duyệt")
	}
	for _, cand := range cands {
		if validateSession(*c, cand.token) {
			c.Session = cand.token
			if err := editCfgFile(func(latest *Config) {
				latest.Session = c.Session
				if latest.LocationID == "" {
					latest.LocationID = c.LocationID
				}
			}); err != nil {
				return err
			}
			logf("[session] Đã lấy SESSION từ %s/%s (%s)", cand.browser, cand.profile, maskToken(cand.token))
			return nil
		}
	}
	return fmt.Errorf("thấy %d SESSION trong trình duyệt nhưng đều không dùng được (có thể đã hết hạn — mở Chrome vào lại nhanh.ghn.vn)", len(cands))
}

// refreshSessionCLI: chạy từ dòng lệnh --session.
func refreshSessionCLI() {
	cfg := loadCfg()
	if cfg.LocationID == "" {
		fmt.Println("Chưa có mã kho trong config — mở app (không tham số) để cài trước.")
		return
	}
	if err := tryBrowserSession(&cfg, true); err != nil {
		fmt.Println("❌", err)
		fmt.Println("Gợi ý: mở Chrome → nhanh.ghn.vn (đăng nhập) → chạy lại; hoặc xem --debug-session")
		return
	}
	fmt.Println("✅ Đã lấy SESSION mới:", maskToken(cfg.Session))
}

// debugSession: in ra những gì tìm thấy, để hỗ trợ khi không lấy được SESSION.
func debugSession(arg string) {
	fmt.Println("Trình duyệt sẽ quét:")
	if strings.TrimSpace(arg) != "" {
		fmt.Println("  • (chỉ định)", arg)
		dbs := []browserDB{{browser: "Chỉ định", profile: "custom", dir: arg}}
		for _, db := range dbs {
			vals, warns, err := localStorageKeys(db.dir)
			fmt.Printf("\n[%s] %s\n", db.browser, db.dir)
			if all, _, e2 := readLevelDB(db.dir); e2 == nil {
				fmt.Printf("   (đọc được %d khoá trong LevelDB)\n", len(all))
			}
			for _, w := range warns {
				fmt.Println("   ⚠️", w)
			}
			if err != nil {
				fmt.Println("   ❌", err)
				continue
			}
			if len(vals) == 0 {
				fmt.Println("   (không có khoá nào của ghn.vn)")
			}
			for k, v := range vals {
				fmt.Printf("   %-24s = %s\n", k, truncate(maskToken(v), 40))
			}
		}
		return
	}
	for _, r := range browserRoots() {
		fmt.Println("  •", r)
	}
	dbs := findBrowserDBs()
	if len(dbs) == 0 {
		fmt.Println("\nKhông thấy profile nào có Local Storage/leveldb.")
		return
	}
	for _, db := range dbs {
		vals, warns, err := localStorageKeys(db.dir)
		fmt.Printf("\n[%s/%s] %s\n", db.browser, db.profile, db.dir)
		if all, _, e2 := readLevelDB(db.dir); e2 == nil {
			fmt.Printf("   (đọc được %d khoá trong LevelDB)\n", len(all))
		}
		for _, w := range warns {
			fmt.Println("   ⚠️", w)
		}
		if err != nil {
			fmt.Println("   ❌", err)
			continue
		}
		if len(vals) == 0 {
			fmt.Println("   (không có khoá nào của ghn.vn)")
		}
		for k, v := range vals {
			fmt.Printf("   %-24s = %s\n", k, truncate(maskToken(v), 40))
		}
	}
	waitEnter()
}
