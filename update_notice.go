package main

import "strings"

func updateSuccessNotice(r updateRelease, tracking string) string {
	lines := []string{"✅ GHN Tele đã cập nhật lên v" + r.Version}
	var bullets []string
	for _, line := range strings.Split(r.Notes, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"- ", "* ", "• "} {
			line = strings.TrimPrefix(line, prefix)
		}
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if chars := []rune(line); len(chars) > 120 {
			line = string(chars[:119]) + "…"
		}
		bullets = append(bullets, "• "+line)
		if len(bullets) == 3 {
			break
		}
	}
	if len(bullets) > 0 {
		lines = append(lines, "", "Có gì mới?")
		lines = append(lines, bullets...)
	}
	if tracking = strings.TrimSpace(tracking); tracking != "" {
		lines = append(lines, "", tracking)
	}
	return strings.Join(lines, "\n")
}

func telegramUpdateNotice(status string) string {
	lines := strings.Split(status, "\n")
	for i, line := range lines {
		lines[i] = esc(line)
		if i == 0 || (i == 2 && line == "Có gì mới?") {
			lines[i] = "<b>" + lines[i] + "</b>"
		}
	}
	return strings.Join(lines, "\n")
}
