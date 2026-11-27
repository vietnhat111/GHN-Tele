package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGHNPaginationUsesReportedTotal(t *testing.T) {
	old := apiBase
	t.Cleanup(func() { apiBase = old })
	for _, tc := range []struct {
		name                string
		pageSize, totalRows int
		stopsEarly          bool
	}{
		{"short server pages", 20, 60, false},
		{"more than old cap", 50, 350, false},
		{"incomplete list", 20, 60, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct{ Offset int }
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					http.Error(w, "request", 400)
					return
				}
				var trips []Transport
				if !tc.stopsEarly || request.Offset == 0 {
					for i := request.Offset; i < min(request.Offset+tc.pageSize, tc.totalRows); i++ {
						trips = append(trips, Transport{PartnerTransportationCode: fmt.Sprint(i)})
					}
				}
				_ = json.NewEncoder(w).Encode(ArrivedResp{Status: "OK", Total: tc.totalRows, Data: []ArrivedData{{List: trips}}})
			}))
			defer server.Close()
			apiBase = server.URL
			result, err := fetchArrived(Config{LocationID: "test", Limit: 50})
			if tc.stopsEarly {
				if err == nil {
					t.Fatal("incomplete list was accepted")
				}
			} else if err != nil || len(result.list()) != tc.totalRows {
				t.Fatalf("got %d/%d, error %v", len(result.list()), tc.totalRows, err)
			}
		})
	}
}

func TestGHNDepartureRetryConfirmsAfterFailure(t *testing.T) {
	defer botTestDir(t)()
	prev := apiBase
	oldHealth := warehouseScan.Load()
	t.Cleanup(func() {
		apiBase = prev
		warehouseScan.Store(oldHealth)
	})
	now := time.Now()
	day := now.In(warehouseZone)
	trip := tripAt("E123456789", time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 0, 0, warehouseZone))
	var round atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "transportation-transporting-list") {
			if round.Load() == 2 {
				http.Error(w, "temporary", 503)
			} else {
				fmt.Fprint(w, `{"status":"OK","total":1,"data":[{"partnerTransportationCode":"E123456789"}]}`)
			}
			return
		}
		var rows []Transport
		if round.Load() == 0 && strings.Contains(r.URL.Path, "upcoming") {
			rows = []Transport{trip}
		}
		_ = json.NewEncoder(w).Encode(ArrivedResp{Status: "OK", Total: len(rows), Data: []ArrivedData{{List: rows}}})
	}))
	defer server.Close()
	apiBase = server.URL
	expiry := base64.RawURLEncoding.EncodeToString([]byte(`{"expired":"` + now.Add(time.Hour).Format(time.RFC3339) + `"}`))
	cfg := Config{LocationID: "test", Session: "h." + expiry + ".s", AutoSession: boolPtr(false)}
	seen := newSeen()
	for i := int32(0); i < 4; i++ {
		round.Store(i)
		_, _, msgs, ok := pollLive(&cfg, seen, i == 0)
		if !ok {
			t.Fatal("main scan failed")
		}
		if i == 2 && (len(msgs) != 0 || seen.Missing[trip.PartnerTransportationCode] != vanishAfter) {
			t.Fatalf("should retain unresolved departure without notice: %v", texts(msgs))
		}
		if i == 3 && (len(msgs) != 1 || !strings.Contains(msgs[0].Text, "XE ĐÃ ĐẾN KHO")) {
			t.Fatalf("confirmation did not produce arrival: %v", texts(msgs))
		}
	}
}

func TestScanAndNotificationCommitTogether(t *testing.T) {
	defer botTestDir(t)()
	cfg := Config{LocationID: "test", TelegramToken: "token", TelegramChat: "-9", TelegramPersonal: "42", NotifyMode: "both"}
	now := time.Now()
	seen := newSeen()
	seen.Arrived["E123456789"] = now.UnixMilli()
	path := filepath.Join(exeDir, outboxFile)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	msg := loud("🟠 Xe E123456789")
	if err := commitScan(cfg, seen, []outMsg{msg}, now); err == nil {
		t.Fatal("expected checkpoint write failure")
	}
	state, _ := outboxAccess(cfg, nil)
	if len(state.Scan) != 0 || len(state.Pending) != 0 {
		t.Fatal("failed transaction exposed state or notifications")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := commitScan(cfg, seen, []outMsg{msg}, now); err != nil {
		t.Fatal(err)
	}
	outbox.Lock()
	outbox.path = ""
	outbox.value = deliveryState{}
	outbox.Unlock()
	state, failed := outboxAccess(cfg, nil)
	if failed || len(state.Pending) != 2 || loadCommittedSeen(cfg).Arrived["E123456789"] == 0 {
		t.Fatalf("restart lost checkpoint: %+v", state)
	}
}

func TestETAChangeAndAIIntents(t *testing.T) {
	now := time.Now()
	trip := tripAt("E123456789", now.Add(time.Hour))
	seen := newSeen()
	diffScan(seen, nil, []Transport{trip}, now, true, 30)
	trip.ExpectedArrivalTime = now.Add(-time.Hour).Format(time.RFC3339)
	diff := diffScan(seen, nil, []Transport{trip}, now, false, 30)
	if len(diff.ETA) != 1 || len(diff.Late) != 1 {
		t.Fatalf("ETA change and late event expected: %+v", diff)
	}
	m := &tgIncoming{Text: "Còn mấy kiện?", Chat: tgChat{Type: "group"}, ReplyTo: &tgIncoming{
		From: &tgUser{Username: "kho_bot", IsBot: true}, Text: "Dạ, chuyến E123456789."}}
	q, called := aiQuestionFrom(m, "kho_bot")
	if !called || !strings.Contains(q.text, "E123456789") {
		t.Fatal("reply lost trip reference")
	}
	cfg := Config{AIStyle: "emut"}
	data := snapshot{At: now, Up: []Transport{trip}}
	answer := answerAIQuestion(context.Background(), aiQuestion{text: "xe có bao nhiêu chuyến trễ rồi em"}, cfg, data, now, nil)
	if !strings.Contains(answer, "1 chuyến đã quá giờ") || strings.Contains(answer, "😄") {
		t.Fatalf("incorrect or joking late answer: %s", answer)
	}
	m.Chat.Type, m.ReplyTo, m.Text = "private", nil, "Em soạn giúp anh tin nhắc đổi ca"
	q, called = aiQuestionFrom(m, "")
	if !called || !q.draft {
		t.Fatal("natural private draft not recognized")
	}
}
