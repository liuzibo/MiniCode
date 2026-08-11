package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ssbsunshengbo/minicode-go/internal/config"
	"github.com/ssbsunshengbo/minicode-go/internal/manage"
	"github.com/ssbsunshengbo/minicode-go/internal/mcp"
	"github.com/ssbsunshengbo/minicode-go/internal/message"
	"github.com/ssbsunshengbo/minicode-go/internal/model"
	"github.com/ssbsunshengbo/minicode-go/internal/permissions"
	"github.com/ssbsunshengbo/minicode-go/internal/prompt"
	"github.com/ssbsunshengbo/minicode-go/internal/session"
	"github.com/ssbsunshengbo/minicode-go/internal/skills"
	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

func main() {
	// 错误处理
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, argv []string) error {
	// 获取当前路径
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	startup, err := parseStartupArgs(argv)
	if err != nil {
		return err
	}
	// 拦截命令
	if out, handled, err := manage.Handle(ctx, cwd, startup.ManagementArgs); handled || err != nil {
		if out != "" {
			fmt.Println(out)
		}
		return err
	}
	// 加载配置
	runtime, runtimeErr := config.LoadRuntime(cwd)
	// 扫描skill
	skillStore := skills.NewStore(cwd, homeDir())
	discoveredSkills, _ := skillStore.Discover(ctx)
	// 注册内置工具
	toolRegistry := tools.Builtins(cwd, nil, skillStore)
	// MCP servers
	mcpResult := mcp.CreateBackedTools(ctx, cwd, runtime.MCPServers)
	definitions := append(toolRegistry.List(), mcpResult.Tools...)
	toolRegistry = tools.NewRegistry(definitions, tools.Metadata{Skills: discoveredSkills, MCPServers: mcpResult.Servers}).WithDisposer(mcpResult.Dispose)
	defer func() {
		_ = toolRegistry.Dispose(ctx)
	}()
	// 权限管理器，构建提示词
	pm, _ := permissions.New(cwd, config.PermissionsPath(), nil)
	systemPrompt := prompt.Build(ctx, prompt.Args{
		CWD:               cwd,
		PermissionSummary: pm.Summary(),
		Skills:            discoveredSkills,
		MCPServers:        toolRegistry.MCPServers(),
	})
	// fmt.Println(systemPrompt)
	// 创建模型适配器
	var adapter message.Model
	mockMode := os.Getenv("MINI_CODE_MODEL_MODE") == "mock"
	if mockMode {
		adapter = model.Mock{}
	} else if runtimeErr != nil {
		adapter = model.ErrorModel{Err: runtimeErr}
	} else {
		adapter, err = model.NewFromRuntime(runtime, toolRegistry)
		if err != nil {
			return err
		}
	}

	// 会话创建/恢复
	store := session.Store{Dir: config.SessionsDir()}
	sessionID := startup.ResumeID
	messages := []message.Message{message.SystemMessage(systemPrompt)}
	if sessionID != "" {
		record, err := loadSessionRecord(store, sessionID)
		if err != nil {
			return err
		}
		sessionID = record.ID
		messages = record.Messages
	} else {
		record := session.NewRecord(cwd, messages)
		sessionID = record.ID
	}

	// 启动会话
	app := session.New(session.Args{
		CWD:        cwd,
		Tools:      toolRegistry,
		Model:      adapter,
		Runtime:    runtimePtr(runtime, runtimeErr),
		Messages:   messages,
		Permission: pm,
		History:    session.History{Path: config.HistoryPath()},
		Store:      store,
		SessionID:  sessionID,
	})
	if isTerminal(os.Stdin) && isTerminal(os.Stdout) {
		return app.RunTUI(ctx)
	}
	fmt.Println("MiniCode Go")
	if mockMode {
		fmt.Println("model: mock/offline")
	} else if runtimeErr != nil {
		fmt.Println("model: not-configured")
	} else {
		fmt.Println("provider:", runtime.Provider)
		fmt.Println("model:", runtime.Model)
	}
	return app.Run(ctx)
}

type startupArgs struct {
	ResumeID       string
	ManagementArgs []string
}

func parseStartupArgs(argv []string) (startupArgs, error) {
	out := startupArgs{ManagementArgs: []string{}}
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--resume":
			if i+1 >= len(argv) || strings.TrimSpace(argv[i+1]) == "" {
				return startupArgs{}, fmt.Errorf("missing value for --resume")
			}
			out.ResumeID = argv[i+1]
			i++
		default:
			out.ManagementArgs = append(out.ManagementArgs, argv[i])
		}
	}
	return out, nil
}

func loadSessionRecord(store session.Store, id string) (session.Record, error) {
	if id == "latest" {
		return store.Latest()
	}
	return store.Load(id)
}

func usage() string {
	return "minicode"
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func runtimePtr(runtime config.Runtime, err error) *config.Runtime {
	if err != nil {
		return nil
	}
	return &runtime
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}
