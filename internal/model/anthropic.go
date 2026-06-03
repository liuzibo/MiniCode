package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ssbsunshengbo/minicode-go/internal/config"
	"github.com/ssbsunshengbo/minicode-go/internal/message"
	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

type RuntimeProvider func(context.Context) (config.Runtime, error)

type Anthropic struct {
	runtime RuntimeProvider
	tools   *tools.Registry
	client  *http.Client
}

func NewAnthropic(runtime RuntimeProvider, registry *tools.Registry) *Anthropic {
	return &Anthropic{runtime: runtime, tools: registry, client: http.DefaultClient}
}

func (a *Anthropic) Next(ctx context.Context, messages []message.Message) (message.Step, error) {
	runtime, err := a.runtime(ctx)
	if err != nil {
		return message.Step{}, err
	}
	payload := toAnthropicPayload(runtime, a.tools, messages)
	body, err := json.Marshal(payload)
	if err != nil {
		return message.Step{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(runtime.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return message.Step{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if runtime.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+runtime.AuthToken)
	} else if runtime.APIKey != "" {
		req.Header.Set("x-api-key", runtime.APIKey)
	}

	res, err := a.client.Do(req)
	if err != nil {
		return message.Step{}, err
	}
	defer res.Body.Close()

	var data struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		StopReason string         `json:"stop_reason"`
		Content    []contentBlock `json:"content"`
	}
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return message.Step{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if data.Error != nil && data.Error.Message != "" {
			return message.Step{}, fmt.Errorf(data.Error.Message)
		}
		return message.Step{}, fmt.Errorf("Model request failed: %d", res.StatusCode)
	}

	textParts := []string{}
	calls := []message.ToolCall{}
	blockTypes := []string{}
	ignored := []string{}
	ignoredSeen := map[string]bool{}
	for _, block := range data.Content {
		blockTypes = append(blockTypes, block.Type)
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			calls = append(calls, message.ToolCall{ID: block.ID, ToolName: block.Name, Input: block.Input})
		default:
			if !ignoredSeen[block.Type] {
				ignoredSeen[block.Type] = true
				ignored = append(ignored, block.Type)
			}
		}
	}

	content, kind := ParseAssistantText(strings.TrimSpace(strings.Join(textParts, "\n")))
	diagnostics := message.Diagnostics{StopReason: data.StopReason, BlockTypes: blockTypes, IgnoredBlockTypes: ignored}
	if len(calls) > 0 {
		contentKind := message.ContentNone
		if kind == message.ContentProgress {
			contentKind = message.ContentProgress
		}
		return message.ToolCallsStep(calls, content, contentKind, diagnostics), nil
	}
	return message.AssistantStep(content, kind, diagnostics), nil
}

type contentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

func ParseAssistantText(content string) (string, message.ContentKind) {
	trimmed := strings.TrimSpace(content)
	markers := []struct {
		prefix string
		kind   message.ContentKind
		close  string
	}{
		{"<final>", message.ContentFinal, "</final>"},
		{"[FINAL]", message.ContentFinal, ""},
		{"<progress>", message.ContentProgress, "</progress>"},
		{"[PROGRESS]", message.ContentProgress, ""},
	}
	for _, marker := range markers {
		if strings.HasPrefix(trimmed, marker.prefix) {
			out := strings.TrimSpace(strings.TrimPrefix(trimmed, marker.prefix))
			if marker.close != "" {
				out = strings.TrimSpace(strings.ReplaceAll(out, marker.close, ""))
			}
			return out, marker.kind
		}
	}
	return trimmed, message.ContentNone
}

func toAnthropicPayload(runtime config.Runtime, registry *tools.Registry, messages []message.Message) map[string]any {
	systemParts := []string{}
	apiMessages := []map[string]any{}
	for _, msg := range messages {
		switch msg.Role {
		case message.RoleSystem:
			systemParts = append(systemParts, msg.Content)
		case message.RoleUser:
			apiMessages = appendAnthropicBlock(apiMessages, "user", map[string]any{"type": "text", "text": msg.Content})
		case message.RoleAssistant, message.RoleAssistantProgress:
			text := msg.Content
			if msg.Role == message.RoleAssistantProgress {
				text = "<progress>\n" + msg.Content + "\n</progress>"
			}
			apiMessages = appendAnthropicBlock(apiMessages, "assistant", map[string]any{"type": "text", "text": text})
		case message.RoleAssistantToolCall:
			apiMessages = appendAnthropicBlock(apiMessages, "assistant", map[string]any{"type": "tool_use", "id": msg.ToolUseID, "name": msg.ToolName, "input": msg.Input})
		case message.RoleToolResult:
			apiMessages = appendAnthropicBlock(apiMessages, "user", map[string]any{"type": "tool_result", "tool_use_id": msg.ToolUseID, "content": msg.Content, "is_error": msg.IsError})
		}
	}
	toolSchemas := []map[string]any{}
	for _, definition := range registry.List() {
		toolSchemas = append(toolSchemas, map[string]any{
			"name":         definition.Name,
			"description":  definition.Description,
			"input_schema": definition.InputSchema,
		})
	}
	payload := map[string]any{
		"model":    runtime.Model,
		"system":   strings.Join(systemParts, "\n\n"),
		"messages": apiMessages,
		"tools":    toolSchemas,
	}
	if runtime.MaxOutputTokens > 0 {
		payload["max_tokens"] = runtime.MaxOutputTokens
	}
	return payload
}

func appendAnthropicBlock(messages []map[string]any, role string, block map[string]any) []map[string]any {
	if len(messages) > 0 && messages[len(messages)-1]["role"] == role {
		content := messages[len(messages)-1]["content"].([]map[string]any)
		messages[len(messages)-1]["content"] = append(content, block)
		return messages
	}
	return append(messages, map[string]any{"role": role, "content": []map[string]any{block}})
}
