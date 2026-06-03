package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAndLoadSkill(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	root := filepath.Join(cwd, ".mini-code", "skills", "demo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Demo\n\nUse this demo skill."), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(cwd, home)
	discovered, err := store.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 1 || discovered[0].Name != "demo" || discovered[0].Description != "Use this demo skill." {
		t.Fatalf("unexpected skills: %#v", discovered)
	}

	loaded, err := store.Load(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "demo" || loaded.Content == "" {
		t.Fatalf("unexpected loaded skill: %#v", loaded)
	}
}
