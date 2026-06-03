package prompt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

func TestBuildIncludesSkillsMCPAndClaudeFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("Project rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("Global rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Build(context.Background(), Args{
		CWD:               cwd,
		Home:              home,
		PermissionSummary: []string{"cwd: " + cwd},
		Skills:            []tools.SkillSummary{{Name: "demo", Description: "Demo skill"}},
		MCPServers:        []tools.MCPServerSummary{{Name: "fs", Status: "connected", ToolCount: 2}},
	})

	for _, want := range []string{"You are mini-code", "Demo skill", "fs: connected", "Global rule", "Project rule"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q:\n%s", want, got)
		}
	}
}

func TestBuildIncludesTypeScriptParityBehaviorRules(t *testing.T) {
	out := Build(context.Background(), Args{CWD: t.TempDir()})
	for _, want := range []string{
		"If a missing preference would materially change the result",
		"When using read_file, pay attention to the header fields",
		"If the user names a skill or clearly asks for a workflow",
		"Do not stop after a progress update",
		"After you have used any tool in the current turn",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}
}
