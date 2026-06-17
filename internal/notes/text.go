package notes

import "strings"

func ExtractTitle(content string) string {
	lines := nonEmptyLines(content)
	title := "Untitled Note"
	if len(lines) > 0 {
		title = lines[0]
	}
	if len(title) > 50 {
		return title[:50] + "..."
	}
	return title
}

func ExtractPreview(content string) string {
	lines := nonEmptyLines(content)
	if len(lines) <= 1 {
		return ""
	}
	preview := strings.Join(lines[1:], " ")
	if len(preview) > 80 {
		return preview[:80] + "..."
	}
	return preview
}

func ExtractVoiceUserContent(content string) string {
	if strings.HasPrefix(content, "User: ") {
		parts := strings.SplitN(content, "\nAssistant: ", 2)
		return strings.TrimPrefix(parts[0], "User: ")
	}
	return content
}

func nonEmptyLines(content string) []string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
