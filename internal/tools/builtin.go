package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ssbsunshengbo/minicode-go/internal/filereview"
	"github.com/ssbsunshengbo/minicode-go/internal/workspace"
)

type SkillLoader interface {
	LoadSkill(ctx context.Context, name string) (string, error)
}

func Builtins(cwd string, permission PermissionManager, skillLoader SkillLoader) *Registry {
	definitions := []Definition{
		listFilesTool(),
		grepFilesTool(),
		readFileTool(),
		writeFileTool(),
		modifyFileTool(),
		editFileTool(),
		patchFileTool(),
		runCommandTool(),
		loadSkillTool(skillLoader),
	}
	return NewRegistry(definitions, Metadata{})
}

func listFilesTool() Definition {
	return Definition{
		Name:        "list_files",
		Description: "List files in a directory relative to the workspace root.",
		InputSchema: objectSchema(map[string]any{"path": map[string]any{"type": "string"}}, nil),
		Run: func(ctx context.Context, raw json.RawMessage, tc Context) Result {
			var input struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(raw, &input)
			target, err := workspace.Resolve(ctx, tc.CWD, defaultString(input.Path, "."), "list", tc.Permission)
			if err != nil {
				return Error(err.Error())
			}
			entries, err := os.ReadDir(target)
			if err != nil {
				return Error(err.Error())
			}
			lines := []string{}
			for i, entry := range entries {
				if i >= 200 {
					break
				}
				prefix := "file"
				if entry.IsDir() {
					prefix = "dir "
				}
				lines = append(lines, prefix+" "+entry.Name())
			}
			if len(lines) == 0 {
				return Success("(empty)")
			}
			return Success(strings.Join(lines, "\n"))
		},
	}
}

func grepFilesTool() Definition {
	return Definition{
		Name:        "grep_files",
		Description: "Search for text in files using ripgrep.",
		InputSchema: objectSchema(map[string]any{"pattern": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, []string{"pattern"}),
		Run: func(ctx context.Context, raw json.RawMessage, tc Context) Result {
			var input struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return Error(err.Error())
			}
			args := []string{"-n", "--no-heading", input.Pattern}
			if input.Path != "" {
				target, err := workspace.Resolve(ctx, tc.CWD, input.Path, "search", tc.Permission)
				if err != nil {
					return Error(err.Error())
				}
				args = append(args, target)
			} else {
				args = append(args, ".")
			}
			cmd := exec.CommandContext(ctx, "rg", args...)
			cmd.Dir = tc.CWD
			output, _ := cmd.CombinedOutput()
			text := strings.TrimSpace(string(output))
			if text == "" {
				text = "(no matches)"
			}
			return Success(text)
		},
	}
}

func readFileTool() Definition {
	return Definition{
		Name:        "read_file",
		Description: "Read a UTF-8 text file relative to the workspace root. Large files can be read in chunks via offset and limit.",
		InputSchema: objectSchema(map[string]any{"path": map[string]any{"type": "string"}, "offset": map[string]any{"type": "number"}, "limit": map[string]any{"type": "number"}}, []string{"path"}),
		Run: func(ctx context.Context, raw json.RawMessage, tc Context) Result {
			var input struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return Error(err.Error())
			}
			target, err := workspace.Resolve(ctx, tc.CWD, input.Path, "read", tc.Permission)
			if err != nil {
				return Error(err.Error())
			}
			bytes, err := os.ReadFile(target)
			if err != nil {
				return Error(err.Error())
			}
			content := string(bytes)
			limit := input.Limit
			if limit <= 0 {
				limit = 8000
			}
			if limit > 20000 {
				limit = 20000
			}
			offset := input.Offset
			if offset < 0 {
				offset = 0
			}
			if offset > len(content) {
				offset = len(content)
			}
			end := offset + limit
			if end > len(content) {
				end = len(content)
			}
			truncated := "no"
			if end < len(content) {
				truncated = "yes - call read_file again with offset " + strconv.Itoa(end)
			}
			header := strings.Join([]string{
				"FILE: " + input.Path,
				"OFFSET: " + strconv.Itoa(offset),
				"END: " + strconv.Itoa(end),
				"TOTAL_CHARS: " + strconv.Itoa(len(content)),
				"TRUNCATED: " + truncated,
				"",
			}, "\n")
			return Success(header + content[offset:end])
		},
	}
}

func writeFileTool() Definition {
	return reviewedWriteTool("write_file", "Write a UTF-8 text file relative to the workspace root.")
}

func modifyFileTool() Definition {
	return reviewedWriteTool("modify_file", "Replace a file with reviewed content so the user can approve the diff first.")
}

func reviewedWriteTool(name, description string) Definition {
	return Definition{
		Name:        name,
		Description: description,
		InputSchema: objectSchema(map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, []string{"path", "content"}),
		Run: func(ctx context.Context, raw json.RawMessage, tc Context) Result {
			var input struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return Error(err.Error())
			}
			target, err := workspace.Resolve(ctx, tc.CWD, input.Path, "write", tc.Permission)
			if err != nil {
				return Error(err.Error())
			}
			return fromReview(filereview.ApplyReviewedChange(ctx, tc.Permission, input.Path, target, input.Content))
		},
	}
}

func editFileTool() Definition {
	return Definition{
		Name:        "edit_file",
		Description: "Edit a text file by replacing exact text.",
		InputSchema: objectSchema(map[string]any{"path": map[string]any{"type": "string"}, "search": map[string]any{"type": "string"}, "replace": map[string]any{"type": "string"}, "replaceAll": map[string]any{"type": "boolean"}}, []string{"path", "search", "replace"}),
		Run: func(ctx context.Context, raw json.RawMessage, tc Context) Result {
			var input struct {
				Path       string `json:"path"`
				Search     string `json:"search"`
				Replace    string `json:"replace"`
				ReplaceAll bool   `json:"replaceAll"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return Error(err.Error())
			}
			target, err := workspace.Resolve(ctx, tc.CWD, input.Path, "write", tc.Permission)
			if err != nil {
				return Error(err.Error())
			}
			bytes, err := os.ReadFile(target)
			if err != nil {
				return Error(err.Error())
			}
			original := string(bytes)
			if !strings.Contains(original, input.Search) {
				return Error("Text not found in " + input.Path)
			}
			next := strings.Replace(original, input.Search, input.Replace, 1)
			if input.ReplaceAll {
				next = strings.ReplaceAll(original, input.Search, input.Replace)
			}
			return fromReview(filereview.ApplyReviewedChange(ctx, tc.Permission, input.Path, target, next))
		},
	}
}

func patchFileTool() Definition {
	return Definition{
		Name:        "patch_file",
		Description: "Apply multiple exact-text replacements to one file in a single operation.",
		InputSchema: objectSchema(map[string]any{
			"path": map[string]any{"type": "string"},
			"replacements": map[string]any{
				"type": "array",
				"items": objectSchema(map[string]any{
					"search":     map[string]any{"type": "string"},
					"replace":    map[string]any{"type": "string"},
					"replaceAll": map[string]any{"type": "boolean"},
				}, []string{"search", "replace"}),
			},
		}, []string{"path", "replacements"}),
		Run: func(ctx context.Context, raw json.RawMessage, tc Context) Result {
			var input struct {
				Path         string `json:"path"`
				Replacements []struct {
					Search     string `json:"search"`
					Replace    string `json:"replace"`
					ReplaceAll bool   `json:"replaceAll"`
				} `json:"replacements"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return Error(err.Error())
			}
			target, err := workspace.Resolve(ctx, tc.CWD, input.Path, "write", tc.Permission)
			if err != nil {
				return Error(err.Error())
			}
			bytes, err := os.ReadFile(target)
			if err != nil {
				return Error(err.Error())
			}
			content := string(bytes)
			for index, replacement := range input.Replacements {
				if !strings.Contains(content, replacement.Search) {
					return Error("Replacement " + strconv.Itoa(index+1) + " not found in " + input.Path)
				}
				if replacement.ReplaceAll {
					content = strings.ReplaceAll(content, replacement.Search, replacement.Replace)
				} else {
					content = strings.Replace(content, replacement.Search, replacement.Replace, 1)
				}
			}
			result := fromReview(filereview.ApplyReviewedChange(ctx, tc.Permission, input.Path, target, content))
			if !result.OK {
				return result
			}
			return Success("Patched " + input.Path + " with " + strconv.Itoa(len(input.Replacements)) + " replacement(s)")
		},
	}
}

func runCommandTool() Definition {
	return Definition{
		Name:        "run_command",
		Description: "Run a common development command from an allowlist.",
		InputSchema: objectSchema(map[string]any{
			"command": map[string]any{"type": "string"},
			"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"cwd":     map[string]any{"type": "string"},
		}, []string{"command"}),
		Run: func(ctx context.Context, raw json.RawMessage, tc Context) Result {
			var input struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
				CWD     string   `json:"cwd"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return Error(err.Error())
			}
			effectiveCWD := tc.CWD
			if input.CWD != "" {
				target, err := workspace.Resolve(ctx, tc.CWD, input.CWD, "list", tc.Permission)
				if err != nil {
					return Error(err.Error())
				}
				effectiveCWD = target
			}
			command := input.Command
			args := input.Args
			if looksLikeShellSnippet(command, args) {
				args = []string{"-lc", command}
				command = "bash"
			} else if !allowedCommand(command) {
				return Error("Command not allowed: " + command)
			}
			if tc.Permission != nil {
				if err := tc.Permission.EnsureCommand(ctx, command, args, effectiveCWD); err != nil {
					return Error(err.Error())
				}
			}
			cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(cmdCtx, command, args...)
			cmd.Dir = effectiveCWD
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			err := cmd.Run()
			text := strings.TrimSpace(output.String())
			if err != nil {
				if text == "" {
					text = err.Error()
				}
				return Error(text)
			}
			return Success(text)
		},
	}
}

func loadSkillTool(loader SkillLoader) Definition {
	return Definition{
		Name:        "load_skill",
		Description: "Load the full contents of a named SKILL.md file so you can follow that workflow accurately.",
		InputSchema: objectSchema(map[string]any{"name": map[string]any{"type": "string"}}, []string{"name"}),
		Run: func(ctx context.Context, raw json.RawMessage, _ Context) Result {
			var input struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &input); err != nil {
				return Error(err.Error())
			}
			if loader == nil {
				return Error("Unknown skill: " + input.Name)
			}
			content, err := loader.LoadSkill(ctx, input.Name)
			if err != nil {
				return Error(err.Error())
			}
			return Success(content)
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if required != nil {
		schema["required"] = required
	}
	return schema
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func allowedCommand(command string) bool {
	allowed := map[string]bool{"pwd": true, "ls": true, "find": true, "rg": true, "cat": true, "echo": true, "env": true, "grep": true, "git": true, "npm": true, "node": true, "python3": true, "pytest": true, "bash": true, "sh": true, "bun": true, "sed": true, "head": true, "tail": true, "wc": true, "go": true}
	return allowed[command]
}

func looksLikeShellSnippet(command string, args []string) bool {
	if len(args) > 0 {
		return false
	}
	return regexp.MustCompile(`[|&;<>()$` + "`" + `]`).MatchString(command)
}

func absPath(path string) string {
	out, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return out
}

func fromReview(result filereview.Result) Result {
	return Result{OK: result.OK, Output: result.Output}
}
