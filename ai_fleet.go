package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func aiFleetVoice(cfg Config, answer string, light bool) string {
	if cfg.AIStyle != "gon" && light {
		return answer + " Anh em chuẩn bị cửa nhận là vừa nha 😄"
	}
	return answer
}

func answerAIFleet(question string, cfg Config, data snapshot, now time.Time) (string, bool) {
	words := aiWords(question)
	has := func(phrases ...string) bool {
		for _, phrase := range phrases {
			if strings.Contains(words, " "+phrase+" ") {
				return true
			}
		}
		return false
	}
	explicit := aiTripCode.FindAllString(question, -1)
	var found *Transport
	var arrived bool
	for i, list := range [][]Transport{data.Arr, data.Up} {
		for j := range list {
			code := list[j].PartnerTransportationCode
			if code != "" && strings.Contains(words, aiWords(code)) {
				found, arrived = &list[j], i == 0
				break
			}
		}
		if found != nil {
			break
		}
	}
	cargo := has("kien", "thung")
	handover := has("ban giao", "boc xong", "xuong hang xong", "xong chua", "ket ban giao")
	late := has("tre", "cham", "qua gio", "chua toi gio nay")
	departure := has("roi kho", "ra kho", "di roi", "roi chua", "ra cong")
	eta := has("doi gio", "doi lich", "gio moi", "may gio", "eta", "bao lau nua", "con bao lau")
	if found == nil && len(explicit) == 0 && !cargo && !handover && !departure && !eta &&
		!has("xe", "chuyen", "bien so", "tai xe", "trong kho", "sap den", "sap toi", "dang o dau") {
		return "", false
	}
	if has("dien thoai", "sdt", "so tai xe", "bien so") {
		return "Em có thể báo số xe, mã chuyến và giờ dự kiến. Phần thông tin tài xế anh tra trong hệ thống GHN nha.", true
	}
	freshness := max(15*time.Minute, 2*time.Duration(cfg.liveMin())*time.Minute)
	if cfg.Paused || data.At.IsZero() || now.Sub(data.At) > freshness ||
		(data.Location != "" && data.Location != cfg.LocationID) {
		return "Em chưa có dữ liệu quét đủ mới để trả lời chắc chắn. Anh dùng /app để xem lần quét gần nhất.", true
	}
	scan := " · Quét lúc " + data.At.In(warehouseZone).Format("15:04") + "."
	if len(explicit) > 1 && !strings.EqualFold(explicit[0], explicit[1]) {
		return "Anh muốn em xem chuyến nào trước ạ? Anh gửi em một mã chuyến nha.", true
	}
	for _, id := range explicit {
		for code, hit := range data.Departed {
			if !strings.EqualFold(id, code) {
				continue
			}
			if cargo {
				return fmt.Sprintf("Dạ, chuyến %s có %d kiện cần xuống theo lần ghi nhận trước khi rời kho%s", code, totalPkg(hit.It.WillDropOff), scan), true
			}
			if _, ok := parseISO(string(hit.Dep.DepartureTime)); ok {
				return fmt.Sprintf("Dạ, GHN ghi nhận chuyến %s đã rời kho lúc %s%s", code, fmtTimeShort(string(hit.Dep.DepartureTime)), scan), true
			}
			return fmt.Sprintf("Dạ, GHN ghi nhận chuyến %s đã rời kho%s", code, scan), true
		}
	}
	if found != nil {
		code := found.PartnerTransportationCode
		switch {
		case cargo:
			return fmt.Sprintf("Dạ, chuyến %s có %d kiện cần xuống kho%s", code, totalPkg(found.WillDropOff), scan), true
		case handover && arrived:
			return fmt.Sprintf("Dạ anh, chuyến %s có trạng thái bàn giao: %s%s", code, statusVi(found.StopSessionStatus), scan), true
		case handover:
			return fmt.Sprintf("Dạ, chuyến %s còn ở danh sách sắp đến, em chưa có trạng thái bàn giao%s", code, scan), true
		case arrived:
			return fmt.Sprintf("Dạ, chuyến %s đã vào kho lúc %s%s", code, fmtTimeShort(found.ArrivalTime), scan), true
		default:
			return aiArrivalText(*found, now) + scan, true
		}
	}
	if len(explicit) > 0 {
		return "Em chưa thấy mã đó trong dữ liệu lần quét mới nhất" + scan, true
	}
	if has("dang o dau") {
		return "Em chưa có vị trí trực tiếp. Anh gửi em mã chuyến để em tra trạng thái và giờ dự kiến nha.", true
	}
	if departure {
		if len(data.Departed) == 0 {
			return "Em chưa có dữ liệu vị trí hoặc xe đã rời kho trong phần đã đối soát.", true
		}
		codes := make([]string, 0, len(data.Departed))
		for code := range data.Departed {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		return fmt.Sprintf("Dạ, em đã đối soát %d chuyến rời kho gần đây. Mã chuyến: %s%s",
			len(codes), strings.Join(codes[:min(5, len(codes))], ", "), scan), true
	}
	if handover {
		if has("chuyen nay", "xe do", "xong chua") {
			return "Anh đang hỏi chuyến nào ạ? Anh gửi mã chuyến hoặc reply vào tin có mã để em xem bàn giao nha.", true
		}
		done, inProgress, unknown := 0, 0, 0
		for _, trip := range data.Arr {
			switch trip.StopSessionStatus {
			case statusDone:
				done++
			case "ARRIVED", "TRANSFERRING":
				inProgress++
			default:
				unknown++
			}
		}
		return fmt.Sprintf("Dạ, xe trong kho: %d đã bàn giao, %d chưa xong, %d chưa rõ trạng thái%s", done, inProgress, unknown, scan), true
	}
	if late {
		if !cfg.upcomingOn() {
			return "Máy kho đang tắt quét xe sắp đến.", true
		}
		count := 0
		var worst *Transport
		var oldest time.Time
		for i := range data.Up {
			t, ok := parseISO(data.Up[i].ExpectedArrivalTime)
			if !ok || !t.Before(now) {
				continue
			}
			count++
			if worst == nil || t.Before(oldest) {
				worst, oldest = &data.Up[i], t
			}
		}
		reply := fmt.Sprintf("Dạ, có %d chuyến đã quá giờ dự kiến mà chưa vào kho", count)
		if worst != nil {
			reply += fmt.Sprintf(". Chuyến %s trễ lâu nhất, khoảng %s", worst.PartnerTransportationCode, durVi(now.Sub(oldest)))
		}
		return reply + scan, true
	}
	if cargo {
		total := func(list []Transport) int {
			n := 0
			for _, trip := range list {
				n += totalPkg(trip.WillDropOff)
			}
			return n
		}
		if has("con", "chuyen nay", "xe nay") {
			return "Anh cho em mã chuyến để xem số kiện của đúng xe nha.", true
		}
		reply := fmt.Sprintf("Dạ, xe trong kho có %d kiện cần xuống", total(data.Arr))
		if cfg.upcomingOn() && !has("trong kho", "da den", "da vao") {
			reply += fmt.Sprintf(" · Xe sắp đến có %d kiện cần xuống", total(data.Up))
		}
		return reply + scan, true
	}
	if has("trong kho", "da den", "da vao") {
		return fmt.Sprintf("Dạ, đang có %d xe trong kho%s", len(data.Arr), scan), true
	}
	if has("doi gio", "doi lich", "gio moi") {
		return "Anh gửi em mã chuyến để em tra giờ dự kiến mới nhất nha.", true
	}
	if !cfg.upcomingOn() {
		return "Máy kho đang tắt quét xe sắp đến.", true
	}
	if !eta && !has("sap den", "sap toi", "ke tiep", "dau tien", "tiep theo", "bao nhieu xe", "may xe", "xe nao", "chuyen nao", "xe ve") {
		return "Anh muốn xem xe sắp tới, xe đang trong kho hay một mã chuyến cụ thể ạ?", true
	}
	var next *Transport
	var nextTime time.Time
	for i := range data.Up {
		t, ok := parseISO(data.Up[i].ExpectedArrivalTime)
		if ok && !t.Before(now) && (next == nil || t.Before(nextTime)) {
			next, nextTime = &data.Up[i], t
		}
	}
	answer := fmt.Sprintf("Dạ, có %d xe đang sắp đến", len(data.Up))
	if next != nil {
		answer += ". " + aiArrivalText(*next, now)
	} else if len(data.Up) > 0 {
		answer += ". Em chưa thấy chuyến nào có giờ dự kiến sắp tới"
	}
	return aiFleetVoice(cfg, answer+scan, next != nil && nextTime.Sub(now) <= 30*time.Minute), true
}
