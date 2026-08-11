package session

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ssbsunshengbo/minicode-go/internal/agent"
	"github.com/ssbsunshengbo/minicode-go/internal/commands"
	"github.com/ssbsunshengbo/minicode-go/internal/config"
	"github.com/ssbsunshengbo/minicode-go/internal/message"
	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

type Args struct {
	CWD        string
	Tools      *tools.Registry
	Model      message.Model
	Runtime    *config.Runtime
	Messages   []message.Message
	Permission tools.PermissionManager
	History    History
	Store      Store
	SessionID  string
	In         io.Reader
	Out        io.Writer
}

type Session struct {
	args Args
}

func New(args Args) *Session {
	if args.In == nil {
		args.In = os.Stdin
	}
	if args.Out == nil {
		args.Out = os.Stdout
	}
	return &Session{args: args}
}

func (s *Session) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(s.args.In)
	historyEntries, _ := s.args.History.Load()
	for {
		fmt.Fprint(s.args.Out, "minicode> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "/exit" {
			return nil
		}
		// 保存历史
		if input != "" && (len(historyEntries) == 0 || historyEntries[len(historyEntries)-1] != input) {
			historyEntries = append(historyEntries, input)
			_ = s.args.History.Save(historyEntries)
		}
		if err := s.RunOnce(ctx, input); err != nil {
			fmt.Fprintln(s.args.Out, err)
		}
	}
}

func (s *Session) RunOnce(ctx context.Context, input string) error {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	if input == "/help" || input == "/" {
		fmt.Fprintln(s.args.Out, commands.FormatHelp())
		return nil
	}
	if input == "/tools" {
		for _, tool := range s.args.Tools.List() {
			fmt.Fprintf(s.args.Out, "%s: %s\n", tool.Name, tool.Description)
		}
		return nil
	}
	if input == "/config-paths" {
		fmt.Fprintln(s.args.Out, "mini-code settings: "+config.SettingsPath())
		fmt.Fprintln(s.args.Out, "mini-code permissions: "+config.PermissionsPath())
		fmt.Fprintln(s.args.Out, "mini-code mcp: "+config.MCPPath())
		fmt.Fprintln(s.args.Out, "compat fallback: "+config.ClaudeSettingsPath())
		return nil
	}
	if input == "/permissions" {
		fmt.Fprintln(s.args.Out, "permission store: "+config.PermissionsPath())
		return nil
	}
	if input == "/status" {
		if s.args.Runtime == nil {
			fmt.Fprintln(s.args.Out, "model: not-configured")
			return nil
		}
		fmt.Fprintln(s.args.Out, "provider: "+s.args.Runtime.Provider)
		fmt.Fprintln(s.args.Out, "model: "+s.args.Runtime.Model)
		fmt.Fprintln(s.args.Out, "baseUrl: "+s.args.Runtime.BaseURL)
		auth := "API_KEY"
		if s.args.Runtime.AuthToken != "" {
			auth = "AUTH_TOKEN"
		}
		fmt.Fprintln(s.args.Out, "auth: "+auth)
		fmt.Fprintf(s.args.Out, "mcp servers: %d\n", len(s.args.Runtime.MCPServers))
		fmt.Fprintln(s.args.Out, s.args.Runtime.SourceSummary)
		return nil
	}
	if input == "/model" {
		if s.args.Runtime == nil {
			fmt.Fprintln(s.args.Out, "current model: not-configured")
			return nil
		}
		fmt.Fprintln(s.args.Out, "current model: "+s.args.Runtime.Model)
		return nil
	}
	if strings.HasPrefix(input, "/model ") {
		modelName := strings.TrimSpace(strings.TrimPrefix(input, "/model "))
		if modelName == "" {
			fmt.Fprintln(s.args.Out, "用法: /model <model-name>")
			return nil
		}
		if err := config.SaveSettings(config.Settings{Model: modelName}); err != nil {
			return err
		}
		fmt.Fprintln(s.args.Out, "saved model="+modelName+" to "+config.SettingsPath())
		return nil
	}
	if input == "/skills" {
		skills := s.args.Tools.Skills()
		if len(skills) == 0 {
			fmt.Fprintln(s.args.Out, "No skills discovered. Add skills under ~/.mini-code/skills/<name>/SKILL.md, .mini-code/skills/<name>/SKILL.md, .claude/skills/<name>/SKILL.md, or ~/.claude/skills/<name>/SKILL.md.")
			return nil
		}
		for _, skill := range skills {
			fmt.Fprintf(s.args.Out, "%s  %s  [%s]\n", skill.Name, skill.Description, skill.Source)
		}
		return nil
	}
	if input == "/mcp" {
		servers := s.args.Tools.MCPServers()
		if len(servers) == 0 {
			fmt.Fprintln(s.args.Out, "No MCP servers configured. Add mcpServers to ~/.mini-code/settings.json, ~/.mini-code/mcp.json, or project .mcp.json.")
			return nil
		}
		for _, server := range servers {
			line := fmt.Sprintf("%s  status=%s  tools=%d", server.Name, server.Status, server.ToolCount)
			if server.ResourceCount > 0 {
				line += fmt.Sprintf("  resources=%d", server.ResourceCount)
			}
			if server.PromptCount > 0 {
				line += fmt.Sprintf("  prompts=%d", server.PromptCount)
			}
			if server.Protocol != "" {
				line += "  protocol=" + server.Protocol
			}
			if server.Error != "" {
				line += "  error=" + server.Error
			}
			fmt.Fprintln(s.args.Out, line)
		}
		return nil
	}
	if call, ok := commands.ParseShortcut(input); ok {
		result := s.args.Tools.Execute(ctx, call.ToolName, call.Input, tools.Context{CWD: s.args.CWD, Permission: s.args.Permission})
		if result.OK {
			fmt.Fprintln(s.args.Out, result.Output)
		} else {
			fmt.Fprintln(s.args.Out, "ERROR: "+result.Output)
		}
		return nil
	}
	if strings.HasPrefix(input, "/") {
		matches := commands.FindMatching(input)
		if len(matches) > 0 {
			fmt.Fprintln(s.args.Out, "未识别命令。你是不是想输入：")
			fmt.Fprintln(s.args.Out, strings.Join(matches, "\n"))
		} else {
			fmt.Fprintln(s.args.Out, "未识别命令。输入 /help 查看可用命令。")
		}
		return nil
	}

	//
	messages := append(s.args.Messages, message.UserMessage(input))
	next, err := agent.RunTurn(ctx, agent.Args{
		Model:      s.args.Model,
		Tools:      s.args.Tools,
		Messages:   messages,
		CWD:        s.args.CWD,
		Permission: s.args.Permission,
		OnProgressMessage: func(content string) {
			fmt.Fprintln(s.args.Out, "progress: "+content)
		},
		OnToolStart: func(name string, _ any) {
			fmt.Fprintln(s.args.Out, "tool: "+name)
		},
	})
	if err != nil {
		content := "请求失败: " + err.Error()
		s.args.Messages = append(messages, message.AssistantMessage(content))
		if persistErr := s.persistMessages(); persistErr != nil {
			return persistErr
		}
		fmt.Fprintln(s.args.Out, content)
		return nil
	}
	s.args.Messages = next
	if err := s.persistMessages(); err != nil {
		return err
	}
	for i := len(next) - 1; i >= 0; i-- {
		if next[i].Role == message.RoleAssistant {
			fmt.Fprintln(s.args.Out, next[i].Content)
			break
		}
	}
	return nil
}

func (s *Session) persistMessages() error {
	if s.args.Store.Dir == "" || s.args.SessionID == "" {
		return nil
	}
	return s.args.Store.Save(Record{
		ID:       s.args.SessionID,
		CWD:      s.args.CWD,
		Messages: s.args.Messages,
	})
}
