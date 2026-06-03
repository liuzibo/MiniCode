package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeConfigMergesSettingsAndEnv(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")

	miniDir := filepath.Join(home, ".mini-code")
	if err := os.MkdirAll(miniDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(miniDir, "settings.json"), []byte(`{
		"model": "claude-test",
		"env": {"ANTHROPIC_BASE_URL": "https://example.test"}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".mcp.json"), []byte(`{
		"mcpServers": {"fs": {"command": "npx", "args": ["server"]}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime, err := LoadRuntime(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Model != "claude-test" || runtime.BaseURL != "https://example.test" {
		t.Fatalf("unexpected runtime: %#v", runtime)
	}
	if runtime.AuthToken != "env-token" {
		t.Fatalf("expected env auth token, got %q", runtime.AuthToken)
	}
	if runtime.MCPServers["fs"].Command != "npx" {
		t.Fatalf("project MCP config not loaded: %#v", runtime.MCPServers)
	}
}

func TestLoadRuntimeSupportsOpenAIProvider(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "openai-key")

	miniDir := filepath.Join(home, ".mini-code")
	if err := os.MkdirAll(miniDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(miniDir, "settings.json"), []byte(`{
		"provider": "openai",
		"model": "gpt-test",
		"env": {"OPENAI_BASE_URL": "https://openai.example.test"}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime, err := LoadRuntime(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Provider != "openai" || runtime.Model != "gpt-test" || runtime.BaseURL != "https://openai.example.test" || runtime.APIKey != "openai-key" {
		t.Fatalf("unexpected openai runtime: %#v", runtime)
	}
}
