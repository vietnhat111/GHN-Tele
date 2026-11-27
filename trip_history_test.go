package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTripHistorySurvivesCommitAndFindsRoutePlateAndCode(t *testing.T) {
	defer botTestDir(t)()
	old := currentTripCatalog.Load()
	t.Cleanup(func() { currentTripCatalog.Store(old) })
	cfg := Config{LocationID: "depot", TelegramToken: "fake", TelegramChat: "-9", HistoryDays: 7}
	now := time.Now()
	seen := newSeen()
	trip := tripAt("E123456", now.Add(2*time.Hour))
	trip.RouteScheduleName, trip.VehiclePlate = "Đà Nẵng - Huế", "51C-12345"
	recordTripScan(seen, cfg, nil, []Transport{trip}, scanDiff{}, now)
	publishTripCatalog(cfg.LocationID, seen.History)
	before := currentTripCatalog.Load()
	trip.ExpectedArrivalTime = now.Add(3 * time.Hour).Format(time.RFC3339)
	recordTripScan(seen, cfg, nil, []Transport{trip}, scanDiff{}, now.Add(time.Minute))
	trip.StopSessionStatus = statusDone
	trip.ArrivalTime = now.Add(3 * time.Hour).Format(time.RFC3339)
	recordTripScan(seen, cfg, []Transport{trip}, nil, scanDiff{}, now.Add(3*time.Hour))
	after := now.Add(4 * time.Hour)
	recordTripScan(seen, cfg, nil, nil, scanDiff{Departed: []departedHit{{It: trip, Dep: DepartedTrip{
		ArrivalTime: flexTime(trip.ArrivalTime), DepartureTime: flexTime(after.Format(time.RFC3339)),
	}}}}, after)
	if got := before.records[trip.PartnerTransportationCode]; len(got.Events) != 2 || got.Phase != "Sắp đến" {
		t.Fatalf("published catalog was mutated: %+v", got)
	}
	if err := commitScan(cfg, seen, nil, after); err != nil {
		t.Fatal(err)
	}
	outbox.Lock()
	outbox.path = ""
	outbox.value = deliveryState{}
	outbox.Unlock()
	publishTripCatalog(cfg.LocationID, loadCommittedSeen(cfg).History)
	for _, q := range []string{"e123456", "51c12345", "Đà Nẵng", "Xe tuyến Da Nang đổi giờ mấy lần rồi em?"} {
		rows := findTripRecords(cfg, q, after)
		if len(rows) != 1 || rows[0].Phase != "Đã rời kho" {
			t.Fatalf("%q matched %+v", q, rows)
		}
		answer := describeTrip(rows[0], "đổi giờ mấy lần", after)
		if !strings.Contains(answer, "1 lần đổi giờ") {
			t.Fatalf("wrong change information: %s", answer)
		}
	}
	if got := findTripRecords(cfg, "E123456", after.AddDate(0, 0, 8)); len(got) != 0 {
		t.Fatal("expired record was exposed")
	}
}

func TestTripChoiceScopedToOriginalMemberAndTopic(t *testing.T) {
	oldCatalog, oldTG := currentTripCatalog.Load(), tgBase
	t.Cleanup(func() { currentTripCatalog.Store(oldCatalog); tgBase = oldTG })
	var mu sync.Mutex
	var replies []map[string]any
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		if filepath.Base(r.URL.Path) == "sendMessage" {
			mu.Lock()
			replies = append(replies, p)
			mu.Unlock()
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer telegram.Close()
	tgBase = telegram.URL
	now := time.Now()
	cfg := Config{LocationID: "depot", TelegramToken: "local", TelegramPersonal: "42", TelegramChat: "-99"}
	a := tripRecord{Code: "E123456", Packages: 12, Seen: now}
	b := tripRecord{Code: "E543210", Packages: 88, Seen: now}
	publishTripCatalog(cfg.LocationID, map[string]tripRecord{a.Code: a, b.Code: b})
	m := &tgIncoming{From: user("7"), Date: now.Unix(), MessageID: 1, MessageThreadID: 10, Chat: tgChat{Type: "supergroup", ID: json.RawMessage("-99")}}
	extracted := tripsReferencedIn(cfg, "• E123456\n• E543210", now)
	if len(extracted) != 2 {
		t.Fatalf("could not identify two trips in grouped alert: %+v", extracted)
	}
	_, rows := tripLookupResult(cfg, m, extracted, "còn mấy kiện", now)
	if len(rows) != 2 {
		t.Fatalf("expected two trip choices: %+v", rows)
	}
	q := tgCallbackQuery{ID: "t", From: user("8"), Message: m, Data: rows[0][0].Data}
	handleBotCallback(cfg, &q, now)
	q.From = m.From
	m.MessageThreadID++
	handleBotCallback(cfg, &q, now)
	m.MessageThreadID--
	blocked := cfg
	blocked.GroupHistoryOff = true
	handleBotCallback(blocked, &q, now)
	handleBotCallback(cfg, &q, now)
	handleBotCallback(cfg, &q, now) // a used choice is rejected
	mu.Lock()
	defer mu.Unlock()
	if len(replies) != 1 || replies[0]["message_thread_id"] != float64(10) || !strings.Contains(replies[0]["text"].(string), "12 kiện") {
		t.Fatalf("wrong choice routing: %#v", replies)
	}
}

func TestTelegramCommandMenuExposesSettingsOnlyPrivately(t *testing.T) {
	prev := tgBase
	t.Cleanup(func() { tgBase = prev })
	var mu sync.Mutex
	ownerMenu, legacyGroupScope := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) == "setMyCommands" {
			var req struct {
				Commands []struct{ Command string }
				Scope    struct{ Type string }
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var names []string
			for _, command := range req.Commands {
				names = append(names, command.Command)
			}
			mu.Lock()
			defer mu.Unlock()
			want := "xe,giao,chuyen,q,soan"
			if req.Scope.Type == "chat" {
				want += ",menu"
				ownerMenu = true
			}
			if got := strings.Join(names, ","); got != want {
				t.Errorf("scope %q registered %q; want %q", req.Scope.Type, got, want)
			}
			legacyGroupScope = legacyGroupScope || req.Scope.Type == "chat_member"
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	tgBase = server.URL
	registerCommands(Config{TelegramToken: "test", TelegramChat: "-99", TelegramPersonal: "42", ManagerName: "Owner", ManagerUser: "owner"})
	mu.Lock()
	defer mu.Unlock()
	if !ownerMenu || !legacyGroupScope || strings.Contains(helpText(), "/caidat") {
		t.Fatal("missing private menu, stale group-scope replacement, or public help restriction")
	}
}
