package main

import (
	"slices"
	"unicode/utf8"
)

const aiLongQuestion = 320

func reasoningIntent(words string) bool {
	return hasWarehousePhrase(words, "phan tich", "so sanh", "suy luan", "uu nhuoc diem",
		"danh gia", "lap ke hoach", "de xuat phuong an", "chon phuong an", "phan tich nguyen nhan",
		"giai thich tung buoc", "giai thich vi sao", "can nhac", "neu thi sao")
}

func needsAIReasoning(text string) bool {
	return utf8.RuneCountInString(text) >= aiLongQuestion || reasoningIntent(warehouseWords(text))
}

// General advice can mention the warehouse without asking for live trip facts.
// Operational questions still go through the local warehouse handlers.
func generalReasoningRequest(text string) bool {
	if aiTripCode.MatchString(text) || aiSensitive.MatchString(text) {
		return false
	}
	words := warehouseWords(text)
	if !reasoningIntent(words) || asksShiftBrief(words) {
		return false
	}
	return !hasWarehousePhrase(words, "hien tai", "bay gio", "hom nay", "luc nay",
		"lan quet", "lich su chuyen", "tra cuu", "xe nao", "chuyen nao", "xe nay", "xe do",
		"con may kien", "bao nhieu kien", "bao nhieu xe", "bien so", "doi gio", "doi lich",
		"xe trong kho", "ban giao xong", "ket ban giao", "xe sap den", "xe sap toi", "ca hom nay", "ca minh con")
}

func preferredAIProviders(providers []aiProvider, reasoning bool) []aiProvider {
	if !reasoning {
		return providers
	}
	ordered := slices.Clone(providers)
	for i, provider := range ordered {
		if provider.kind == "claude" {
			copy(ordered[1:i+1], ordered[:i])
			ordered[0] = provider
			break
		}
	}
	return ordered
}
