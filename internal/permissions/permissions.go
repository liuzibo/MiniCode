package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ssbsunshengbo/minicode-go/internal/workspace"
)

type Kind string

const (
	KindPath    Kind = "path"
	KindCommand Kind = "command"
	KindEdit    Kind = "edit"
)

type Decision string

const (
	DecisionAllowOnce        Decision = "allow_once"
	DecisionAllowAlways      Decision = "allow_always"
	DecisionAllowTurn        Decision = "allow_turn"
	DecisionAllowAllTurn     Decision = "allow_all_turn"
	DecisionDenyOnce         Decision = "deny_once"
	DecisionDenyAlways       Decision = "deny_always"
	DecisionDenyWithFeedback Decision = "deny_with_feedback"
)

type Choice struct {
	Key      string
	Label    string
	Decision Decision
}

type Request struct {
	Kind    Kind
	Summary string
	Details []string
	Scope   string
	Choices []Choice
}

type PromptResult struct {
	Decision Decision
	Feedback string
}

type Prompt func(context.Context, Request) (PromptResult, error)

type Store struct {
	AllowedDirectoryPrefixes []string `json:"allowedDirectoryPrefixes,omitempty"`
	DeniedDirectoryPrefixes  []string `json:"deniedDirectoryPrefixes,omitempty"`
	AllowedCommandPatterns   []string `json:"allowedCommandPatterns,omitempty"`
	DeniedCommandPatterns    []string `json:"deniedCommandPatterns,omitempty"`
	AllowedEditPatterns      []string `json:"allowedEditPatterns,omitempty"`
	DeniedEditPatterns       []string `json:"deniedEditPatterns,omitempty"`
}

type Manager struct {
	workspaceRoot          string
	storePath              string
	prompt                 Prompt
	allowedDirectoryPrefix map[string]bool
	deniedDirectoryPrefix  map[string]bool
	sessionAllowedPaths    map[string]bool
	sessionDeniedPaths     map[string]bool
	allowedCommands        map[string]bool
	deniedCommands         map[string]bool
	sessionAllowedCommands map[string]bool
	sessionDeniedCommands  map[string]bool
	allowedEdits           map[string]bool
	deniedEdits            map[string]bool
	sessionAllowedEdits    map[string]bool
	sessionDeniedEdits     map[string]bool
	turnAllowedEdits       map[string]bool
	turnAllowAllEdits      bool
}

func New(workspaceRoot, storePath string, prompt Prompt) (*Manager, error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, err
	}
	manager := &Manager{
		workspaceRoot:          root,
		storePath:              storePath,
		prompt:                 prompt,
		allowedDirectoryPrefix: map[string]bool{},
		deniedDirectoryPrefix:  map[string]bool{},
		sessionAllowedPaths:    map[string]bool{},
		sessionDeniedPaths:     map[string]bool{},
		allowedCommands:        map[string]bool{},
		deniedCommands:         map[string]bool{},
		sessionAllowedCommands: map[string]bool{},
		sessionDeniedCommands:  map[string]bool{},
		allowedEdits:           map[string]bool{},
		deniedEdits:            map[string]bool{},
		sessionAllowedEdits:    map[string]bool{},
		sessionDeniedEdits:     map[string]bool{},
		turnAllowedEdits:       map[string]bool{},
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) SetPrompt(prompt Prompt) {
	m.prompt = prompt
}

func (m *Manager) BeginTurn() {
	m.turnAllowedEdits = map[string]bool{}
	m.turnAllowAllEdits = false
}

func (m *Manager) EndTurn() {
	m.BeginTurn()
}

func (m *Manager) Summary() []string {
	summary := []string{"cwd: " + m.workspaceRoot}
	if len(m.allowedDirectoryPrefix) > 0 {
		summary = append(summary, "extra allowed dirs: "+strings.Join(firstKeys(m.allowedDirectoryPrefix, 4), ", "))
	} else {
		summary = append(summary, "extra allowed dirs: none")
	}
	if len(m.allowedCommands) > 0 {
		summary = append(summary, "dangerous allowlist: "+strings.Join(firstKeys(m.allowedCommands, 4), ", "))
	} else {
		summary = append(summary, "dangerous allowlist: none")
	}
	if len(m.allowedEdits) > 0 {
		summary = append(summary, "trusted edit targets: "+strings.Join(firstKeys(m.allowedEdits, 2), ", "))
	}
	return summary
}

func (m *Manager) EnsurePathAccess(ctx context.Context, targetPath, intent string) error {
	target, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	if workspace.Within(m.workspaceRoot, target) {
		return nil
	}
	if m.sessionDeniedPaths[target] || matchesPrefix(target, m.deniedDirectoryPrefix) {
		return fmt.Errorf("Access denied for path outside cwd: %s", target)
	}
	if m.sessionAllowedPaths[target] || matchesPrefix(target, m.allowedDirectoryPrefix) {
		return nil
	}
	if m.prompt == nil {
		return fmt.Errorf("Path %s is outside cwd %s. Start minicode in TTY mode to approve it.", target, m.workspaceRoot)
	}
	scope := filepath.Dir(target)
	if intent == "list" || intent == "command_cwd" {
		scope = target
	}
	result, err := m.prompt(ctx, Request{
		Kind:    KindPath,
		Summary: "mini-code wants " + strings.ReplaceAll(intent, "_", " ") + " access outside the current cwd",
		Details: []string{"cwd: " + m.workspaceRoot, "target: " + target, "scope directory: " + scope},
		Scope:   scope,
		Choices: []Choice{
			{Key: "y", Label: "allow once", Decision: DecisionAllowOnce},
			{Key: "a", Label: "allow this directory", Decision: DecisionAllowAlways},
			{Key: "n", Label: "deny once", Decision: DecisionDenyOnce},
			{Key: "d", Label: "deny this directory", Decision: DecisionDenyAlways},
		},
	})
	if err != nil {
		return err
	}
	switch result.Decision {
	case DecisionAllowOnce:
		m.sessionAllowedPaths[target] = true
		return nil
	case DecisionAllowAlways:
		m.allowedDirectoryPrefix[scope] = true
		return m.persist()
	case DecisionDenyAlways:
		m.deniedDirectoryPrefix[scope] = true
		_ = m.persist()
	}
	m.sessionDeniedPaths[target] = true
	return fmt.Errorf("Access denied for path outside cwd: %s", target)
}

func (m *Manager) EnsureCommand(ctx context.Context, command string, args []string, cwd string) error {
	if err := m.EnsurePathAccess(ctx, cwd, "command_cwd"); err != nil {
		return err
	}
	reason := ClassifyDangerousCommand(command, args)
	if reason == "" {
		return nil
	}
	signature := formatCommand(command, args)
	if m.sessionDeniedCommands[signature] || m.deniedCommands[signature] {
		return fmt.Errorf("Command denied: %s", signature)
	}
	if m.sessionAllowedCommands[signature] || m.allowedCommands[signature] {
		return nil
	}
	if m.prompt == nil {
		return fmt.Errorf("Command requires approval: %s. Start minicode in TTY mode to approve it.", signature)
	}
	result, err := m.prompt(ctx, Request{
		Kind:    KindCommand,
		Summary: "mini-code wants to run a dangerous command",
		Details: []string{"cwd: " + cwd, "command: " + signature, "reason: " + reason},
		Scope:   signature,
		Choices: []Choice{
			{Key: "y", Label: "allow once", Decision: DecisionAllowOnce},
			{Key: "a", Label: "always allow this command", Decision: DecisionAllowAlways},
			{Key: "n", Label: "deny once", Decision: DecisionDenyOnce},
			{Key: "d", Label: "always deny this command", Decision: DecisionDenyAlways},
		},
	})
	if err != nil {
		return err
	}
	switch result.Decision {
	case DecisionAllowOnce:
		m.sessionAllowedCommands[signature] = true
		return nil
	case DecisionAllowAlways:
		m.allowedCommands[signature] = true
		return m.persist()
	case DecisionDenyAlways:
		m.deniedCommands[signature] = true
		_ = m.persist()
	}
	m.sessionDeniedCommands[signature] = true
	return fmt.Errorf("Command denied: %s", signature)
}

func (m *Manager) EnsureEdit(ctx context.Context, targetPath, diffPreview string) error {
	target, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	if m.sessionDeniedEdits[target] || m.deniedEdits[target] {
		return fmt.Errorf("Edit denied: %s", target)
	}
	if m.sessionAllowedEdits[target] || m.turnAllowedEdits[target] || m.turnAllowAllEdits || m.allowedEdits[target] {
		return nil
	}
	if m.prompt == nil {
		return fmt.Errorf("Edit requires approval: %s. Start minicode in TTY mode to review it.", target)
	}
	result, err := m.prompt(ctx, Request{
		Kind:    KindEdit,
		Summary: "mini-code wants to apply a file modification",
		Details: []string{"target: " + target, "", diffPreview},
		Scope:   target,
		Choices: []Choice{
			{Key: "1", Label: "apply once", Decision: DecisionAllowOnce},
			{Key: "2", Label: "allow this file in this turn", Decision: DecisionAllowTurn},
			{Key: "3", Label: "allow all edits in this turn", Decision: DecisionAllowAllTurn},
			{Key: "4", Label: "always allow this file", Decision: DecisionAllowAlways},
			{Key: "5", Label: "reject once", Decision: DecisionDenyOnce},
			{Key: "6", Label: "reject and send guidance to model", Decision: DecisionDenyWithFeedback},
			{Key: "7", Label: "always reject this file", Decision: DecisionDenyAlways},
		},
	})
	if err != nil {
		return err
	}
	switch result.Decision {
	case DecisionAllowOnce:
		m.sessionAllowedEdits[target] = true
		return nil
	case DecisionAllowTurn:
		m.turnAllowedEdits[target] = true
		return nil
	case DecisionAllowAllTurn:
		m.turnAllowAllEdits = true
		return nil
	case DecisionAllowAlways:
		m.allowedEdits[target] = true
		return m.persist()
	case DecisionDenyWithFeedback:
		if strings.TrimSpace(result.Feedback) != "" {
			return fmt.Errorf("Edit denied: %s\nUser guidance: %s", target, strings.TrimSpace(result.Feedback))
		}
	case DecisionDenyAlways:
		m.deniedEdits[target] = true
		_ = m.persist()
	}
	m.sessionDeniedEdits[target] = true
	return fmt.Errorf("Edit denied: %s", target)
}

func ClassifyDangerousCommand(command string, args []string) string {
	signature := formatCommand(command, args)
	if command == "git" {
		if contains(args, "reset") && contains(args, "--hard") {
			return "git reset --hard can discard local changes (" + signature + ")"
		}
		if contains(args, "clean") {
			return "git clean can delete untracked files (" + signature + ")"
		}
		if contains(args, "checkout") && contains(args, "--") {
			return "git checkout -- can overwrite working tree files (" + signature + ")"
		}
		if contains(args, "restore") && containsPrefix(args, "--source") {
			return "git restore --source can overwrite local files (" + signature + ")"
		}
		if contains(args, "push") && (contains(args, "--force") || contains(args, "-f")) {
			return "git push --force rewrites remote history (" + signature + ")"
		}
	}
	if command == "npm" && contains(args, "publish") {
		return "npm publish affects a registry outside this machine (" + signature + ")"
	}
	for _, shell := range []string{"node", "python3", "bun", "bash", "sh"} {
		if command == shell {
			return command + " can execute arbitrary local code (" + signature + ")"
		}
	}
	return ""
}

func (m *Manager) load() error {
	var store Store
	bytes, err := os.ReadFile(m.storePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(bytes, &store); err != nil {
		return err
	}
	addAll(m.allowedDirectoryPrefix, store.AllowedDirectoryPrefixes)
	addAll(m.deniedDirectoryPrefix, store.DeniedDirectoryPrefixes)
	addAll(m.allowedCommands, store.AllowedCommandPatterns)
	addAll(m.deniedCommands, store.DeniedCommandPatterns)
	addAll(m.allowedEdits, store.AllowedEditPatterns)
	addAll(m.deniedEdits, store.DeniedEditPatterns)
	return nil
}

func (m *Manager) persist() error {
	store := Store{
		AllowedDirectoryPrefixes: keys(m.allowedDirectoryPrefix),
		DeniedDirectoryPrefixes:  keys(m.deniedDirectoryPrefix),
		AllowedCommandPatterns:   keys(m.allowedCommands),
		DeniedCommandPatterns:    keys(m.deniedCommands),
		AllowedEditPatterns:      keys(m.allowedEdits),
		DeniedEditPatterns:       keys(m.deniedEdits),
	}
	if err := os.MkdirAll(filepath.Dir(m.storePath), 0o755); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.storePath, append(bytes, '\n'), 0o644)
}

func matchesPrefix(target string, prefixes map[string]bool) bool {
	for prefix := range prefixes {
		if workspace.Within(prefix, target) {
			return true
		}
	}
	return false
}

func formatCommand(command string, args []string) string {
	return strings.TrimSpace(strings.Join(append([]string{command}, args...), " "))
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func addAll(target map[string]bool, values []string) {
	for _, value := range values {
		target[value] = true
	}
}

func keys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func firstKeys(values map[string]bool, limit int) []string {
	out := keys(values)
	if len(out) > limit {
		return out[:limit]
	}
	return out
}
