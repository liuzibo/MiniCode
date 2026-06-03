package manage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssbsunshengbo/minicode-go/internal/config"
	"github.com/ssbsunshengbo/minicode-go/internal/session"
)

func TestHelpCommand(t *testing.T) {
	out, handled, err := Handle(context.Background(), t.TempDir(), []string{"help"})
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "minicode management commands") {
		t.Fatalf("unexpected result: %q handled=%v", out, handled)
	}
}

func TestSkillsList(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(cwd, ".mini-code", "skills", "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Demo\n\nDemo skill."), 0o644); err != nil {
		t.Fatal(err)
	}

	out, handled, err := Handle(context.Background(), cwd, []string{"skills", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "demo: Demo skill.") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestMCPAddParsesProtocolAndEnv(t *testing.T) {
	cwd := t.TempDir()
	_, handled, err := Handle(context.Background(), cwd, []string{
		"mcp", "add", "fs", "--project", "--protocol", "newline-json", "--env", "A=B", "--", "npx", "server",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected command handled")
	}

	servers, err := config.ReadMCPConfig(filepath.Join(cwd, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := servers["fs"]
	if server.Protocol != "newline-json" || server.Env["A"] != "B" {
		t.Fatalf("unexpected server: %#v", server)
	}
}

func TestInstallLocalCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	out, handled, err := Handle(context.Background(), cwd, []string{
		"install-local",
		"--skip-build",
		"--provider", "openai",
		"--model", "gpt-test",
		"--base-url", "https://openai.example.test",
		"--api-key", "key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected command handled")
	}
	for _, want := range []string{"Installed MiniCode Go", filepath.Join(home, ".local", "bin", "minicode"), "PATH"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "minicode")); err != nil {
		t.Fatal(err)
	}
	runtime, err := config.LoadRuntime(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Provider != "openai" || runtime.Model != "gpt-test" || runtime.BaseURL != "https://openai.example.test" || runtime.APIKey != "key" {
		t.Fatalf("settings not written from install-local: %#v", runtime)
	}
}

func TestInstallLocalInteractivePromptsForSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	input := strings.NewReader("\nclaude-interactive\nhttps://anthropic.interactive.test\nsecret-token\n")
	var prompts bytes.Buffer

	out, handled, err := handleInstallLocalWithIO(context.Background(), cwd, []string{"--skip-build"}, input, &prompts, true)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected command handled")
	}
	if !strings.Contains(prompts.String(), "mini-code installer") || !strings.Contains(out, "settings: "+config.SettingsPath()) {
		t.Fatalf("expected installer prompts and settings output, prompts=%q out=%q", prompts.String(), out)
	}
	runtime, err := config.LoadRuntime(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Provider != "anthropic" || runtime.Model != "claude-interactive" || runtime.BaseURL != "https://anthropic.interactive.test" || runtime.AuthToken != "secret-token" {
		t.Fatalf("interactive settings not written: %#v", runtime)
	}
}

func TestInstallLocalFlagOrderDoesNotChangeProviderEnvNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	_, handled, err := Handle(context.Background(), cwd, []string{
		"install-local",
		"--skip-build",
		"--base-url", "https://openai.order.test",
		"--api-key", "key",
		"--provider", "openai",
		"--model", "gpt-order",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected command handled")
	}
	runtime, err := config.LoadRuntime(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Provider != "openai" || runtime.BaseURL != "https://openai.order.test" || runtime.APIKey != "key" {
		t.Fatalf("provider env names depended on flag order: %#v", runtime)
	}
}

func TestSessionsListCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := session.Store{Dir: config.SessionsDir()}
	if err := store.Save(session.Record{ID: "session-1", CWD: "/tmp/project"}); err != nil {
		t.Fatal(err)
	}

	out, handled, err := Handle(context.Background(), t.TempDir(), []string{"sessions", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !handled || !strings.Contains(out, "session-1") || !strings.Contains(out, "/tmp/project") {
		t.Fatalf("unexpected sessions list: %q handled=%v", out, handled)
	}
}
