package session

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/ssbsunshengbo/minicode-go/internal/message"
	"github.com/ssbsunshengbo/minicode-go/internal/model"
	"github.com/ssbsunshengbo/minicode-go/internal/permissions"
	"github.com/ssbsunshengbo/minicode-go/internal/tools"
	"github.com/ssbsunshengbo/minicode-go/internal/tui"
)

var tuiAnsiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestRenderTUIScreenIncludesTranscriptAndPrompt(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	s := New(Args{
		CWD:      dir,
		Tools:    tools.NewRegistry(nil, tools.Metadata{}),
		Model:    model.Mock{},
		Messages: []message.Message{message.SystemMessage("system")},
		Out:      &out,
	})
	screen := s.renderTUIScreen(tuiState{
		input:  "hello",
		cursor: 5,
		transcript: []transcriptEntry{
			{kind: "user", body: "hello"},
			{kind: "assistant", body: "world"},
		},
	}, 80, 30)
	plain := strings.ReplaceAll(tuiAnsiPattern.ReplaceAllString(screen, ""), " ", "")
	for _, want := range []string{"MiniCode", "sessionfeed", "you", "assistant", "mini-code>hello|"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("screen missing %q:\n%s", want, screen)
		}
	}
}

func TestTUIHistoryNavigation(t *testing.T) {
	s := New(Args{Tools: tools.NewRegistry(nil, tools.Metadata{}), Model: model.Mock{}})
	state := tuiState{history: []string{"first", "second"}, historyIndex: 2}

	if _, err := s.handleTUIEvent(context.Background(), &state, tui.InputEvent{Kind: tui.EventKey, Name: tui.KeyUp}); err != nil {
		t.Fatal(err)
	}
	if state.input != "second" || state.cursor != len("second") {
		t.Fatalf("unexpected history up state: %#v", state)
	}

	if _, err := s.handleTUIEvent(context.Background(), &state, tui.InputEvent{Kind: tui.EventKey, Name: tui.KeyDown}); err != nil {
		t.Fatal(err)
	}
	if state.input != "" || state.cursor != 0 {
		t.Fatalf("unexpected history down state: %#v", state)
	}
}

func TestTUIPlainInputAddsProgressAndToolEntries(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry([]tools.Definition{
		{
			Name: "demo_tool",
			Run: func(_ context.Context, _ json.RawMessage, _ tools.Context) tools.Result {
				return tools.Success("tool output")
			},
		},
	}, tools.Metadata{})
	s := New(Args{
		CWD:      dir,
		Tools:    registry,
		Model:    scriptedTUIModel{},
		Messages: []message.Message{message.SystemMessage("system")},
	})
	state := tuiState{input: "do work", cursor: len("do work"), historyIndex: 0}

	if _, err := s.handleTUIEvent(context.Background(), &state, tui.InputEvent{Kind: tui.EventKey, Name: tui.KeyReturn}); err != nil {
		t.Fatal(err)
	}

	kinds := []string{}
	for _, entry := range state.transcript {
		kinds = append(kinds, entry.kind)
	}
	got := strings.Join(kinds, ",")
	if !strings.Contains(got, "user,progress,tool,assistant") {
		t.Fatalf("unexpected transcript kinds %q entries=%#v", got, state.transcript)
	}
}

func TestTUIAgentTurnAggregatesSameFileEditTools(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry([]tools.Definition{
		{
			Name: "edit_file",
			Run: func(_ context.Context, _ json.RawMessage, _ tools.Context) tools.Result {
				return tools.Success("Applied reviewed changes to main.go")
			},
		},
		{
			Name: "patch_file",
			Run: func(_ context.Context, _ json.RawMessage, _ tools.Context) tools.Result {
				return tools.Success("Patched main.go with 2 replacement(s)")
			},
		},
	}, tools.Metadata{})
	s := New(Args{
		CWD:      dir,
		Tools:    registry,
		Model:    editAggregationTUIModel{},
		Messages: []message.Message{message.SystemMessage("system")},
	})
	state := tuiState{input: "edit twice", cursor: len("edit twice"), historyIndex: 0}

	if _, err := s.handleTUIEvent(context.Background(), &state, tui.InputEvent{Kind: tui.EventKey, Name: tui.KeyReturn}); err != nil {
		t.Fatal(err)
	}

	toolEntries := 0
	for _, entry := range state.transcript {
		if entry.kind == "tool" {
			toolEntries++
			if entry.toolName != "file edits" || entry.aggregateCount != 2 || entry.path != "main.go" {
				t.Fatalf("unexpected aggregated tool entry: %#v", entry)
			}
		}
	}
	if toolEntries != 1 {
		t.Fatalf("expected one aggregated tool entry, got %d entries=%#v", toolEntries, state.transcript)
	}
}

func TestRenderTUIScreenCollapsesSuccessfulToolOutputToSummary(t *testing.T) {
	s := New(Args{CWD: t.TempDir(), Tools: tools.NewRegistry(nil, tools.Metadata{}), Model: model.Mock{}})
	screen := s.renderTUIScreen(tuiState{
		transcript: []transcriptEntry{
			{kind: "tool", toolName: "read_file", status: "success", body: "FILE: main.go\nOFFSET: 0\nEND: 10\nTOTAL_CHARS: 100\nTRUNCATED: yes - call read_file again with offset 10\n\npackage main"},
		},
	}, 100, 30)
	if !strings.Contains(screen, "read_file completed: FILE: main.go") {
		t.Fatalf("expected collapsed tool summary:\n%s", screen)
	}
	if strings.Contains(screen, "package main") {
		t.Fatalf("expected successful tool body to be collapsed:\n%s", screen)
	}
}

func TestFinishToolResultAggregatesConsecutiveEditsForSameFile(t *testing.T) {
	state := tuiState{}

	index := appendToolStart(&state, "edit_file", map[string]any{"path": "main.go"})
	finishToolResult(&state, map[string][]int{"edit_file": []int{index}}, "edit_file", "Applied reviewed changes to main.go", false)
	index = appendToolStart(&state, "patch_file", map[string]any{"path": "main.go"})
	finishToolResult(&state, map[string][]int{"patch_file": []int{index}}, "patch_file", "Patched main.go with 2 replacement(s)", false)

	if len(state.transcript) != 1 {
		t.Fatalf("expected aggregated edit entry, got %#v", state.transcript)
	}
	entry := state.transcript[0]
	if entry.toolName != "file edits" || entry.aggregateCount != 2 || !strings.Contains(entry.body, "2 edit operations applied to main.go") {
		t.Fatalf("unexpected aggregate entry: %#v", entry)
	}
}

func TestFinishToolResultDoesNotCollapseErrors(t *testing.T) {
	state := tuiState{}
	index := appendToolStart(&state, "run_command", map[string]any{"command": "go", "args": []any{"test", "./..."}})
	finishToolResult(&state, map[string][]int{"run_command": []int{index}}, "run_command", "compiler error\nline 2", true)
	screen := New(Args{CWD: t.TempDir(), Tools: tools.NewRegistry(nil, tools.Metadata{}), Model: model.Mock{}}).renderTUIScreen(state, 100, 30)
	if !strings.Contains(screen, "compiler error") || !strings.Contains(screen, "line 2") {
		t.Fatalf("expected error output to stay expanded:\n%s", screen)
	}
}

func TestParseTerminalSize(t *testing.T) {
	width, height := parseTerminalSize("40 120\n")
	if width != 120 || height != 40 {
		t.Fatalf("got width=%d height=%d", width, height)
	}
}

func TestRenderTUIScreenShowsApprovalPanel(t *testing.T) {
	s := New(Args{CWD: t.TempDir(), Tools: tools.NewRegistry(nil, tools.Metadata{}), Model: model.Mock{}})
	screen := s.renderTUIScreen(tuiState{
		pendingApproval: &approvalState{
			request: permissions.Request{
				Kind:    permissions.KindEdit,
				Summary: "mini-code wants to apply a file modification",
				Details: []string{"target: file.txt", "", "--- a/file.txt\n+++ b/file.txt"},
				Choices: []permissions.Choice{
					{Label: "apply once", Decision: permissions.DecisionAllowOnce},
					{Label: "reject once", Decision: permissions.DecisionDenyOnce},
				},
			},
		},
	}, 80, 30)
	for _, want := range []string{"approval", "Approval Required", "apply once", "reject once"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("approval screen missing %q:\n%s", want, screen)
		}
	}
}

func TestRenderApprovalPromptHighlightsUnifiedDiff(t *testing.T) {
	out := renderApprovalPrompt(approvalState{
		request: permissions.Request{
			Summary: "edit file",
			Details: []string{"target: file.txt", "--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-oldName\n+newName"},
			Choices: []permissions.Choice{{Label: "allow", Decision: permissions.DecisionAllowOnce}},
		},
	}, 30)
	for _, want := range []string{"\x1b[36m--- a/file.txt", "\x1b[35m@@ -1 +1 @@", "\x1b[1mold", "\x1b[1mnew"} {
		if !strings.Contains(out, want) {
			t.Fatalf("approval prompt missing highlighted diff %q:\n%q", want, out)
		}
	}
}

func TestApprovalSelectionAndFeedback(t *testing.T) {
	state := approvalState{request: permissions.Request{Choices: []permissions.Choice{
		{Label: "allow", Decision: permissions.DecisionAllowOnce},
		{Label: "reject with guidance", Decision: permissions.DecisionDenyWithFeedback},
	}}}

	state.handle(tui.InputEvent{Kind: tui.EventKey, Name: tui.KeyDown})
	if state.selected != 1 {
		t.Fatalf("expected selected=1 got %d", state.selected)
	}
	done, result := state.handle(tui.InputEvent{Kind: tui.EventKey, Name: tui.KeyReturn})
	if done || !state.feedbackMode || result.Decision != "" {
		t.Fatalf("expected feedback mode, done=%v state=%#v result=%#v", done, state, result)
	}
	state.handle(tui.InputEvent{Kind: tui.EventText, Text: "n"})
	state.handle(tui.InputEvent{Kind: tui.EventText, Text: "o"})
	done, result = state.handle(tui.InputEvent{Kind: tui.EventKey, Name: tui.KeyReturn})
	if !done || result.Decision != permissions.DecisionDenyWithFeedback || result.Feedback != "no" {
		t.Fatalf("unexpected feedback result done=%v result=%#v", done, result)
	}
}

type scriptedTUIModel struct {
	calls int
}

func (m scriptedTUIModel) Next(_ context.Context, messages []message.Message) (message.Step, error) {
	for _, msg := range messages {
		if msg.Role == message.RoleToolResult {
			return message.AssistantStep("done", message.ContentFinal, message.Diagnostics{}), nil
		}
	}
	return message.ToolCallsStep(
		[]message.ToolCall{{ID: "toolu_1", ToolName: "demo_tool", Input: map[string]any{}}},
		"working",
		message.ContentProgress,
		message.Diagnostics{},
	), nil
}

type editAggregationTUIModel struct{}

func (m editAggregationTUIModel) Next(_ context.Context, messages []message.Message) (message.Step, error) {
	toolResults := 0
	for _, msg := range messages {
		if msg.Role == message.RoleToolResult {
			toolResults++
		}
	}
	if toolResults >= 2 {
		return message.AssistantStep("done", message.ContentFinal, message.Diagnostics{}), nil
	}
	return message.ToolCallsStep(
		[]message.ToolCall{
			{ID: "toolu_1", ToolName: "edit_file", Input: map[string]any{"path": "main.go"}},
			{ID: "toolu_2", ToolName: "patch_file", Input: map[string]any{"path": "main.go"}},
		},
		"working",
		message.ContentProgress,
		message.Diagnostics{},
	), nil
}
