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

type OpenAI struct {
	runtime RuntimeProvider
	tools   *tools.Registry
	client  *http.Client
}

func NewOpenAI(runtime RuntimeProvider, registry *tools.Registry) *OpenAI {
	return &OpenAI{runtime: runtime, tools: registry, client: http.DefaultClient}
}

func (o *OpenAI) Next(ctx context.Context, messages []message.Message) (message.Step, error) {
	runtime, err := o.runtime(ctx)
	if err != nil {
		return message.Step{}, err
	}
	body, err := json.Marshal(toOpenAIPayload(runtime, o.tools, messages))
	if err != nil {
		return message.Step{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(runtime.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return message.Step{}, err
	}
	req.Header.Set("content-type", "application/json")
	if runtime.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+runtime.AuthToken)
	} else if runtime.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+runtime.APIKey)
	}

	res, err := o.client.Do(req)
	if err != nil {
		return message.Step{}, err
	}
	defer res.Body.Close()

	var data openAIResponse
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		return message.Step{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if data.Error.Message != "" {
			return message.Step{}, fmt.Errorf(data.Error.Message)
		}
		return message.Step{}, fmt.Errorf("Model request failed: %d", res.StatusCode)
	}
	if len(data.Choices) == 0 {
		return message.AssistantStep("", message.ContentNone, message.Diagnostics{StopReason: "empty_choices"}), nil
	}

	choice := data.Choices[0]
	content, kind := ParseAssistantText(strings.TrimSpace(choice.Message.Content))
	calls := []message.ToolCall{}
	for _, call := range choice.Message.ToolCalls {
		input := any(map[string]any{})
		if strings.TrimSpace(call.Function.Arguments) != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
				input = map[string]any{"_invalidArguments": call.Function.Arguments}
			}
		}
		calls = append(calls, message.ToolCall{ID: call.ID, ToolName: call.Function.Name, Input: input})
	}
	diagnostics := message.Diagnostics{StopReason: choice.FinishReason, BlockTypes: []string{"message"}}
	if len(calls) > 0 {
		contentKind := message.ContentNone
		if kind == message.ContentProgress {
			contentKind = message.ContentProgress
		}
		return message.ToolCallsStep(calls, content, contentKind, diagnostics), nil
	}
	return message.AssistantStep(content, kind, diagnostics), nil
}

type openAIResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

func toOpenAIPayload(runtime config.Runtime, registry *tools.Registry, messages []message.Message) map[string]any {
	apiMessages := []map[string]any{}
	for _, msg := range messages {
		switch msg.Role {
		case message.RoleSystem:
			apiMessages = append(apiMessages, map[string]any{"role": "system", "content": msg.Content})
		case message.RoleUser:
			apiMessages = append(apiMessages, map[string]any{"role": "user", "content": msg.Content})
		case message.RoleAssistant, message.RoleAssistantProgress:
			text := msg.Content
			if msg.Role == message.RoleAssistantProgress {
				text = "<progress>\n" + msg.Content + "\n</progress>"
			}
			apiMessages = append(apiMessages, map[string]any{"role": "assistant", "content": text})
		case message.RoleAssistantToolCall:
			apiMessages = append(apiMessages, map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]any{{
					"id":   msg.ToolUseID,
					"type": "function",
					"function": map[string]any{
						"name":      msg.ToolName,
						"arguments": mustJSONString(msg.Input),
					},
				}},
			})
		case message.RoleToolResult:
			apiMessages = append(apiMessages, map[string]any{"role": "tool", "tool_call_id": msg.ToolUseID, "content": msg.Content})
		}
	}
	toolSchemas := []map[string]any{}
	for _, definition := range registry.List() {
		toolSchemas = append(toolSchemas, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        definition.Name,
				"description": definition.Description,
				"parameters":  definition.InputSchema,
			},
		})
	}
	payload := map[string]any{
		"model":    runtime.Model,
		"messages": apiMessages,
	}
	if len(toolSchemas) > 0 {
		payload["tools"] = toolSchemas
	}
	if runtime.MaxOutputTokens > 0 {
		payload["max_tokens"] = runtime.MaxOutputTokens
	}
	return payload
}

func mustJSONString(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}
