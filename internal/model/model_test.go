package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ssbsunshengbo/minicode-go/internal/config"
	"github.com/ssbsunshengbo/minicode-go/internal/message"
	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

func TestParseAssistantMarkers(t *testing.T) {
	content, kind := ParseAssistantText("<final>\ndone\n</final>")
	if content != "done" || kind != message.ContentFinal {
		t.Fatalf("unexpected parse: %q %q", content, kind)
	}
}

func TestNewFromRuntimeSelectsProvider(t *testing.T) {
	registry := tools.NewRegistry(nil, tools.Metadata{})
	anthropic, err := NewFromRuntime(config.Runtime{Provider: "anthropic", Model: "claude", BaseURL: "https://example.test", APIKey: "key"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := anthropic.(*Anthropic); !ok {
		t.Fatalf("expected Anthropic adapter, got %T", anthropic)
	}

	openai, err := NewFromRuntime(config.Runtime{Provider: "openai", Model: "gpt", BaseURL: "https://example.test", APIKey: "key"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := openai.(*OpenAI); !ok {
		t.Fatalf("expected OpenAI adapter, got %T", openai)
	}

	if _, err := NewFromRuntime(config.Runtime{Provider: "unknown", Model: "x"}, registry); err == nil || !strings.Contains(err.Error(), "unsupported model provider") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestErrorModelReturnsConfigurationError(t *testing.T) {
	step, err := (ErrorModel{Err: errors.New("missing config")}).Next(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "missing config") {
		t.Fatalf("expected configuration error, got step=%#v err=%v", step, err)
	}
}

func TestOpenAIAdapterParsesToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer openai-key" {
			t.Fatalf("missing auth header: %s", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-test" {
			t.Fatalf("unexpected request body: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role":    "assistant",
						"content": "<progress>checking",
						"tool_calls": []map[string]any{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "read_file",
									"arguments": `{"path":"README.md"}`,
								},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	adapter := NewOpenAI(func(context.Context) (config.Runtime, error) {
		return config.Runtime{Provider: "openai", Model: "gpt-test", BaseURL: server.URL, APIKey: "openai-key"}, nil
	}, tools.NewRegistry([]tools.Definition{{Name: "read_file", Description: "read", InputSchema: map[string]any{"type": "object"}}}, tools.Metadata{}))

	step, err := adapter.Next(context.Background(), []message.Message{message.SystemMessage("sys"), message.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if step.Type != message.StepToolCalls || len(step.Calls) != 1 || step.Calls[0].ToolName != "read_file" || step.ContentKind != message.ContentProgress {
		t.Fatalf("unexpected step: %#v", step)
	}
	input, ok := step.Calls[0].Input.(map[string]any)
	if !ok || input["path"] != "README.md" {
		t.Fatalf("unexpected tool input: %#v", step.Calls[0].Input)
	}
	if step.Diagnostics.StopReason != "tool_calls" {
		t.Fatalf("unexpected diagnostics: %#v", step.Diagnostics)
	}
}

func TestAnthropicAdapterParsesToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("missing auth header: %s", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "claude-test" {
			t.Fatalf("unexpected body: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "text", "text": "<progress>checking"},
				{"type": "tool_use", "id": "toolu_1", "name": "read_file", "input": map[string]any{"path": "README.md"}},
			},
		})
	}))
	defer server.Close()

	adapter := NewAnthropic(func(context.Context) (config.Runtime, error) {
		return config.Runtime{Model: "claude-test", BaseURL: server.URL, AuthToken: "token"}, nil
	}, tools.NewRegistry([]tools.Definition{{Name: "read_file", Description: "read", InputSchema: map[string]any{"type": "object"}}}, tools.Metadata{}))

	step, err := adapter.Next(context.Background(), []message.Message{message.SystemMessage("sys"), message.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if step.Type != message.StepToolCalls || len(step.Calls) != 1 || step.ContentKind != message.ContentProgress {
		t.Fatalf("unexpected step: %#v", step)
	}
}

func TestAnthropicAdapterDeduplicatesIgnoredBlockDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "pause_turn",
			"content": []map[string]any{
				{"type": "thinking", "thinking": "hidden"},
				{"type": "thinking", "thinking": "hidden again"},
			},
		})
	}))
	defer server.Close()

	adapter := NewAnthropic(func(context.Context) (config.Runtime, error) {
		return config.Runtime{Model: "claude-test", BaseURL: server.URL, AuthToken: "token"}, nil
	}, tools.NewRegistry(nil, tools.Metadata{}))

	step, err := adapter.Next(context.Background(), []message.Message{message.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if step.Diagnostics.StopReason != "pause_turn" {
		t.Fatalf("unexpected diagnostics: %#v", step.Diagnostics)
	}
	if got := step.Diagnostics.IgnoredBlockTypes; len(got) != 1 || got[0] != "thinking" {
		t.Fatalf("expected deduped ignored block diagnostics, got %#v", got)
	}
}
