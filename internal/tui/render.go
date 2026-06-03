package tui

import (
	"regexp"
	"strings"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type PanelOptions struct {
	Width        int
	RightTitle   string
	MinBodyLines int
}

func RenderPanel(title, body string, options PanelOptions) string {
	width := options.Width
	if width < 20 {
		width = 80
	}
	inner := width - 4
	lines := []string{ansiBorder + "╭" + strings.Repeat("─", width-2) + "╮" + ansiReset}
	header := ansiCyan + ansiBold + title + ansiReset
	if options.RightTitle != "" {
		right := ansiDim + truncateVisible(options.RightTitle, max(10, width/3)) + ansiReset
		gap := inner - displayWidth(header) - displayWidth(right)
		if gap < 1 {
			gap = 1
		}
		header = header + strings.Repeat(" ", gap) + right
	}
	lines = append(lines, panelRow(header, width), panelRow("", width))
	bodyLines := []string{}
	if body != "" {
		for _, line := range strings.Split(body, "\n") {
			bodyLines = append(bodyLines, wrapPlain(line, inner)...)
		}
	}
	for len(bodyLines) < options.MinBodyLines {
		bodyLines = append(bodyLines, "")
	}
	for _, line := range bodyLines {
		lines = append(lines, panelRow(line, width))
	}
	lines = append(lines, ansiBorder+"╰"+strings.Repeat("─", width-2)+"╯"+ansiReset)
	return strings.Join(lines, "\n")
}

func RenderTranscript(entries []TranscriptEntry, scrollOffset, windowSize int) string {
	if len(entries) == 0 {
		return ""
	}
	if windowSize < 4 {
		windowSize = 8
	}
	lines := renderTranscriptLines(entries)
	maxOffset := len(lines) - windowSize
	if maxOffset < 0 {
		maxOffset = 0
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
	}
	end := len(lines) - scrollOffset
	start := end - windowSize
	if start < 0 {
		start = 0
	}
	out := strings.Join(lines[start:end], "\n")
	if scrollOffset > 0 {
		out += "\n\nscroll offset: " + itoa(scrollOffset)
	}
	return out
}

func GetTranscriptMaxScrollOffset(entries []TranscriptEntry, windowSize int) int {
	if len(entries) == 0 {
		return 0
	}
	if windowSize < 4 {
		windowSize = 8
	}
	max := len(renderTranscriptLines(entries)) - windowSize
	if max < 0 {
		return 0
	}
	return max
}

func RenderSlashMenu(commands []SlashCommand, selectedIndex int) string {
	if len(commands) == 0 {
		return ansiDim + "no matching slash commands" + ansiReset
	}
	lines := []string{ansiDim + "commands" + ansiReset}
	for index, command := range commands {
		prefix := "  "
		usage := padRight(command.Usage, 24)
		if index == selectedIndex {
			prefix = "> "
			usage = ansiReverse + " " + usage + " " + ansiReset
		}
		lines = append(lines, prefix+usage+" "+ansiDim+truncateVisible(command.Description, 60)+ansiReset)
	}
	return strings.Join(lines, "\n")
}

func RenderInputPrompt(input string, cursorOffset int) string {
	if cursorOffset < 0 {
		cursorOffset = 0
	}
	if cursorOffset > len(input) {
		cursorOffset = len(input)
	}
	return ansiGreen + ansiBold + "mini-code>" + ansiReset + " " + input[:cursorOffset] + ansiReverse + "|" + ansiReset + input[cursorOffset:]
}

func renderTranscriptLines(entries []TranscriptEntry) []string {
	lines := []string{}
	for index, entry := range entries {
		if index > 0 {
			lines = append(lines, "", ansiBlue+ansiDim+"·"+ansiReset, "")
		}
		lines = append(lines, renderEntry(entry)...)
	}
	return lines
}

func renderEntry(entry TranscriptEntry) []string {
	switch entry.Kind {
	case EntryUser:
		return block(ansiCyan+ansiBold+"you"+ansiReset, entry.Body)
	case EntryAssistant:
		return block(ansiGreen+ansiBold+"assistant"+ansiReset, renderMarkdownish(entry.Body))
	case EntryProgress:
		return block(ansiYellow+ansiBold+"progress"+ansiReset, renderMarkdownish(entry.Body))
	case EntryTool:
		status := renderToolStatus(entry.Status)
		body := entry.Body
		if entry.Status == ToolRunning {
			body = entry.Body
		} else if entry.Collapsed {
			body = entry.CollapsedSummary
			if body == "" {
				body = "output collapsed"
			}
			body = ansiDim + body + ansiReset
		} else if entry.CollapsePhase > 0 {
			body = ansiDim + "collapsing" + strings.Repeat(".", entry.CollapsePhase) + ansiReset
		} else {
			body = previewToolBody(entry.ToolName, renderMarkdownish(body))
		}
		return block(ansiMagenta+ansiBold+"tool"+ansiReset+" "+entry.ToolName+" "+status, body)
	default:
		return block("entry", entry.Body)
	}
}

func renderToolStatus(status ToolStatus) string {
	switch status {
	case ToolRunning:
		return ansiYellow + "running" + ansiReset
	case ToolSuccess:
		return ansiGreen + "ok" + ansiReset
	case ToolError:
		return ansiRed + "err" + ansiReset
	default:
		return string(status)
	}
}

func previewToolBody(toolName, body string) string {
	maxChars := 1800
	maxLines := 36
	if toolName == "read_file" {
		maxChars = 1000
		maxLines = 20
	}
	lines := strings.Split(body, "\n")
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	limited := strings.Join(lines, "\n")
	if len(limited) > maxChars {
		limited = limited[:maxChars] + "..."
		truncated = true
	}
	if truncated {
		limited += "\n" + ansiDim + "... output truncated in transcript" + ansiReset
	}
	return limited
}

func renderMarkdownish(input string) string {
	lines := strings.Split(input, "\n")
	inCodeBlock := false
	for index, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			lines[index] = ansiDim + line + ansiReset
			continue
		}
		if inCodeBlock {
			lines[index] = ansiDim + line + ansiReset
			continue
		}
		switch {
		case strings.HasPrefix(line, "### "):
			line = ansiCyan + ansiBold + strings.TrimPrefix(line, "### ") + ansiReset
		case strings.HasPrefix(line, "## "):
			line = ansiCyan + ansiBold + strings.TrimPrefix(line, "## ") + ansiReset
		case strings.HasPrefix(line, "# "):
			line = ansiCyan + ansiBold + strings.TrimPrefix(line, "# ") + ansiReset
		case strings.HasPrefix(line, "> "):
			line = ansiDim + line + ansiReset
		case regexp.MustCompile(`^\s*[-*]\s+`).MatchString(line):
			line = regexp.MustCompile(`^\s*[-*]\s+`).ReplaceAllString(line, ansiYellow+"•"+ansiReset+" ")
		}
		line = regexp.MustCompile("`([^`]+)`").ReplaceAllString(line, ansiMagenta+"$1"+ansiReset)
		line = regexp.MustCompile(`\*\*([^*]+)\*\*`).ReplaceAllString(line, ansiBold+"$1"+ansiReset)
		lines[index] = line
	}
	return strings.Join(lines, "\n")
}

func block(header, body string) []string {
	lines := []string{header}
	for _, line := range strings.Split(body, "\n") {
		lines = append(lines, "  "+line)
	}
	return lines
}

func panelRow(text string, width int) string {
	inner := width - 4
	if displayWidth(text) > inner {
		text = truncateVisible(text, inner)
	}
	return ansiBorder + "│" + ansiReset + " " + text + strings.Repeat(" ", inner-displayWidth(text)) + " " + ansiBorder + "│" + ansiReset
}

func wrapPlain(text string, width int) []string {
	if width <= 0 || displayWidth(text) <= width {
		return []string{text}
	}
	if strings.Contains(text, "\x1b[") {
		return []string{truncateVisible(text, width)}
	}
	lines := []string{}
	for displayWidth(text) > width {
		head, rest := splitVisible(text, width)
		lines = append(lines, head)
		text = rest
	}
	if text != "" {
		lines = append(lines, text)
	}
	return lines
}

func padRight(text string, width int) string {
	if displayWidth(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-displayWidth(text))
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func visibleLen(text string) int {
	return displayWidth(text)
}

func truncateVisible(text string, width int) string {
	if width <= 0 {
		return ""
	}
	visible := 0
	var out strings.Builder
	for index := 0; index < len(text); {
		if text[index] == '\x1b' {
			end := index + 1
			for end < len(text) && text[end] != 'm' {
				end++
			}
			if end < len(text) {
				out.WriteString(text[index : end+1])
				index = end + 1
				continue
			}
		}
		r, size := utf8DecodeRuneInString(text[index:])
		runeWidth := runeDisplayWidth(r)
		if visible+runeWidth > width {
			break
		}
		out.WriteString(text[index : index+size])
		visible += runeWidth
		index += size
	}
	if strings.Contains(out.String(), "\x1b[") {
		out.WriteString(ansiReset)
	}
	return out.String()
}

func displayWidth(text string) int {
	plain := ansiPattern.ReplaceAllString(text, "")
	width := 0
	for _, r := range plain {
		width += runeDisplayWidth(r)
	}
	return width
}

func runeDisplayWidth(r rune) int {
	if r == 0 {
		return 0
	}
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if (r >= 0x1100 && r <= 0x115f) ||
		r == 0x2329 ||
		r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1faf6) ||
		(r >= 0x20000 && r <= 0x3fffd) {
		return 2
	}
	return 1
}

func splitVisible(text string, width int) (string, string) {
	visible := 0
	for index, r := range text {
		next := runeDisplayWidth(r)
		if visible+next > width {
			return text[:index], text[index:]
		}
		visible += next
	}
	return text, ""
}

func utf8DecodeRuneInString(text string) (rune, int) {
	for _, r := range text {
		return r, len(string(r))
	}
	return 0, 0
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
