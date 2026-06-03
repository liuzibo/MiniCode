package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryExecute(t *testing.T) {
	reg := NewRegistry([]Definition{
		{
			Name:        "echo",
			Description: "echo input",
			InputSchema: map[string]any{
				"type": "object",
			},
			Run: func(_ context.Context, input json.RawMessage, _ Context) Result {
				var parsed struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(input, &parsed); err != nil {
					return Error(err.Error())
				}
				return Success(parsed.Text)
			},
		},
	}, Metadata{})

	got := reg.Execute(context.Background(), "echo", map[string]any{"text": "hello"}, Context{})
	if !got.OK || got.Output != "hello" {
		t.Fatalf("unexpected result: %#v", got)
	}

	missing := reg.Execute(context.Background(), "missing", nil, Context{})
	if missing.OK || missing.Output != "Unknown tool: missing" {
		t.Fatalf("unexpected missing result: %#v", missing)
	}
}

func TestRegistryExecuteValidatesRequiredSchemaFields(t *testing.T) {
	called := false
	reg := NewRegistry([]Definition{
		{
			Name:        "write",
			Description: "write input",
			InputSchema: objectSchema(map[string]any{"path": map[string]any{"type": "string"}}, []string{"path"}),
			Run: func(_ context.Context, _ json.RawMessage, _ Context) Result {
				called = true
				return Success("ok")
			},
		},
	}, Metadata{})

	got := reg.Execute(context.Background(), "write", map[string]any{}, Context{})
	if got.OK || !strings.Contains(got.Output, "missing required field: path") {
		t.Fatalf("unexpected validation result: %#v", got)
	}
	if called {
		t.Fatal("tool runner should not be called for invalid input")
	}
}

func TestRegistryExecuteValidatesSchemaTypesAndArrayItems(t *testing.T) {
	reg := NewRegistry([]Definition{
		{
			Name:        "cmd",
			Description: "command input",
			InputSchema: objectSchema(map[string]any{
				"command": map[string]any{"type": "string"},
				"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, []string{"command"}),
			Run: func(_ context.Context, _ json.RawMessage, _ Context) Result {
				return Success("ok")
			},
		},
	}, Metadata{})

	wrongCommand := reg.Execute(context.Background(), "cmd", map[string]any{"command": 3}, Context{})
	if wrongCommand.OK || !strings.Contains(wrongCommand.Output, "command must be string") {
		t.Fatalf("unexpected command validation result: %#v", wrongCommand)
	}

	wrongArg := reg.Execute(context.Background(), "cmd", map[string]any{"command": "go", "args": []any{"test", 7}}, Context{})
	if wrongArg.OK || !strings.Contains(wrongArg.Output, "args[1] must be string") {
		t.Fatalf("unexpected args validation result: %#v", wrongArg)
	}

	valid := reg.Execute(context.Background(), "cmd", map[string]any{"command": "go", "args": []any{"test", "./..."}}, Context{})
	if !valid.OK {
		t.Fatalf("expected valid input to run, got %#v", valid)
	}
}

func TestRegistryExecuteTurnsToolPanicIntoError(t *testing.T) {
	reg := NewRegistry([]Definition{
		{
			Name:        "boom",
			Description: "panic tool",
			InputSchema: map[string]any{"type": "object"},
			Run: func(_ context.Context, _ json.RawMessage, _ Context) Result {
				panic("kaboom")
			},
		},
	}, Metadata{})

	got := reg.Execute(context.Background(), "boom", map[string]any{}, Context{})
	if got.OK || !strings.Contains(got.Output, "Tool boom panicked: kaboom") {
		t.Fatalf("unexpected panic result: %#v", got)
	}
}

func TestRegistryExecuteValidatesStringMinLength(t *testing.T) {
	reg := NewRegistry([]Definition{
		{
			Name:        "named",
			Description: "named input",
			InputSchema: objectSchema(map[string]any{"name": map[string]any{"type": "string", "minLength": 1}}, []string{"name"}),
			Run: func(_ context.Context, _ json.RawMessage, _ Context) Result {
				return Success("ok")
			},
		},
	}, Metadata{})

	got := reg.Execute(context.Background(), "named", map[string]any{"name": ""}, Context{})
	if got.OK || !strings.Contains(got.Output, "name must have length at least 1") {
		t.Fatalf("unexpected minLength result: %#v", got)
	}
}

func TestRegistryExecuteValidatesCommonJSONSchemaConstraints(t *testing.T) {
	schema := objectSchema(map[string]any{
		"name":  map[string]any{"type": "string", "minLength": 2, "maxLength": 4, "pattern": "^[a-z]+$"},
		"count": map[string]any{"type": "integer", "minimum": 1, "maximum": 3},
		"items": map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": map[string]any{"type": "string"}},
	}, []string{"name", "count", "items"})
	schema["additionalProperties"] = false
	reg := NewRegistry([]Definition{
		{
			Name:        "schema",
			Description: "schema input",
			InputSchema: schema,
			Run: func(_ context.Context, _ json.RawMessage, _ Context) Result {
				return Success("ok")
			},
		},
	}, Metadata{})

	cases := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"extra", map[string]any{"name": "ab", "count": 1, "items": []any{"x"}, "extra": true}, "unexpected field: extra"},
		{"maxLength", map[string]any{"name": "abcde", "count": 1, "items": []any{"x"}}, "name must have length at most 4"},
		{"pattern", map[string]any{"name": "A1", "count": 1, "items": []any{"x"}}, "name must match pattern"},
		{"minimum", map[string]any{"name": "ab", "count": 0, "items": []any{"x"}}, "count must be at least 1"},
		{"maximum", map[string]any{"name": "ab", "count": 4, "items": []any{"x"}}, "count must be at most 3"},
		{"minItems", map[string]any{"name": "ab", "count": 1, "items": []any{}}, "items must contain at least 1 item(s)"},
		{"maxItems", map[string]any{"name": "ab", "count": 1, "items": []any{"x", "y", "z"}}, "items must contain at most 2 item(s)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reg.Execute(context.Background(), "schema", tc.input, Context{})
			if got.OK || !strings.Contains(got.Output, tc.want) {
				t.Fatalf("unexpected validation result: %#v, want %q", got, tc.want)
			}
		})
	}

	valid := reg.Execute(context.Background(), "schema", map[string]any{"name": "abc", "count": 2, "items": []any{"x", "y"}}, Context{})
	if !valid.OK {
		t.Fatalf("expected valid schema input, got %#v", valid)
	}
}
