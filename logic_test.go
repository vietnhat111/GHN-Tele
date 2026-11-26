package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseHHMM(t *testing.T) {
	if m, ok := parseHHMM("05:00"); !ok || m != 300 {
		t.Errorf("parseHHMM(05:00) = %d,%v", m, ok)
	}
	for _, bad := range []string{"", "24:00", "12:60", "abc", "12"} {
		if _, ok := parseHHMM(bad); ok {
			t.Errorf("parseHHMM(%q) phải lỗi", bad)
		}
	}
	if hhmmToMin("rác") != -1 {
		t.Error("hhmmToMin chuỗi sai phải -1")
	}
}

func TestInSpan(t *testing.T) {
	if !inSpan(360, 300, 480) || inSpan(500, 300, 480) {
		t.Error("khung 05:00-08:00 sai")
	}
	if !inSpan(23*60, 22*60, 2*60) || inSpan(12*60, 22*60, 2*60) {
		t.Error("khung qua đêm sai")
	}
	if !inSpan(0, 0, 0) {
		t.Error("from == to phải là cả ngày")
	}
}

func TestHasCargo(t *testing.T) {
	if hasCargo(Transport{}) {
		t.Error("xe rỗng không có hàng")
	}
	if !hasCargo(Transport{WillDropOff: WillDropOff{NumOfPkg: 1}}) {
		t.Error("1 kiện là có hàng")
	}
	if !hasCargo(Transport{WillDropOff: WillDropOff{NumOfPkgFwd: 2}}) {
		t.Error("kiện chuyển tiếp cũng là có hàng")
	}
	if got := totalPkg(WillDropOff{NumOfPkg: 1, NumOfPkgBulky: 2, NumOfPkgFwd: 3, NumOfPkgFragile: 9}); got != 6 {
		t.Errorf("totalPkg = %d", got)
	}
}

func plain(s string) string {
	s = strings.ReplaceAll(s, "<b>", "")
	return strings.ReplaceAll(s, "</b>", "")
}

func TestFirstMsg(t *testing.T) {
	it := sampleUpcoming()
	empty := it
	empty.WillDropOff = WillDropOff{}
	msg := plain(firstMsg(empty))
	for _, want := range []string{"XE SẮP ĐẾN KHO", "Chuyến: " + it.PartnerTransportationCode, "Dự kiến đến:", "Cần nhận: chưa có kiện"} {
		if !strings.Contains(msg, want) {
			t.Errorf("thiếu %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(plain(firstMsg(it)), "14 kiện") {
		t.Error("có kiện phải hiện số kiện")
	}
}

func TestUpdateMsg(t *testing.T) {
	it := sampleUpcoming()
	a := pkgSig(WillDropOff{})
	b := pkgSig(WillDropOff{NumOfPkg: 8, NumOfPkgBulky: 1, NumOfPkgFwd: 5})
	msg := plain(updateMsg(it, a, b))
	if !strings.Contains(msg, "CẬP NHẬT SỐ KIỆN") {
		t.Errorf("thiếu tiêu đề:\n%s", msg)
	}
	if !strings.Contains(msg, "Bao cần rã: 0 → 8") || !strings.Contains(msg, "Tổng cần nhận: 0 → 14 kiện") {
		t.Errorf("diff sai:\n%s", msg)
	}
	// đổi 1 loại thì không in dòng không đổi
	msg = plain(updateMsg(it, b, pkgSig(WillDropOff{NumOfPkg: 9, NumOfPkgBulky: 1, NumOfPkgFwd: 5})))
	if strings.Contains(msg, "Cồng kềnh") {
		t.Errorf("không đổi thì không in:\n%s", msg)
	}
}

func TestParseShifts(t *testing.T) {
	sh := parseShifts("05:00-08:00, 11:00-14:00")
	if len(sh) != 2 || sh[0].Name != "05:00-08:00" {
		t.Fatalf("parseShifts sai: %+v", sh)
	}
	if len(parseShifts("rác")) != 0 {
		t.Error("chuỗi rác phải 0 ca")
	}
}

func TestCurrentShift(t *testing.T) {
	cfg := Config{Shifts: "05:00-08:00,11:00-14:00"}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 22, h, m, 0, 0, time.Local) }
	if sh, ok := currentShift(at(6, 0), cfg); !ok || sh.Name != "05:00-08:00" {
		t.Errorf("06:00 phải là ca 1: %+v %v", sh, ok)
	}
	if sh, ok := currentShift(at(12, 30), cfg); !ok || sh.Name != "11:00-14:00" {
		t.Errorf("12:30 phải là ca 2: %+v %v", sh, ok)
	}
	if _, ok := currentShift(at(15, 0), cfg); ok {
		t.Error("15:00 ngoài ca")
	}
}

func TestArrivedMsg(t *testing.T) {
	it := sampleArrived()
	msg := plain(arrivedMsg(it))
	for _, want := range []string{"ĐÃ ĐẾN KHO", it.PartnerTransportationCode, "15 kiện", "Chưa bàn giao"} {
		if !strings.Contains(msg, want) {
			t.Errorf("thiếu %q:\n%s", want, msg)
		}
	}
}

func TestStartMsg(t *testing.T) {
	msg := plain(startMsg(Config{LocationID: "2212700"}, []Transport{sampleArrived()}, []Transport{sampleUpcoming()}))
	if !strings.Contains(msg, "BẮT ĐẦU THEO DÕI") || !strings.Contains(msg, "Sắp đến: 1") {
		t.Errorf("tin bắt đầu sai:\n%s", msg)
	}
}

func TestEscapeHTML(t *testing.T) {
	it := sampleUpcoming()
	it.DriverName = "<b>Gấu</b>"
	it.RouteScheduleName = "A&B"
	msg := firstMsg(it)
	if strings.Contains(msg, "<b>Gấu</b>") || strings.Contains(msg, "A&B") {
		t.Errorf("chưa escape:\n%s", msg)
	}
	if strings.Count(msg, "<b>") != strings.Count(msg, "</b>") {
		t.Errorf("thẻ <b> không cân:\n%s", msg)
	}
}

func TestSplitMsg(t *testing.T) {
	if got := splitMsg("xin chào"); len(got) != 1 {
		t.Errorf("tin ngắn bị cắt: %v", got)
	}
	long := strings.Repeat("dòng thử số một hai ba bốn\n", 400)
	parts := splitMsg(long)
	if len(parts) < 2 {
		t.Fatal("tin dài phải cắt")
	}
	total := 0
	for _, p := range parts {
		if len(p) > tgMaxLen {
			t.Errorf("phần %d quá dài", len(p))
		}
		total += len(p)
	}
	if total != len(long) {
		t.Error("cắt làm mất nội dung")
	}
}

func TestJWTExpiry(t *testing.T) {
	payload := `{"expired":"2030-01-01T00:00:00Z"}`
	tok := "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
	exp, ok := jwtExpiredAt(tok)
	if !ok || exp.Year() != 2030 {
		t.Errorf("đọc hạn sai: %v %v", exp, ok)
	}
	if _, ok := jwtExpiredAt("không-phải-jwt"); ok {
		t.Error("token rác phải trả ok=false")
	}
}

func TestLateText(t *testing.T) {
	now := time.Now()
	if got := lateText(Transport{
		ArrivalTime:         now.Format(time.RFC3339Nano),
		ExpectedArrivalTime: now.Add(-30 * time.Minute).Format(time.RFC3339),
	}); !strings.Contains(got, "trễ 30 phút") {
		t.Errorf("lateText = %q", got)
	}
}

func TestCleanChatID(t *testing.T) {
	for in, want := range map[string]string{
		"-1001234567890":       "-1001234567890",
		`  "-1001234567890"  `: "-1001234567890",
		"'123'":                "123",
		"@kho_ghn":             "@kho_ghn",
	} {
		if got := cleanChatID(in); got != want {
			t.Errorf("cleanChatID(%q) = %q", in, got)
		}
	}
}

func TestTgSendParams(t *testing.T) {
	p := tgSendParams("-1001", 0, "x")
	if _, ok := p["message_thread_id"]; ok {
		t.Error("không có topic thì không gửi thread id")
	}
	p = tgSendParams("-1001", 42, "x")
	if p["message_thread_id"] != 42 {
		t.Error("thiếu message_thread_id")
	}
}

func TestSeenStore(t *testing.T) {
	s := newSeen()
	s.Arrived["A"] = time.Now().UnixMilli()
	s.Sig["A"] = "1 2 3 4"
	cleanSeen(s)
	if len(s.Arrived) != 1 {
		t.Error("chuyến mới không được xoá")
	}
	s.Arrived["OLD"] = time.Now().AddDate(0, 0, -10).UnixMilli()
	s.Sig["OLD"] = "0 0 0 0"
	cleanSeen(s)
	if _, ok := s.Arrived["OLD"]; ok {
		t.Error("chuyến cũ phải bị xoá")
	}
	if _, ok := s.Sig["OLD"]; ok {
		t.Error("chữ ký cũ phải bị xoá")
	}
}

func TestReportMessages(t *testing.T) {
	up := plain(reportUpcoming(&ArrivedResp{Data: []ArrivedData{{List: []Transport{sampleUpcoming()}}}}))
	if !strings.Contains(up, "SẮP ĐẾN KHO") || !strings.Contains(up, "14 kiện") {
		t.Errorf("report sắp đến sai:\n%s", up)
	}
	arr := plain(reportArrived(&ArrivedResp{Data: []ArrivedData{{List: []Transport{sampleArrived()}}}}))
	if !strings.Contains(arr, "ĐÃ ĐẾN KHO") || !strings.Contains(arr, "15 kiện") {
		t.Errorf("report đã đến sai:\n%s", arr)
	}
	empty := reportUpcoming(&ArrivedResp{})
	if !strings.Contains(empty, "Không có chuyến nào") {
		t.Errorf("rỗng phải báo không có:\n%s", empty)
	}
}

func TestRawIDString(t *testing.T) {
	if got := rawIDString(json.RawMessage("-1001234567890")); got != "-1001234567890" {
		t.Errorf("số: %q", got)
	}
	if got := rawIDString(json.RawMessage(`"-1001"`)); got != "-1001" {
		t.Errorf("chuỗi: %q", got)
	}
}

func TestTargets(t *testing.T) {
	both := Config{TelegramChat: "-100", TelegramThread: 7, TelegramPersonal: "42", NotifyMode: "both"}
	ts := both.targets()
	if len(ts) != 2 {
		t.Fatalf("both phải 2 nơi, được %d", len(ts))
	}
	if ts[0].chat != "-100" || ts[0].thread != 7 {
		t.Errorf("nhóm sai: %+v", ts[0])
	}
	if ts[1].chat != "42" || ts[1].thread != 0 {
		t.Errorf("riêng sai: %+v", ts[1])
	}
	if g := (Config{TelegramChat: "-100", TelegramPersonal: "42", NotifyMode: "group"}).targets(); len(g) != 1 || g[0].chat != "-100" {
		t.Errorf("group sai: %+v", g)
	}
	if p := (Config{TelegramChat: "-100", TelegramPersonal: "42", NotifyMode: "personal"}).targets(); len(p) != 1 || p[0].chat != "42" {
		t.Errorf("personal sai: %+v", p)
	}
	if auto := (Config{TelegramChat: "-100", TelegramPersonal: "42"}); auto.notifyMode() != "both" {
		t.Errorf("tự suy phải both, được %q", auto.notifyMode())
	}
	if only := (Config{TelegramPersonal: "42"}); only.notifyMode() != "personal" || len(only.targets()) != 1 {
		t.Error("chỉ có chat riêng phải là personal")
	}
	if len((Config{}).targets()) != 0 {
		t.Error("chưa có ID nào thì không gửi")
	}
}

func TestNewTripsMsg(t *testing.T) {
	list := []Transport{sampleUpcoming(), sampleArrived()}
	msg := plain(newTripsMsg(list))
	if !strings.Contains(msg, "2 XE MỚI SẮP ĐẾN") {
		t.Errorf("thiếu tiêu đề:\n%s", msg)
	}
	if !strings.Contains(msg, list[0].PartnerTransportationCode) || !strings.Contains(msg, list[1].PartnerTransportationCode) {
		t.Errorf("thiếu mã chuyến:\n%s", msg)
	}
	one := plain(firstMsg(sampleUpcoming()))
	if strings.Contains(one, "XE MỚI") {
		t.Error("một chuyến thì dùng tin chi tiết")
	}
}

func TestCfgReady(t *testing.T) {
	if !cfgReady(Config{LocationID: "1", TelegramToken: "t", TelegramChat: "-1"}) {
		t.Error("nhóm là đủ")
	}
	if !cfgReady(Config{LocationID: "1", TelegramToken: "t", TelegramPersonal: "9"}) {
		t.Error("chat riêng là đủ")
	}
	if cfgReady(Config{LocationID: "1", TelegramToken: "t"}) {
		t.Error("không có nơi nhận thì chưa đủ")
	}
}
