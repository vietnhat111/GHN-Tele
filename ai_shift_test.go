package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWarehouseAssistantUsesLocalDataForCrewPhrases(t *testing.T) {
	now := time.Date(2026, 11, 27, 23, 55, 0, 0, warehouseZone)
	tomorrow := tripAt("E123456", now.Add(10*time.Minute))
	overdue := tripAt("E654321", now.Add(-20*time.Minute))
	inKho := Transport{PartnerTransportationCode: "E111111", StopSessionStatus: "TRANSFERRING", WillDropOff: WillDropOff{NumOfPkg: 18}}
	done := Transport{PartnerTransportationCode: "E222222", StopSessionStatus: statusDone}
	unknown := Transport{PartnerTransportationCode: "E333333"}
	noETA := Transport{PartnerTransportationCode: "E444444"}
	data := snapshot{At: now.Add(-time.Minute), Location: "1", Arr: []Transport{inKho, done, unknown},
		Up: []Transport{tomorrow, overdue, noETA, inKho}}
	cfg := Config{LocationID: "1"}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected remote call", 500)
	}))
	defer server.Close()
	providers := []aiProvider{{kind: "gemini", key: "synthetic", url: server.URL}}
	for _, tt := range []struct {
		query string
		draft bool
		wants []string
	}{
		{"Ca mình còn việc gì em?", false, []string{"Chưa bàn giao xong: 1 xe", "1 xe chưa rõ tình trạng", "Sắp tới: 1 xe", "Quá giờ dự kiến: 1 xe", "1 xe chưa có giờ", "28/11 00:05", "27/11 23:54"}},
		{"Soạn bàn giao ca", true, []string{"Bản nháp bàn giao ca", "Lấy theo lượt quét"}},
		{"Ca nay con gi em?", false, []string{"Chưa bàn giao xong: 1 xe"}},
		{"Xe nào vô trước em?", false, []string{"E123456", "28/11 00:05"}},
		{"Còn con nào kẹt hàng không em?", false, []string{"1 chưa xong", "1 chưa rõ trạng thái"}},
		{"Chuyến E111111 bốc hàng xong chưa?", false, []string{"Đang bàn giao"}},
		{"Xe E111111 còn mấy kiện?", false, []string{"18 kiện", "chưa đo được số kiện còn trên xe"}},
		{"Cảm ơn em", false, []string{"anh gọi em"}},
	} {
		answer := answerAIQuestion(context.Background(), aiQuestion{text: tt.query, draft: tt.draft}, cfg, data, now, providers)
		for _, want := range tt.wants {
			if !strings.Contains(answer, want) {
				t.Errorf("%q = %q, missing %q", tt.query, answer, want)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("local warehouse queries made %d external calls", calls.Load())
	}
}

func TestShiftBriefWithIncompleteOrExpiredScan(t *testing.T) {
	now := time.Now()
	cfg := Config{LocationID: "kho"}
	data := snapshot{At: now.Add(-2 * time.Minute), Location: "kho"}
	h := scanHealth{at: now.Add(-time.Minute), result: scanPartial}
	if got := shiftBrief(cfg, data, h, now, false); !strings.Contains(got, "chưa đủ") || strings.Contains(got, "0 xe") {
		t.Fatalf("incomplete data listed as complete: %s", got)
	}
	if got := shiftBrief(cfg, data, scanHealth{}, now.Add(time.Hour), false); !strings.Contains(got, "đủ mới") {
		t.Fatalf("stale data accepted: %s", got)
	}
	if got := shiftBrief(cfg, snapshot{At: now, Location: "kho-khac"}, scanHealth{}, now, false); !strings.Contains(got, "đủ mới") {
		t.Fatalf("data from another warehouse accepted: %s", got)
	}
	cfg.NotifyUpcoming = boolPtr(false)
	if got := shiftBrief(cfg, data, scanHealth{}, now, false); !strings.Contains(got, "đang tắt quét") || strings.Contains(got, "Sắp tới: 0 xe") {
		t.Fatalf("disabled list rendered as empty: %s", got)
	}
}

func TestWarehouseFollowUpKeepsOrClearsTripContext(t *testing.T) {
	now := time.Now()
	old := currentTripCatalog.Load()
	t.Cleanup(func() { currentTripCatalog.Store(old) })
	cfg := Config{LocationID: "1", TelegramChat: "-99"}
	rowA := tripRecord{Code: "E123456", Seen: now, Packages: 12}
	rowB := tripRecord{Code: "E654321", Seen: now, Packages: 2}
	publishTripCatalog("1", map[string]tripRecord{rowA.Code: rowA, rowB.Code: rowB})
	msg := &tgIncoming{Text: "Còn mấy kiện?", From: user("77"), Chat: tgChat{Type: "supergroup", ID: json.RawMessage("-99")},
		ReplyTo: &tgIncoming{From: &tgUser{Username: "kho_bot", IsBot: true}, Text: "Xe E123456 và E654321"}}
	q, called := aiQuestionFrom(msg, "kho_bot")
	if reply, buttons, _ := contextualTripReply(cfg, msg, q, now); !called || len(buttons) != 2 || !strings.Contains(reply, "chọn") {
		t.Fatalf("grouped alert needed choice: %q, %v", reply, buttons)
	}
	msg.ReplyTo.Text = "Xe E123456."
	q, _ = aiQuestionFrom(msg, "kho_bot")
	if reply, buttons, _ := contextualTripReply(cfg, msg, q, now); len(buttons) != 0 || !strings.Contains(reply, "12 kiện") {
		t.Fatalf("single trip was not carried forward: %q, %v", reply, buttons)
	}
	msg.Text = "Vậy xe nào vô trước?"
	q, _ = aiQuestionFrom(msg, "kho_bot")
	if q.replyText != "" || strings.Contains(q.text, "E123456") {
		t.Fatalf("whole-warehouse question retained a prior trip: %+v", q)
	}
}
