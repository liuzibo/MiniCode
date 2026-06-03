# Go MiniCode Core Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go implementation of MiniCode that aligns with the current TypeScript product surface: agent loop, Anthropic-compatible model calls, built-in tools, permissions, skills, MCP, management commands, and a usable terminal session.

**Architecture:** The Go version lives beside the existing TypeScript implementation and keeps compatible behavior while using Go-native package boundaries. The first vertical slice produces a working CLI and mock/offline loop; later tasks add model, tools, permissions, skills, MCP, and richer terminal interaction.

**Tech Stack:** Go 1.22, standard library first, `go test ./...` for verification, optional small dependencies only when a later TUI task clearly needs them.

---

## File Structure

- Create `go.mod`: declares module `github.com/ssbsunshengbo/minicode-go`.
- Create `cmd/minicode/main.go`: command entrypoint, management command routing, interactive session bootstrap.
- Create `internal/message`: shared chat message, tool call, model step types.
- Create `internal/agent`: model-tool-model loop with progress/final handling and retry behavior.
- Create `internal/model`: model adapter interface, mock adapter, Anthropic-compatible adapter.
- Create `internal/config`: settings, MCP config, config path resolution, config merge behavior.
- Create `internal/tools`: tool interface, registry, built-in file/search/command/skill/MCP helper tools.
- Create `internal/workspace`: path resolution and workspace boundary checks.
- Create `internal/permissions`: path, command, and edit approval policy plus persistence.
- Create `internal/filereview`: unified diff preview and reviewed file write helper.
- Create `internal/skills`: discovery, loading, install/remove helpers.
- Create `internal/mcp`: stdio JSON-RPC MCP client with content-length and newline-json framing.
- Create `internal/commands`: slash commands and direct local tool shortcuts.
- Create `internal/session`: line-oriented terminal session, history, prompt refresh, tool transcript.

## Delivery Tasks

### Task 1: Go Module and Message Contracts

**Files:**
- Create: `go.mod`
- Create: `internal/message/message.go`
- Test: `internal/message/message_test.go`

- [ ] **Step 1: Write tests for message contracts**

```go
package message

import "testing"

func TestAssistantKinds(t *testing.T) {
	msg := AssistantMessage("hello")
	if msg.Role != RoleAssistant || msg.Content != "hello" {
		t.Fatalf("unexpected assistant message: %#v", msg)
	}
	progress := ProgressMessage("working")
	if progress.Role != RoleAssistantProgress || progress.Content != "working" {
		t.Fatalf("unexpected progress message: %#v", progress)
	}
}
```

- [ ] **Step 2: Run red test**

Run: `go test ./internal/message`

Expected: fail because package files do not exist yet.

- [ ] **Step 3: Implement message types**

Create roles for `system`, `user`, `assistant`, `assistant_progress`, `assistant_tool_call`, and `tool_result`. Add `ToolCall`, `Diagnostics`, `Step`, and `Model` interface contracts.

- [ ] **Step 4: Run green test**

Run: `go test ./internal/message`

Expected: pass.

### Task 2: Agent Loop

**Files:**
- Create: `internal/agent/loop.go`
- Test: `internal/agent/loop_test.go`

- [ ] **Step 1: Write tests for tool execution and final answer**

Test that a model returning one tool call followed by an assistant final response produces `assistant_tool_call`, `tool_result`, and final `assistant` messages in order.

- [ ] **Step 2: Run red test**

Run: `go test ./internal/agent`

Expected: fail because agent loop is missing.

- [ ] **Step 3: Implement minimal loop**

Implement `RunTurn` with model step iteration, tool execution, `MaxSteps`, callbacks, and message appending.

- [ ] **Step 4: Add progress and empty-response tests**

Test `<progress>` continuation behavior, empty assistant retry, recoverable thinking retry, and max step fallback.

- [ ] **Step 5: Run package tests**

Run: `go test ./internal/agent`

Expected: pass.

### Task 3: Config and Prompt

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/prompt/prompt.go`
- Test: `internal/config/config_test.go`
- Test: `internal/prompt/prompt_test.go`

- [ ] **Step 1: Write config merge tests**

Test MiniCode settings, global MCP, project MCP, Claude fallback, and environment override precedence.

- [ ] **Step 2: Implement config loading**

Implement `~/.mini-code/settings.json`, `~/.mini-code/mcp.json`, project `.mcp.json`, Claude compatible settings, and runtime auth validation.

- [ ] **Step 3: Write prompt tests**

Assert prompt includes cwd, permission summary, skills, MCP server summaries, global `CLAUDE.md`, and project `CLAUDE.md` when present.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config ./internal/prompt`

Expected: pass.

### Task 4: Tool Registry and Built-in Tools

**Files:**
- Create: `internal/tools/registry.go`
- Create: `internal/tools/list_files.go`
- Create: `internal/tools/grep_files.go`
- Create: `internal/tools/read_file.go`
- Create: `internal/tools/write_file.go`
- Create: `internal/tools/edit_file.go`
- Create: `internal/tools/patch_file.go`
- Create: `internal/tools/run_command.go`
- Create: `internal/tools/load_skill.go`
- Create: `internal/workspace/workspace.go`
- Test: `internal/tools/*_test.go`
- Test: `internal/workspace/workspace_test.go`

- [ ] **Step 1: Write registry tests**

Test unknown tool, invalid input JSON, and successful execution.

- [ ] **Step 2: Implement registry**

Use `json.RawMessage` validation per tool and a common `Result{OK, Output}`.

- [ ] **Step 3: Write tests for file tools**

Cover list limit, read offset/limit/truncation header, exact edit, multi replacement patch, and write via review helper.

- [ ] **Step 4: Implement file tools**

Keep TypeScript-compatible names and output style.

- [ ] **Step 5: Write command tests**

Cover allowlisted commands, blocked commands, shell snippet detection, and dangerous command approval hook.

- [ ] **Step 6: Implement `run_command`**

Use `exec.CommandContext`, 1 MiB output cap, allowlist, and permission checks.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/workspace ./internal/tools`

Expected: pass.

### Task 5: Permissions and File Review

**Files:**
- Create: `internal/permissions/permissions.go`
- Create: `internal/filereview/review.go`
- Test: `internal/permissions/permissions_test.go`
- Test: `internal/filereview/review_test.go`

- [ ] **Step 1: Write permission tests**

Test workspace path access, outside path prompt, persistent allow/deny, dangerous command classification, edit allow once, allow turn, allow all turn, reject with feedback.

- [ ] **Step 2: Implement permissions**

Persist to `~/.mini-code/permissions.json`, matching the TypeScript decision vocabulary.

- [ ] **Step 3: Write file review tests**

Test no-op write, new file diff, modified file diff, and approval before write.

- [ ] **Step 4: Implement file review**

Generate unified diff preview and apply approved UTF-8 writes.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/permissions ./internal/filereview ./internal/tools`

Expected: pass.

### Task 6: Skills and Management Commands

**Files:**
- Create: `internal/skills/skills.go`
- Create: `internal/manage/manage.go`
- Test: `internal/skills/skills_test.go`
- Test: `internal/manage/manage_test.go`

- [ ] **Step 1: Write skills tests**

Test project/user/Claude-compatible discovery precedence, description extraction, load, install, and remove.

- [ ] **Step 2: Implement skills**

Match `.mini-code/skills/<name>/SKILL.md` and `.claude/skills/<name>/SKILL.md` behavior.

- [ ] **Step 3: Write management tests**

Test `mcp list/add/remove` and `skills list/add/remove` command parsing with user/project scope.

- [ ] **Step 4: Implement management commands**

Keep command syntax compatible with the TypeScript README.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/skills ./internal/manage`

Expected: pass.

### Task 7: Anthropic Adapter and Mock Mode

**Files:**
- Create: `internal/model/anthropic.go`
- Create: `internal/model/mock.go`
- Test: `internal/model/anthropic_test.go`
- Test: `internal/model/mock_test.go`

- [ ] **Step 1: Write adapter tests**

Use `httptest.Server` to verify request body, auth headers, tool schema emission, text parsing, progress/final markers, tool_use parsing, and diagnostics capture.

- [ ] **Step 2: Implement adapter**

Map internal messages to Anthropic content blocks and parse response content blocks.

- [ ] **Step 3: Write mock tests**

Test deterministic offline responses for tool exploration.

- [ ] **Step 4: Implement mock adapter**

Support `MINI_CODE_MODEL_MODE=mock`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/model`

Expected: pass.

### Task 8: MCP Client and MCP Helper Tools

**Files:**
- Create: `internal/mcp/client.go`
- Create: `internal/mcp/tools.go`
- Test: `internal/mcp/client_test.go`
- Test: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write framing tests**

Test content-length parsing, newline-json parsing, initialize handshake, pending request timeout, and stderr error capture.

- [ ] **Step 2: Implement stdio client**

Use `os/exec`, JSON-RPC IDs, pending request map, timeout handling, and protocol negotiation.

- [ ] **Step 3: Write wrapper tests**

Test MCP tool name sanitization, `tools/list` wrapping, `resources/list/read`, and `prompts/list/get`.

- [ ] **Step 4: Implement wrappers**

Expose remote tools through the local registry and add generic MCP resource/prompt tools.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/mcp ./internal/tools`

Expected: pass.

### Task 9: Slash Commands and Terminal Session

**Files:**
- Create: `internal/commands/slash.go`
- Create: `internal/commands/shortcuts.go`
- Create: `internal/session/history.go`
- Create: `internal/session/session.go`
- Test: `internal/commands/*_test.go`
- Test: `internal/session/*_test.go`

- [ ] **Step 1: Write slash command tests**

Test help text, matching suggestions, `/model`, `/status`, `/config-paths`, `/skills`, `/mcp`, and `/permissions`.

- [ ] **Step 2: Implement slash commands**

Keep output concise and compatible with current TypeScript behavior.

- [ ] **Step 3: Write shortcut tests**

Test `/ls`, `/grep`, `/read`, `/write`, `/modify`, `/edit`, `/patch`, and `/cmd` parsing.

- [ ] **Step 4: Implement shortcuts**

Return local tool calls without invoking the model.

- [ ] **Step 5: Write session tests**

Test history load/save, prompt refresh before model turn, direct shortcut execution, and final assistant display.

- [ ] **Step 6: Implement line-oriented session**

Start with a reliable line session; keep a later raw-screen TUI upgrade isolated.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/commands ./internal/session`

Expected: pass.

### Task 10: CLI Entrypoint and End-to-End Verification

**Files:**
- Create: `cmd/minicode/main.go`
- Create: `README_GO.md`
- Test: `cmd/minicode/main_test.go`

- [ ] **Step 1: Write CLI tests**

Test management command routing, mock mode bootstrap, and non-interactive stdin turn.

- [ ] **Step 2: Implement CLI**

Wire config, tools, permissions, model adapter, prompt, management commands, and session.

- [ ] **Step 3: Add Go README**

Document install, mock run, config, MCP, skills, slash commands, and parity status.

- [ ] **Step 4: Run full verification**

Run: `go test ./...`

Expected: pass.

Run: `go run ./cmd/minicode --help`

Expected: management usage prints successfully.

Run: `MINI_CODE_MODEL_MODE=mock go run ./cmd/minicode`

Expected: interactive session starts without API credentials.

## Self-Review

- Scope covers core TypeScript parity: loop, model, built-in tools, permissions, skills, MCP, management commands, and terminal session.
- First Go TUI target is line-oriented for reliability. The raw full-screen UI can be upgraded after the parity core is stable.
- No task depends on hidden code from another task without naming the file and package that provides it.
- Verification commands are explicit and package-scoped, ending with `go test ./...`.
