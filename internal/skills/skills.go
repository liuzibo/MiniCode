package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

type Loaded struct {
	tools.SkillSummary
	Content string
}

type Store struct {
	CWD  string
	Home string
}

func NewStore(cwd, home string) Store {
	return Store{CWD: cwd, Home: home}
}

func (s Store) Discover(ctx context.Context) ([]tools.SkillSummary, error) {
	byName := map[string]tools.SkillSummary{}
	for _, root := range s.roots() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(root.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if _, exists := byName[name]; exists {
				continue
			}
			skillPath := filepath.Join(root.path, name, "SKILL.md")
			content, err := os.ReadFile(skillPath)
			if err != nil {
				continue
			}
			byName[name] = tools.SkillSummary{
				Name:        name,
				Description: extractDescription(string(content)),
				Path:        skillPath,
				Source:      root.source,
			}
		}
	}
	out := make([]tools.SkillSummary, 0, len(byName))
	for _, skill := range byName {
		out = append(out, skill)
	}
	return out, nil
}

func (s Store) Load(ctx context.Context, name string) (Loaded, error) {
	for _, root := range s.roots() {
		if err := ctx.Err(); err != nil {
			return Loaded{}, err
		}
		skillPath := filepath.Join(root.path, strings.TrimSpace(name), "SKILL.md")
		content, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		return Loaded{
			SkillSummary: tools.SkillSummary{
				Name:        strings.TrimSpace(name),
				Description: extractDescription(string(content)),
				Path:        skillPath,
				Source:      root.source,
			},
			Content: string(content),
		}, nil
	}
	return Loaded{}, errors.New("Unknown skill: " + name)
}

func (s Store) LoadSkill(ctx context.Context, name string) (string, error) {
	loaded, err := s.Load(ctx, name)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"SKILL: " + loaded.Name,
		"SOURCE: " + loaded.Source,
		"PATH: " + loaded.Path,
		"",
		loaded.Content,
	}, "\n"), nil
}

func (s Store) Install(_ context.Context, sourcePath, name, scope string) (string, error) {
	content, err := os.ReadFile(filepath.Join(sourcePath, "SKILL.md"))
	if err != nil {
		content, err = os.ReadFile(sourcePath)
		if err != nil {
			return "", err
		}
	}
	if name == "" {
		name = filepath.Base(sourcePath)
		if name == "SKILL.md" {
			name = filepath.Base(filepath.Dir(sourcePath))
		}
	}
	targetRoot := filepath.Join(s.Home, ".mini-code", "skills")
	if scope == "project" {
		targetRoot = filepath.Join(s.CWD, ".mini-code", "skills")
	}
	target := filepath.Join(targetRoot, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	return target, os.WriteFile(target, content, 0o644)
}

func (s Store) Remove(_ context.Context, name, scope string) (string, bool, error) {
	targetRoot := filepath.Join(s.Home, ".mini-code", "skills")
	if scope == "project" {
		targetRoot = filepath.Join(s.CWD, ".mini-code", "skills")
	}
	target := filepath.Join(targetRoot, name)
	err := os.RemoveAll(target)
	return target, err == nil, err
}

type root struct {
	path   string
	source string
}

func (s Store) roots() []root {
	return []root{
		{filepath.Join(s.CWD, ".mini-code", "skills"), "project"},
		{filepath.Join(s.Home, ".mini-code", "skills"), "user"},
		{filepath.Join(s.CWD, ".claude", "skills"), "compat_project"},
		{filepath.Join(s.Home, ".claude", "skills"), "compat_user"},
	}
}

func extractDescription(markdown string) string {
	blocks := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n\n")
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" || strings.HasPrefix(block, "#") {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				return strings.ReplaceAll(line, "`", "")
			}
		}
	}
	return "No description provided."
}
