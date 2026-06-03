package commands

import "testing"

func TestParseShortcut(t *testing.T) {
	call, ok := ParseShortcut("/edit a.txt::old::new")
	if !ok {
		t.Fatal("expected shortcut")
	}
	if call.ToolName != "edit_file" {
		t.Fatalf("unexpected call: %#v", call)
	}
}

func TestFindMatchingSlashCommands(t *testing.T) {
	matches := FindMatching("/mo")
	if len(matches) != 3 {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}
