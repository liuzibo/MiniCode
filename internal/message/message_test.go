package message

import "testing"

func TestMessageConstructors(t *testing.T) {
	assistant := AssistantMessage("hello")
	if assistant.Role != RoleAssistant || assistant.Content != "hello" {
		t.Fatalf("unexpected assistant message: %#v", assistant)
	}

	progress := ProgressMessage("working")
	if progress.Role != RoleAssistantProgress || progress.Content != "working" {
		t.Fatalf("unexpected progress message: %#v", progress)
	}
}

func TestStepConstructors(t *testing.T) {
	call := ToolCall{ID: "u1", ToolName: "read_file", Input: map[string]any{"path": "README.md"}}
	step := ToolCallsStep([]ToolCall{call}, "checking", ContentProgress, Diagnostics{StopReason: "tool_use"})

	if step.Type != StepToolCalls || len(step.Calls) != 1 {
		t.Fatalf("unexpected tool step: %#v", step)
	}
	if step.Calls[0].ToolName != "read_file" || step.ContentKind != ContentProgress {
		t.Fatalf("unexpected tool call content: %#v", step)
	}
}
