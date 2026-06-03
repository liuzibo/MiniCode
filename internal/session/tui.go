package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ssbsunshengbo/minicode-go/internal/agent"
	"github.com/ssbsunshengbo/minicode-go/internal/commands"
	"github.com/ssbsunshengbo/minicode-go/internal/message"
	"github.com/ssbsunshengbo/minicode-go/internal/permissions"
	"github.com/ssbsunshengbo/minicode-go/internal/tui"
)

type transcriptEntry struct {
	kind           string
	body           string
	toolName       string
	status         string
	path           string
	editOperation  bool
	aggregateCount int
}

type tuiState struct {
	input           string
	cursor          int
	transcript      []transcriptEntry
	scroll          int
	status          string
	selected        int
	nextEntryID     int
	history         []string
	historyIndex    int
	historyDraft    string
	pendingApproval *approvalState
}

type approvalState struct {
	request       permissions.Request
	selected      int
	feedbackMode  bool
	feedbackInput string
	expanded      bool
	scroll        int
}

func (s *Session) RunTUI(ctx context.Context) error {
	historyEntries, _ := s.args.History.Load()
	state := tuiState{cursor: 0, nextEntryID: 1, history: historyEntries, historyIndex: len(historyEntries)}
	if err := enterRawMode(); err != nil {
		return s.Run(ctx)
	}
	defer exitRawMode()
	fmt.Fprint(s.args.Out, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(s.args.Out, "\x1b[?25h\x1b[?1049l")

	rest := ""
	var render func()
	render = func() {
		fmt.Fprint(s.args.Out, "\x1b[H\x1b[2J")
		fmt.Fprint(s.args.Out, s.renderTUIScreen(state, terminalWidth(), terminalHeight()))
	}
	if setter, ok := s.args.Permission.(interface{ SetPrompt(permissions.Prompt) }); ok {
		setter.SetPrompt(func(ctx context.Context, request permissions.Request) (permissions.PromptResult, error) {
			return s.runApprovalPrompt(ctx, &state, request, render)
		})
	}
	render()

	buffer := make([]byte, 64)
	for {
		n, err := s.args.In.Read(buffer)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		parsed := tui.ParseInputChunk(rest, string(buffer[:n]))
		rest = parsed.Rest
		for _, event := range parsed.Events {
			shouldExit, err := s.handleTUIEvent(ctx, &state, event)
			if err != nil {
				state.transcript = append(state.transcript, transcriptEntry{kind: "assistant", body: err.Error()})
			}
			if shouldExit {
				return nil
			}
		}
		render()
	}
}

func (s *Session) handleTUIEvent(ctx context.Context, state *tuiState, event tui.InputEvent) (bool, error) {
	if state.pendingApproval != nil {
		state.pendingApproval.handle(event)
		return false, nil
	}
	if event.Kind == tui.EventText && event.Ctrl && event.Text == "c" {
		return true, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyEscape {
		state.input = ""
		state.cursor = 0
		state.selected = 0
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyBackspace {
		if state.cursor > 0 {
			state.input = state.input[:state.cursor-1] + state.input[state.cursor:]
			state.cursor--
		}
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyLeft {
		if state.cursor > 0 {
			state.cursor--
		}
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyRight {
		if state.cursor < len(state.input) {
			state.cursor++
		}
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyPageUp {
		state.scroll += 8
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyPageDown {
		state.scroll -= 8
		if state.scroll < 0 {
			state.scroll = 0
		}
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyUp {
		visible := s.visibleSlashCommands(state.input)
		if len(visible) > 0 {
			state.selected = (state.selected - 1 + len(visible)) % len(visible)
		} else if historyUp(state) {
			return false, nil
		} else {
			state.scroll++
		}
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyDown {
		visible := s.visibleSlashCommands(state.input)
		if len(visible) > 0 {
			state.selected = (state.selected + 1) % len(visible)
		} else if historyDown(state) {
			return false, nil
		} else if state.scroll > 0 {
			state.scroll--
		}
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyTab {
		visible := s.visibleSlashCommands(state.input)
		if len(visible) > 0 {
			selected := visible[min(state.selected, len(visible)-1)]
			state.input = selected.Usage
			state.cursor = len(state.input)
			state.selected = 0
		}
		return false, nil
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyReturn {
		input := strings.TrimSpace(state.input)
		if input == "/exit" {
			return true, nil
		}
		if input != "" {
			state.transcript = append(state.transcript, transcriptEntry{kind: "user", body: input})
			if len(state.history) == 0 || state.history[len(state.history)-1] != input {
				state.history = append(state.history, input)
				_ = s.args.History.Save(state.history)
			}
			state.historyIndex = len(state.history)
			state.historyDraft = ""
			state.input = ""
			state.cursor = 0
			var err error
			if strings.HasPrefix(input, "/") || isShortcutInput(input) {
				var out strings.Builder
				previousOut := s.args.Out
				s.args.Out = &out
				err = s.RunOnce(ctx, input)
				s.args.Out = previousOut
				if strings.TrimSpace(out.String()) != "" {
					state.transcript = append(state.transcript, transcriptEntry{kind: "assistant", body: strings.TrimSpace(out.String())})
				}
			} else {
				err = s.runAgentForTUI(ctx, state, input)
			}
			state.scroll = 0
			return false, err
		}
	}
	if event.Kind == tui.EventText && !event.Ctrl {
		state.input = state.input[:state.cursor] + event.Text + state.input[state.cursor:]
		state.cursor += len(event.Text)
		state.selected = 0
		state.historyIndex = len(state.history)
	}
	return false, nil
}

func (s *Session) runAgentForTUI(ctx context.Context, state *tuiState, input string) error {
	messages := append(s.args.Messages, message.UserMessage(input))
	pendingToolEntries := map[string][]int{}
	next, err := agent.RunTurn(ctx, agent.Args{
		Model:      s.args.Model,
		Tools:      s.args.Tools,
		Messages:   messages,
		CWD:        s.args.CWD,
		Permission: s.args.Permission,
		OnProgressMessage: func(content string) {
			state.transcript = append(state.transcript, transcriptEntry{kind: "progress", body: content})
		},
		OnToolStart: func(name string, input any) {
			index := appendToolStart(state, name, input)
			pendingToolEntries[name] = append(pendingToolEntries[name], index)
		},
		OnToolResult: func(name, output string, isError bool) {
			finishToolResult(state, pendingToolEntries, name, output, isError)
		},
		OnAssistant: func(content string) {
			state.transcript = append(state.transcript, transcriptEntry{kind: "assistant", body: content})
		},
	})
	if err != nil {
		return err
	}
	s.args.Messages = next
	return s.persistMessages()
}

func (s *Session) renderTUIScreen(state tuiState, width, height int) string {
	if width < 60 {
		width = 60
	}
	if height < 20 {
		height = 20
	}
	header := tui.RenderPanel("MiniCode", s.renderHeaderBody(len(state.transcript)), tui.PanelOptions{Width: width, RightTitle: "Go"})
	if state.pendingApproval != nil {
		approval := tui.RenderPanel("approval", renderApprovalPrompt(*state.pendingApproval, height-8), tui.PanelOptions{Width: width, MinBodyLines: max(8, height-12)})
		return strings.Join([]string{header, "", approval, "", "Waiting for approval..."}, "\n")
	}
	entries := make([]tui.TranscriptEntry, 0, len(state.transcript))
	for index, entry := range state.transcript {
		entries = append(entries, tui.TranscriptEntry{
			ID:               index + 1,
			Kind:             mapEntryKind(entry.kind),
			Body:             entry.body,
			ToolName:         entry.toolName,
			Status:           mapToolStatus(entry.status),
			Collapsed:        entry.status == "success",
			CollapsedSummary: summarizeSuccessfulToolEntry(entry),
		})
	}
	transcriptHeight := height - 13
	if transcriptHeight < 6 {
		transcriptHeight = 6
	}
	feed := tui.RenderTranscript(entries, state.scroll, transcriptHeight)
	if feed == "" {
		feed = "Ready\n\nType /help for commands."
	}
	feedPanel := tui.RenderPanel("session feed", feed, tui.PanelOptions{Width: width, RightTitle: fmt.Sprintf("%d events", len(entries)), MinBodyLines: transcriptHeight})
	promptBody := tui.RenderInputPrompt(state.input, state.cursor)
	if visible := s.visibleSlashCommands(state.input); len(visible) > 0 {
		promptBody += "\n" + tui.RenderSlashMenu(visible, state.selected)
	}
	promptPanel := tui.RenderPanel("prompt", promptBody, tui.PanelOptions{Width: width})
	return strings.Join([]string{header, "", feedPanel, "", promptPanel, "", "Ready"}, "\n")
}

func (s *Session) renderHeaderBody(eventCount int) string {
	model := "mock/offline"
	if s.args.Runtime != nil {
		model = s.args.Runtime.Model
	}
	return strings.Join([]string{
		"Terminal coding assistant with a card-style session layout.",
		"",
		s.args.CWD,
		fmt.Sprintf("[session] local  [model] %s  [messages] %d  [events] %d  [skills] %d  [mcp] %d", model, len(s.args.Messages), eventCount, len(s.args.Tools.Skills()), len(s.args.Tools.MCPServers())),
	}, "\n")
}

func (s *Session) visibleSlashCommands(input string) []tui.SlashCommand {
	if !strings.HasPrefix(input, "/") {
		return nil
	}
	matches := commands.SlashCommands
	if input != "/" {
		usages := map[string]bool{}
		for _, usage := range commands.FindMatching(input) {
			usages[usage] = true
		}
		matches = nil
		for _, command := range commands.SlashCommands {
			if usages[command.Usage] {
				matches = append(matches, command)
			}
		}
	}
	out := make([]tui.SlashCommand, 0, len(matches))
	for _, command := range matches {
		out = append(out, tui.SlashCommand{Usage: command.Usage, Description: command.Description})
	}
	return out
}

func mapEntryKind(kind string) tui.EntryKind {
	switch kind {
	case "user":
		return tui.EntryUser
	case "assistant":
		return tui.EntryAssistant
	case "progress":
		return tui.EntryProgress
	case "tool":
		return tui.EntryTool
	default:
		return tui.EntryAssistant
	}
}

func mapToolStatus(status string) tui.ToolStatus {
	switch status {
	case "running":
		return tui.ToolRunning
	case "error":
		return tui.ToolError
	default:
		return tui.ToolSuccess
	}
}

func enterRawMode() error {
	return exec.Command("stty", "raw", "-echo").Run()
}

func exitRawMode() {
	_ = exec.Command("stty", "sane").Run()
}

func terminalWidth() int {
	width, _ := terminalSize()
	return width
}

func terminalHeight() int {
	_, height := terminalSize()
	return height
}

func terminalSize() (int, int) {
	output, err := exec.Command("stty", "size").Output()
	if err != nil {
		return 100, 40
	}
	width, height := parseTerminalSize(string(output))
	if width <= 0 || height <= 0 {
		return 100, 40
	}
	return width, height
}

func (s *Session) runApprovalPrompt(ctx context.Context, state *tuiState, request permissions.Request, render func()) (permissions.PromptResult, error) {
	approval := &approvalState{request: request}
	state.pendingApproval = approval
	render()
	defer func() {
		state.pendingApproval = nil
		render()
	}()

	rest := ""
	buffer := make([]byte, 64)
	for {
		select {
		case <-ctx.Done():
			return permissions.PromptResult{}, ctx.Err()
		default:
		}
		n, err := s.args.In.Read(buffer)
		if err != nil {
			if err == io.EOF {
				return permissions.PromptResult{Decision: permissions.DecisionDenyOnce}, nil
			}
			return permissions.PromptResult{}, err
		}
		parsed := tui.ParseInputChunk(rest, string(buffer[:n]))
		rest = parsed.Rest
		for _, event := range parsed.Events {
			done, result := approval.handle(event)
			render()
			if done {
				return result, nil
			}
		}
	}
}

func (a *approvalState) handle(event tui.InputEvent) (bool, permissions.PromptResult) {
	if event.Kind == tui.EventKey && event.Name == tui.KeyEscape {
		if a.feedbackMode {
			a.feedbackMode = false
			a.feedbackInput = ""
			return false, permissions.PromptResult{}
		}
		return true, permissions.PromptResult{Decision: permissions.DecisionDenyOnce}
	}
	if event.Kind == tui.EventText && event.Ctrl && event.Text == "o" {
		a.expanded = !a.expanded
		a.scroll = 0
		return false, permissions.PromptResult{}
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyPageUp {
		a.scroll -= 8
		if a.scroll < 0 {
			a.scroll = 0
		}
		return false, permissions.PromptResult{}
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyPageDown {
		a.scroll += 8
		return false, permissions.PromptResult{}
	}
	if a.feedbackMode {
		if event.Kind == tui.EventKey && event.Name == tui.KeyBackspace {
			if len(a.feedbackInput) > 0 {
				a.feedbackInput = a.feedbackInput[:len(a.feedbackInput)-1]
			}
			return false, permissions.PromptResult{}
		}
		if event.Kind == tui.EventText && !event.Ctrl {
			a.feedbackInput += event.Text
			return false, permissions.PromptResult{}
		}
		if event.Kind == tui.EventKey && event.Name == tui.KeyReturn {
			return true, permissions.PromptResult{Decision: permissions.DecisionDenyWithFeedback, Feedback: strings.TrimSpace(a.feedbackInput)}
		}
		return false, permissions.PromptResult{}
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyUp {
		if len(a.request.Choices) > 0 {
			a.selected = (a.selected - 1 + len(a.request.Choices)) % len(a.request.Choices)
		}
		return false, permissions.PromptResult{}
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyDown {
		if len(a.request.Choices) > 0 {
			a.selected = (a.selected + 1) % len(a.request.Choices)
		}
		return false, permissions.PromptResult{}
	}
	if event.Kind == tui.EventKey && event.Name == tui.KeyReturn {
		if len(a.request.Choices) == 0 {
			return true, permissions.PromptResult{Decision: permissions.DecisionDenyOnce}
		}
		choice := a.request.Choices[min(a.selected, len(a.request.Choices)-1)]
		if choice.Decision == permissions.DecisionDenyWithFeedback {
			a.feedbackMode = true
			a.feedbackInput = ""
			return false, permissions.PromptResult{}
		}
		return true, permissions.PromptResult{Decision: choice.Decision}
	}
	return false, permissions.PromptResult{}
}

func renderApprovalPrompt(state approvalState, height int) string {
	lines := []string{
		"Approval Required",
		state.request.Summary,
	}
	details := flattenDetails(state.request.Details)
	limit := 16
	if state.expanded {
		limit = max(8, height-10)
	}
	start := state.scroll
	if start > len(details) {
		start = len(details)
	}
	end := start + limit
	if end > len(details) {
		end = len(details)
	}
	lines = append(lines, details[start:end]...)
	if !state.expanded && len(details) > end {
		lines = append(lines, fmt.Sprintf("... %d more line(s) hidden", len(details)-end), "Ctrl+O expand full details")
	} else if state.expanded {
		lines = append(lines, "Ctrl+O collapse | PgUp/PgDn scroll")
	}
	lines = append(lines, "")
	if state.feedbackMode {
		lines = append(lines, "Reject With Guidance", "Type feedback for model, Enter submit, Esc back", "> "+state.feedbackInput)
	} else {
		for index, choice := range state.request.Choices {
			prefix := "  "
			if index == state.selected {
				prefix = "> "
			}
			lines = append(lines, prefix+choice.Label)
		}
		lines = append(lines, "", "Use Up/Down to select, Enter confirm, Esc deny once")
	}
	return strings.Join(lines, "\n")
}

func flattenDetails(details []string) []string {
	lines := []string{}
	for index, detail := range details {
		if index > 0 {
			lines = append(lines, "")
		}
		if looksLikeUnifiedDiff(detail) {
			detail = tui.RenderUnifiedDiff(detail)
		}
		lines = append(lines, strings.Split(detail, "\n")...)
	}
	return lines
}

func looksLikeUnifiedDiff(detail string) bool {
	return strings.Contains(detail, "\n+++ ") && strings.Contains(detail, "\n@@ ")
}

func parseTerminalSize(output string) (int, int) {
	parts := strings.Fields(output)
	if len(parts) != 2 {
		return 0, 0
	}
	rows, rowErr := strconv.Atoi(parts[0])
	cols, colErr := strconv.Atoi(parts[1])
	if rowErr != nil || colErr != nil {
		return 0, 0
	}
	return cols, rows
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func historyUp(state *tuiState) bool {
	if len(state.history) == 0 || state.historyIndex <= 0 {
		return false
	}
	if state.historyIndex == len(state.history) {
		state.historyDraft = state.input
	}
	state.historyIndex--
	state.input = state.history[state.historyIndex]
	state.cursor = len(state.input)
	return true
}

func historyDown(state *tuiState) bool {
	if state.historyIndex >= len(state.history) {
		return false
	}
	state.historyIndex++
	if state.historyIndex == len(state.history) {
		state.input = state.historyDraft
	} else {
		state.input = state.history[state.historyIndex]
	}
	state.cursor = len(state.input)
	return true
}

func isShortcutInput(input string) bool {
	_, ok := commands.ParseShortcut(input)
	return ok
}

func appendToolStart(state *tuiState, toolName string, input any) int {
	summary, path, isEdit := summarizeToolInputDetails(toolName, input)
	state.transcript = append(state.transcript, transcriptEntry{
		kind:           "tool",
		toolName:       toolName,
		status:         "running",
		body:           summary,
		path:           path,
		editOperation:  isEdit,
		aggregateCount: boolToCount(isEdit),
	})
	return len(state.transcript) - 1
}

func finishToolResult(state *tuiState, pending map[string][]int, toolName, output string, isError bool) {
	status := "success"
	if isError {
		status = "error"
	}
	index := -1
	if queue := pending[toolName]; len(queue) > 0 {
		index = queue[0]
		pending[toolName] = queue[1:]
		if len(pending[toolName]) == 0 {
			delete(pending, toolName)
		}
	}
	if index < 0 || index >= len(state.transcript) {
		state.transcript = append(state.transcript, transcriptEntry{kind: "tool", toolName: toolName, status: status, body: output})
		return
	}
	state.transcript[index].status = status
	state.transcript[index].body = output
	if status == "success" {
		aggregateConsecutiveEdit(state, index)
	}
}

func aggregateConsecutiveEdit(state *tuiState, index int) {
	if index <= 0 || index >= len(state.transcript) {
		return
	}
	current := state.transcript[index]
	previous := state.transcript[index-1]
	if !current.editOperation || !previous.editOperation || current.path == "" || previous.path != current.path || previous.status != "success" || current.status != "success" {
		return
	}
	count := previous.aggregateCount
	if count <= 0 {
		count = 1
	}
	currentCount := current.aggregateCount
	if currentCount <= 0 {
		currentCount = 1
	}
	previous.aggregateCount = count + currentCount
	previous.toolName = "file edits"
	previous.body = fmt.Sprintf("%d edit operations applied to %s", previous.aggregateCount, previous.path)
	state.transcript[index-1] = previous
	state.transcript = append(state.transcript[:index], state.transcript[index+1:]...)
}

func summarizeToolInput(toolName string, input any) string {
	summary, _, _ := summarizeToolInputDetails(toolName, input)
	return summary
}

func summarizeToolInputDetails(toolName string, input any) (string, string, bool) {
	if typed, ok := input.(map[string]any); ok {
		if path, ok := typed["path"].(string); ok && path != "" {
			return toolName + " path=" + path, path, isEditTool(toolName)
		}
		if command, ok := typed["command"].(string); ok && command != "" {
			return toolName + " command=" + command, "", false
		}
	}
	return toolName, "", false
}

func summarizeSuccessfulToolEntry(entry transcriptEntry) string {
	if entry.aggregateCount > 1 && entry.path != "" {
		return fmt.Sprintf("%d edit operations applied to %s", entry.aggregateCount, entry.path)
	}
	firstLine := strings.TrimSpace(strings.Split(entry.body, "\n")[0])
	if firstLine == "" {
		firstLine = "completed"
	}
	if len(firstLine) > 96 {
		firstLine = firstLine[:93] + "..."
	}
	return entry.toolName + " completed: " + firstLine
}

func isEditTool(toolName string) bool {
	switch toolName {
	case "write_file", "modify_file", "edit_file", "patch_file":
		return true
	default:
		return false
	}
}

func boolToCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
