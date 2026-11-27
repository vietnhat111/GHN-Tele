package main

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const maxHistoryTrips = 1500

type tripEvent struct {
	At    time.Time
	Kind  string
	Value string
}

type tripRecord struct {
	Code, Plate, Route string
	Phase, Handover    string
	ETA, Arrival       string
	Packages           int
	Seen               time.Time
	Events             []tripEvent
}

type tripCatalog struct {
	location string
	records  map[string]tripRecord
}

var currentTripCatalog atomic.Pointer[tripCatalog]

func (cfg Config) historyDays() int {
	switch cfg.HistoryDays {
	case 3, 7, 14:
		return cfg.HistoryDays
	default:
		return 7
	}
}

func publishTripCatalog(location string, records map[string]tripRecord) {
	currentTripCatalog.Store(&tripCatalog{location: location, records: maps.Clone(records)})
}

func tripRecords(cfg Config) map[string]tripRecord {
	if history := currentTripCatalog.Load(); history != nil && history.location == cfg.LocationID {
		return history.records
	}
	return nil
}

func recordTripScan(seen *SeenStore, cfg Config, arr, up []Transport, diff scanDiff, now time.Time) {
	if seen.History == nil {
		seen.History = make(map[string]tripRecord)
	}
	observed := make(map[string]bool, len(arr)+len(up))
	record := func(trip Transport, phase string) {
		key := strings.TrimSpace(trip.PartnerTransportationCode)
		if key == "" || observed[key] {
			return
		}
		observed[key] = true
		entry := seen.History[key]
		if phase == "Sắp đến" && (entry.Phase == "Đã vào kho" || entry.Phase == "Đã rời kho") {
			return
		}
		if entry.Code != "" && entry.Packages != totalPkg(trip.WillDropOff) {
			entry.add(now, "Đổi số kiện", fmt.Sprintf("%d → %d", entry.Packages, totalPkg(trip.WillDropOff)))
		}
		entry.Code = key
		if trip.VehiclePlate != "" {
			entry.Plate = trip.VehiclePlate
		}
		if trip.RouteScheduleName != "" {
			entry.Route = trip.RouteScheduleName
		}
		if phase != entry.Phase {
			entry.add(now, phase, trip.ArrivalTime)
		}
		if trip.ExpectedArrivalTime != "" && trip.ExpectedArrivalTime != entry.ETA {
			latest, valid := parseISO(trip.ExpectedArrivalTime)
			old, had := parseISO(entry.ETA)
			if valid && !had {
				entry.add(now, "Giờ dự kiến", historyTime(trip.ExpectedArrivalTime))
			} else if valid && !latest.Equal(old) {
				entry.add(now, "Đổi giờ dự kiến", historyTime(entry.ETA)+" → "+historyTime(trip.ExpectedArrivalTime))
			}
			entry.ETA = trip.ExpectedArrivalTime
		}
		if phase == "Đã vào kho" && trip.StopSessionStatus != "" && trip.StopSessionStatus != entry.Handover {
			entry.Handover = trip.StopSessionStatus
			entry.add(now, "Bàn giao", statusVi(entry.Handover))
		}
		if trip.ArrivalTime != "" {
			entry.Arrival = trip.ArrivalTime
		}
		entry.Phase, entry.Seen, entry.Packages = phase, now, totalPkg(trip.WillDropOff)
		seen.History[key] = entry
	}
	for _, trip := range arr {
		record(trip, "Đã vào kho")
	}
	for _, trip := range up {
		record(trip, "Sắp đến")
	}
	for _, departures := range [][]departedHit{diff.Departed, diff.HistoryDeparted} {
		for _, hit := range departures {
			recordDeparture(seen, hit, observed, now)
		}
	}

	for _, trip := range diff.Gone {
		code := strings.TrimSpace(trip.PartnerTransportationCode)
		if entry, ok := seen.History[code]; ok {
			entry.Phase = "Chờ đối soát"
			entry.add(now, entry.Phase, "")
			seen.History[code] = entry
		}
	}

	cutoff := now.AddDate(0, 0, -cfg.historyDays())
	for code, row := range seen.History {
		if row.Seen.Before(cutoff) {
			delete(seen.History, code)
		} else {
			row.Events = recentTripEvents(row.Events, cutoff)
			seen.History[code] = row
		}
	}
	if len(seen.History) > maxHistoryTrips {
		rows := sortedTripRecords(seen.History, cutoff)
		for _, row := range rows[maxHistoryTrips:] {
			delete(seen.History, row.Code)
		}
	}
}

// Xe đã từng thấy trong kho chỉ có mốc rời kho khi có xác nhận từ GHN.
func pendingHistoryDepartures(seen *SeenStore, arrived []Transport, now time.Time) []Transport {
	if now.Sub(time.Unix(seen.HistoryCheck, 0)) < 5*time.Minute {
		return nil
	}
	present := make(map[string]bool, len(arrived))
	for _, trip := range arrived {
		present[trip.PartnerTransportationCode] = true
	}
	var candidates []Transport
	for code, row := range seen.History {
		if row.Phase == "Đã vào kho" && !present[code] && !row.Seen.Before(now.Add(-6*time.Hour)) {
			candidates = append(candidates, Transport{PartnerTransportationCode: code})
		}
	}
	return candidates
}

func recordDeparture(seen *SeenStore, hit departedHit, observed map[string]bool, now time.Time) {
	code := strings.TrimSpace(hit.It.PartnerTransportationCode)
	if observed[code] {
		return
	}
	entry := seen.History[code]
	if entry.Code == "" {
		entry = tripRecord{Code: code, Plate: hit.It.VehiclePlate, Route: hit.It.RouteScheduleName, Packages: totalPkg(hit.It.WillDropOff)}
	}
	if string(hit.Dep.ArrivalTime) != "" {
		if entry.Phase != "Đã vào kho" {
			entry.add(now, "Đã vào kho", string(hit.Dep.ArrivalTime))
		}
		entry.Arrival = string(hit.Dep.ArrivalTime)
	}
	entry.Phase, entry.Seen = "Đã rời kho", now
	entry.add(now, entry.Phase, string(hit.Dep.DepartureTime))
	seen.History[code] = entry
}

func (r *tripRecord) add(now time.Time, kind, value string) {
	const eventLimit = 24
	if parsed, ok := parseISO(value); ok {
		value = parsed.In(warehouseZone).Format("02/01 15:04")
	}
	start := max(0, len(r.Events)-eventLimit+1)
	// Copy on append so the bot can read the last published catalog concurrently.
	events := append([]tripEvent(nil), r.Events[start:]...)
	r.Events = append(events, tripEvent{At: now, Kind: kind, Value: value})
}

func historyTime(s string) string {
	if at, ok := parseISO(s); ok {
		return at.In(warehouseZone).Format("02/01 15:04")
	}
	return "chưa rõ"
}

func recentTripEvents(events []tripEvent, cutoff time.Time) []tripEvent {
	for len(events) > 0 && events[0].At.Before(cutoff) {
		events = events[1:]
	}
	return events
}

func sortedTripRecords(records map[string]tripRecord, cutoff time.Time) []tripRecord {
	rows := make([]tripRecord, 0, len(records))
	for _, row := range records {
		if !row.Seen.Before(cutoff) {
			row.Events = recentTripEvents(row.Events, cutoff)
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Seen.Equal(rows[j].Seen) {
			return rows[i].Code < rows[j].Code
		}
		return rows[i].Seen.After(rows[j].Seen)
	})
	return rows
}

func normalizePlate(raw string) string {
	return strings.ReplaceAll(strings.TrimSpace(aiWords(raw)), " ", "")
}

func findTripRecords(cfg Config, query string, now time.Time) []tripRecord {
	words := aiWords(query)
	if strings.TrimSpace(words) == "" {
		return nil
	}
	plate := normalizePlate(query)
	cutoff := now.AddDate(0, 0, -cfg.historyDays())
	for code, row := range tripRecords(cfg) {
		if strings.EqualFold(strings.TrimSpace(query), code) && !row.Seen.Before(cutoff) {
			row.Events = recentTripEvents(row.Events, cutoff)
			return []tripRecord{row}
		}
	}
	var rows []tripRecord
	for _, row := range sortedTripRecords(tripRecords(cfg), cutoff) {
		if strings.Contains(words, aiWords(row.Code)) ||
			(row.Plate != "" && (normalizePlate(row.Plate) == plate || strings.Contains(words, aiWords(row.Plate)))) ||
			matchesTripRoute(words, row.Route) {
			rows = append(rows, row)
		}
	}
	return rows
}

func matchesTripRoute(query, route string) bool {
	if route == "" {
		return false
	}
	routeWords := aiWords(route)
	if strings.Contains(query, routeWords) || strings.Contains(routeWords, query) {
		return true
	}
	_, tail, hasRoute := strings.Cut(query, " tuyen ")
	if !hasRoute {
		return false
	}
	tail = " " + tail
	for _, stop := range []string{" doi gio ", " doi lich ", " sap toi ", " sap den ", " den luc ", " may gio ", " lich su ", " em "} {
		if before, _, ok := strings.Cut(tail, stop); ok {
			tail = before + " "
		}
	}
	if name := strings.TrimSpace(tail); len([]rune(name)) >= 3 {
		return strings.Contains(routeWords, " "+name+" ")
	}
	return false
}

func describeTrip(row tripRecord, query string, now time.Time) string {
	words := aiWords(query)
	ref := " · Theo dữ liệu ghi nhận lúc " + row.Seen.In(warehouseZone).Format("02/01 15:04") + "."
	switch {
	case strings.Contains(words, " kien ") || strings.Contains(words, " thung "):
		return fmt.Sprintf("Dạ, chuyến %s ghi nhận %d kiện cần xuống kho%s", row.Code, row.Packages, ref)
	case strings.Contains(words, " ban giao ") || strings.Contains(words, " xong "):
		if row.Handover == "" {
			return "Em chưa thấy trạng thái bàn giao của chuyến " + row.Code + ref
		}
		return "Dạ, chuyến " + row.Code + " · Bàn giao: " + statusVi(row.Handover) + ref
	case strings.Contains(words, " roi kho ") || strings.Contains(words, " ra kho "):
		if row.Phase == "Đã rời kho" {
			return "Dạ, GHN đã xác nhận chuyến " + row.Code + " rời kho" + ref
		}
		return "Em chưa có mốc GHN xác nhận chuyến " + row.Code + " rời kho" + ref
	case strings.Contains(words, " vao luc ") || (strings.Contains(words, " den luc ") || strings.Contains(words, " den chua ") || strings.Contains(words, " vao chua ")) && row.Phase != "Sắp đến":
		return "Dạ, chuyến " + row.Code + " · Giờ vào kho: " + historyTime(row.Arrival) + ref
	case strings.Contains(words, " bao lau ") || strings.Contains(words, " con may phut "):
		if row.Phase == "Sắp đến" && !now.Before(row.Seen) && now.Sub(row.Seen) < 15*time.Minute {
			return aiArrivalText(Transport{PartnerTransportationCode: row.Code, ExpectedArrivalTime: row.ETA}, now) + ref
		}
		return "Dạ, lần gần nhất em ghi nhận chuyến " + row.Code + " ở trạng thái " + row.Phase + ref
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🚚 %s · %s", row.Code, row.Phase)
	events := row.Events
	if strings.Contains(words, " doi gio ") || strings.Contains(words, " doi lich ") {
		events = nil
		for _, event := range row.Events {
			if event.Kind == "Đổi giờ dự kiến" {
				events = append(events, event)
			}
		}
		fmt.Fprintf(&b, "\nDạ, em ghi nhận %d lần đổi giờ trong lịch sử còn lưu.", len(events))
		if row.ETA != "" {
			b.WriteString(" Giờ dự kiến gần nhất: " + historyTime(row.ETA) + ".")
		}
	}
	if row.Route != "" {
		b.WriteString("\nTuyến: " + row.Route)
	}
	if row.Plate != "" {
		b.WriteString(" · Xe " + row.Plate)
	}
	for _, event := range events[max(0, len(events)-8):] {
		fmt.Fprintf(&b, "\n• %s — %s", event.At.In(warehouseZone).Format("02/01 15:04"), event.Kind)
		if event.Value != "" {
			b.WriteString(" · " + event.Value)
		}
	}
	return b.String() + "\n" + strings.TrimPrefix(ref, " · ")
}
