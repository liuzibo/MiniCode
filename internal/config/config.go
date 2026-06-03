package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

type Settings struct {
	Env             map[string]any             `json:"env,omitempty"`
	Provider        string                     `json:"provider,omitempty"`
	Model           string                     `json:"model,omitempty"`
	MaxOutputTokens int                        `json:"maxOutputTokens,omitempty"`
	MCPServers      map[string]MCPServerConfig `json:"mcpServers,omitempty"`
}

type MCPServerConfig struct {
	Command  string         `json:"command"`
	Args     []string       `json:"args,omitempty"`
	Env      map[string]any `json:"env,omitempty"`
	CWD      string         `json:"cwd,omitempty"`
	Enabled  *bool          `json:"enabled,omitempty"`
	Protocol string         `json:"protocol,omitempty"`
}

type Runtime struct {
	Provider        string
	Model           string
	BaseURL         string
	AuthToken       string
	APIKey          string
	MaxOutputTokens int
	MCPServers      map[string]MCPServerConfig
	SourceSummary   string
}

func MiniCodeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mini-code")
}

func SettingsPath() string {
	return filepath.Join(MiniCodeDir(), "settings.json")
}

func MCPPath() string {
	return filepath.Join(MiniCodeDir(), "mcp.json")
}

func PermissionsPath() string {
	return filepath.Join(MiniCodeDir(), "permissions.json")
}

func HistoryPath() string {
	return filepath.Join(MiniCodeDir(), "history.json")
}

func SessionsDir() string {
	return filepath.Join(MiniCodeDir(), "sessions")
}

func ClaudeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func ProjectMCPPath(cwd string) string {
	return filepath.Join(cwd, ".mcp.json")
}

func LoadRuntime(cwd string) (Runtime, error) {
	settings, err := LoadEffectiveSettings(cwd)
	if err != nil {
		return Runtime{}, err
	}

	env := map[string]string{}
	for key, value := range settings.Env {
		env[key] = stringify(value)
	}
	for _, item := range os.Environ() {
		for i := 0; i < len(item); i++ {
			if item[i] == '=' {
				env[item[:i]] = item[i+1:]
				break
			}
		}
	}

	provider := firstNonEmpty(os.Getenv("MINI_CODE_PROVIDER"), env["MINI_CODE_PROVIDER"], settings.Provider, "anthropic")
	model := firstNonEmpty(os.Getenv("MINI_CODE_MODEL"), settings.Model, providerModelEnv(provider, env))
	baseURL := firstNonEmpty(providerBaseURLEnv(provider, env), defaultBaseURL(provider))
	authToken := providerAuthTokenEnv(provider, env)
	apiKey := providerAPIKeyEnv(provider, env)
	maxTokens := settings.MaxOutputTokens
	if raw := firstNonEmpty(os.Getenv("MINI_CODE_MAX_OUTPUT_TOKENS"), env["MINI_CODE_MAX_OUTPUT_TOKENS"]); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxTokens = parsed
		}
	}

	if model == "" {
		return Runtime{}, errors.New("No model configured. Set ~/.mini-code/settings.json or env.ANTHROPIC_MODEL.")
	}
	if authToken == "" && apiKey == "" {
		return Runtime{}, errors.New("No auth configured. Set ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY in ~/.mini-code/settings.json or process env.")
	}

	return Runtime{
		Provider:        provider,
		Model:           model,
		BaseURL:         baseURL,
		AuthToken:       authToken,
		APIKey:          apiKey,
		MaxOutputTokens: maxTokens,
		MCPServers:      settings.MCPServers,
		SourceSummary:   "config: " + SettingsPath() + " > " + ClaudeSettingsPath() + " > process.env",
	}, nil
}

func LoadEffectiveSettings(cwd string) (Settings, error) {
	claude, err := readSettings(ClaudeSettingsPath())
	if err != nil {
		return Settings{}, err
	}
	globalMCP, err := ReadMCPConfig(MCPPath())
	if err != nil {
		return Settings{}, err
	}
	projectMCP, err := ReadMCPConfig(ProjectMCPPath(cwd))
	if err != nil {
		return Settings{}, err
	}
	mini, err := readSettings(SettingsPath())
	if err != nil {
		return Settings{}, err
	}

	merged := mergeSettings(claude, Settings{MCPServers: globalMCP})
	merged = mergeSettings(merged, Settings{MCPServers: projectMCP})
	return mergeSettings(merged, mini), nil
}

func ReadMCPConfig(path string) (map[string]MCPServerConfig, error) {
	var parsed struct {
		MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	}
	if err := readJSON(path, &parsed); err != nil {
		return nil, err
	}
	if parsed.MCPServers == nil {
		return map[string]MCPServerConfig{}, nil
	}
	return parsed.MCPServers, nil
}

func SaveMCPConfig(path string, servers map[string]MCPServerConfig) error {
	return writeJSON(path, map[string]any{"mcpServers": servers})
}

func SaveSettings(updates Settings) error {
	existing, err := readSettings(SettingsPath())
	if err != nil {
		return err
	}
	return writeJSON(SettingsPath(), mergeSettings(existing, updates))
}

func readSettings(path string) (Settings, error) {
	var settings Settings
	if err := readJSON(path, &settings); err != nil {
		return Settings{}, err
	}
	if settings.Env == nil {
		settings.Env = map[string]any{}
	}
	if settings.MCPServers == nil {
		settings.MCPServers = map[string]MCPServerConfig{}
	}
	return settings, nil
}

func readJSON(path string, target any) error {
	bytes, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, target)
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(bytes, '\n'), 0o644)
}

func mergeSettings(base, override Settings) Settings {
	out := base
	if override.Provider != "" {
		out.Provider = override.Provider
	}
	if override.Model != "" {
		out.Model = override.Model
	}
	if override.MaxOutputTokens != 0 {
		out.MaxOutputTokens = override.MaxOutputTokens
	}
	if out.Env == nil {
		out.Env = map[string]any{}
	}
	for key, value := range override.Env {
		out.Env[key] = value
	}
	if out.MCPServers == nil {
		out.MCPServers = map[string]MCPServerConfig{}
	}
	for name, server := range override.MCPServers {
		previous := out.MCPServers[name]
		if previous.Env == nil {
			previous.Env = map[string]any{}
		}
		for key, value := range server.Env {
			previous.Env[key] = value
		}
		if server.Command != "" {
			previous.Command = server.Command
		}
		if server.Args != nil {
			previous.Args = server.Args
		}
		if server.CWD != "" {
			previous.CWD = server.CWD
		}
		if server.Enabled != nil {
			previous.Enabled = server.Enabled
		}
		if server.Protocol != "" {
			previous.Protocol = server.Protocol
		}
		out.MCPServers[name] = previous
	}
	return out
}

func providerModelEnv(provider string, env map[string]string) string {
	switch provider {
	case "openai":
		return env["OPENAI_MODEL"]
	default:
		return env["ANTHROPIC_MODEL"]
	}
}

func providerBaseURLEnv(provider string, env map[string]string) string {
	switch provider {
	case "openai":
		return env["OPENAI_BASE_URL"]
	default:
		return env["ANTHROPIC_BASE_URL"]
	}
}

func providerAuthTokenEnv(provider string, env map[string]string) string {
	switch provider {
	case "openai":
		return env["OPENAI_AUTH_TOKEN"]
	default:
		return env["ANTHROPIC_AUTH_TOKEN"]
	}
}

func providerAPIKeyEnv(provider string, env map[string]string) string {
	switch provider {
	case "openai":
		return env["OPENAI_API_KEY"]
	default:
		return env["ANTHROPIC_API_KEY"]
	}
}

func defaultBaseURL(provider string) string {
	switch provider {
	case "openai":
		return "https://api.openai.com"
	default:
		return "https://api.anthropic.com"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}
