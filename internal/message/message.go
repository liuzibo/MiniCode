package message

import "context"

type Role string

const (
	RoleSystem            Role = "system"
	RoleUser              Role = "user"
	RoleAssistant         Role = "assistant"
	RoleAssistantProgress Role = "assistant_progress"
	RoleAssistantToolCall Role = "assistant_tool_call"
	RoleToolResult        Role = "tool_result"
)

type Message struct {
	Role      Role
	Content   string
	ToolUseID string
	ToolName  string
	Input     any
	IsError   bool
}

func SystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

func UserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

func AssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

func ProgressMessage(content string) Message {
	return Message{Role: RoleAssistantProgress, Content: content}
}

func AssistantToolCallMessage(call ToolCall) Message {
	return Message{
		Role:      RoleAssistantToolCall,
		ToolUseID: call.ID,
		ToolName:  call.ToolName,
		Input:     call.Input,
	}
}

func ToolResultMessage(toolUseID, toolName, content string, isError bool) Message {
	return Message{
		Role:      RoleToolResult,
		ToolUseID: toolUseID,
		ToolName:  toolName,
		Content:   content,
		IsError:   isError,
	}
}

type ToolCall struct {
	ID       string
	ToolName string
	Input    any
}

type Diagnostics struct {
	StopReason        string
	BlockTypes        []string
	IgnoredBlockTypes []string
}

type StepType string

const (
	StepAssistant StepType = "assistant"
	StepToolCalls StepType = "tool_calls"
)

type ContentKind string

const (
	ContentNone     ContentKind = ""
	ContentFinal    ContentKind = "final"
	ContentProgress ContentKind = "progress"
)

type Step struct {
	Type        StepType
	Content     string
	Kind        ContentKind
	Calls       []ToolCall
	ContentKind ContentKind
	Diagnostics Diagnostics
}

func AssistantStep(content string, kind ContentKind, diagnostics Diagnostics) Step {
	return Step{
		Type:        StepAssistant,
		Content:     content,
		Kind:        kind,
		Diagnostics: diagnostics,
	}
}

func ToolCallsStep(calls []ToolCall, content string, contentKind ContentKind, diagnostics Diagnostics) Step {
	return Step{
		Type:        StepToolCalls,
		Calls:       calls,
		Content:     content,
		ContentKind: contentKind,
		Diagnostics: diagnostics,
	}
}

type Model interface {
	Next(ctx context.Context, messages []Message) (Step, error)
}
