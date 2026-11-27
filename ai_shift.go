package main

import (
	"fmt"
	"strings"
	"time"
)

var warehouseVocabulary = strings.NewReplacer(
	" vo kho ", " vao kho ",
	" boc hang xong ", " ban giao xong ",
	" xuong hang xong ", " ban giao xong ",
	" ket hang ", " ket ban giao ",
	" tre gio ", " qua gio ",
)

// Normalize warehouse vocabulary without changing the original Telegram text.
func warehouseWords(text string) string {
	return warehouseVocabulary.Replace(aiWords(text))
}

func hasWarehousePhrase(words string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(words, " "+phrase+" ") {
			return true
		}
	}
	return false
}

func asksShiftBrief(words string) bool {
	if hasWarehousePhrase(words, "ban giao ca kho") {
		return false
	}
	return hasWarehousePhrase(words, "ban giao ca", "tom tat ca", "viec con lai cua ca",
		"ca minh con", "ca nay con viec", "ca nay con gi", "ca sau can lam gi",
		"chuyen ca con gi", "cuoi ca con", "giao ca", "noi dung ban giao ca")
}

func aiFleetWide(words string) bool {
	return asksShiftBrief(words) || hasWarehousePhrase(words,
		"xe nao", "chuyen nao", "con nao", "ca kho", "may xe", "bao nhieu xe",
		"bao nhieu chuyen", "kho con bao nhieu kien", "xe ke tiep", "xe tiep theo", "xe dau tien", "tong so kien",
		"xe sap toi", "xe sap den", "xe trong kho", "xe tre nhat", "chuyen tre nhat")
}

func freshFleetSnapshot(cfg Config, data snapshot, now time.Time) bool {
	freshness := max(15*time.Minute, 2*time.Duration(cfg.liveMin())*time.Minute)
	return !cfg.Paused && !data.At.IsZero() && !data.At.After(now) && now.Sub(data.At) <= freshness &&
		(data.Location == "" || data.Location == cfg.LocationID)
}

func upcomingStatus(cfg Config, data snapshot) string {
	if !cfg.upcomingOn() {
		return "Máy kho đang tắt quét xe sắp đến."
	}
	if data.UpcomingDisabled {
		return "Danh sách xe sắp đến chưa được quét sau khi bật theo dõi."
	}
	return ""
}

func distinctWarehouseTrips(arr, up []Transport) (arrived, upcoming []Transport, unidentified int) {
	seen := make(map[string]bool, len(arr)+len(up))
	for _, source := range []struct {
		rows []Transport
		dest *[]Transport
	}{{arr, &arrived}, {up, &upcoming}} {
		for _, trip := range source.rows {
			code := strings.ToUpper(strings.TrimSpace(trip.PartnerTransportationCode))
			if code == "" {
				unidentified++
				continue
			}
			if !seen[code] {
				seen[code] = true
				*source.dest = append(*source.dest, trip)
			}
		}
	}
	return
}

func shiftBrief(cfg Config, data snapshot, latest scanHealth, now time.Time, draft bool) string {
	if !freshFleetSnapshot(cfg, data, now) {
		return "Dạ anh, em chưa có lượt quét đủ mới để chốt bàn giao ca. Anh xem lần quét gần nhất bằng /app nha."
	}
	if latest.at.After(data.At) && latest.result != scanHealthy {
		return fmt.Sprintf("Dạ anh, lần đọc mới nhất chưa đủ để chốt bàn giao. Lượt đầy đủ gần nhất là %s giờ kho.",
			data.At.In(warehouseZone).Format("02/01 15:04"))
	}

	var b strings.Builder
	if draft {
		b.WriteString("Bản nháp bàn giao ca\n")
	} else {
		b.WriteString("Dạ anh, em tóm tắt phần xe cần theo dõi trong ca nha.\n")
	}
	arrived, upcomingTrips, unidentified := distinctWarehouseTrips(data.Arr, data.Up)
	var waiting, unknownHandover int
	var waitingCodes []string
	for _, trip := range arrived {
		code := trip.PartnerTransportationCode
		switch trip.StopSessionStatus {
		case statusDone:
		case "ARRIVED", "TRANSFERRING":
			waiting++
			if len(waitingCodes) < 3 {
				waitingCodes = append(waitingCodes, code)
			}
		default:
			unknownHandover++
		}
	}
	fmt.Fprintf(&b, "• Chưa bàn giao xong: %d xe", waiting)
	if len(waitingCodes) > 0 {
		b.WriteString(" (" + strings.Join(waitingCodes, ", ") + ")")
	}
	if unknownHandover > 0 {
		fmt.Fprintf(&b, " · %d xe chưa rõ tình trạng", unknownHandover)
	}

	if status := upcomingStatus(cfg, data); status != "" {
		b.WriteString("\n• " + status)
	} else {
		upcoming, late, noETA := 0, 0, 0
		var next *Transport
		var nextAt time.Time
		for i := range upcomingTrips {
			trip := &upcomingTrips[i]
			at, valid := parseISO(trip.ExpectedArrivalTime)
			switch {
			case !valid:
				noETA++
			case at.Before(now):
				late++
			default:
				upcoming++
				if next == nil || at.Before(nextAt) {
					next, nextAt = trip, at
				}
			}
		}
		fmt.Fprintf(&b, "\n• Sắp tới: %d xe · Quá giờ dự kiến: %d xe", upcoming, late)
		if noETA > 0 {
			fmt.Fprintf(&b, " · %d xe chưa có giờ", noETA)
		}
		if next != nil {
			fmt.Fprintf(&b, "\n• Tới trước: %s · %s", next.PartnerTransportationCode, nextAt.In(warehouseZone).Format("02/01 15:04"))
		}
	}
	if unidentified > 0 {
		fmt.Fprintf(&b, "\n• GHN có %d dòng chưa rõ mã chuyến.", unidentified)
	}
	fmt.Fprintf(&b, "\nLấy theo lượt quét %s giờ kho.", data.At.In(warehouseZone).Format("02/01 15:04"))
	return b.String()
}

func smallTalkReply(cfg Config, text string) string {
	switch strings.TrimSpace(aiWords(text)) {
	case "chao em", "chao bot", "alo em", "em oi", "alo", "hello em", "hi em":
		if cfg.AIStyle == "gon" {
			return "Dạ, em đây. Anh cần xem chuyến nào ạ?"
		}
		return "Dạ em đây anh 😄 Anh gọi em coi xe hay xem ca mình còn việc gì nè?"
	case "cam on", "cam on em", "cam on nha", "thank em":
		return "Dạ, có gì cần xem thêm anh gọi em nha."
	}
	return ""
}
