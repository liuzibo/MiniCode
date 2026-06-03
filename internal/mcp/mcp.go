package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ssbsunshengbo/minicode-go/internal/config"
	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

type Result struct {
	Tools   []tools.Definition
	Servers []tools.MCPServerSummary
	Dispose func(context.Context) error
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	msg jsonRPCMessage
	err error
}

type toolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type resourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type promptDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type stdioClient struct {
	serverName  string
	config      config.MCPServerConfig
	cwd         string
	protocol    string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	reader      *bufio.Reader
	nextID      int
	mu          sync.Mutex
	pending     map[int]chan rpcResponse
	stderrMu    sync.Mutex
	stderrLines []string
}

func CreateBackedTools(ctx context.Context, cwd string, servers map[string]config.MCPServerConfig) Result {
	clients := []*stdioClient{}
	definitions := []tools.Definition{}
	summaries := []tools.MCPServerSummary{}
	resourceIndex := map[string]resourceEntry{}
	promptIndex := map[string]promptEntry{}

	for serverName, serverConfig := range servers {
		if serverConfig.Enabled != nil && !*serverConfig.Enabled {
			summaries = append(summaries, tools.MCPServerSummary{
				Name:      serverName,
				Command:   serverConfig.Command,
				Status:    "disabled",
				ToolCount: 0,
				Protocol:  configuredProtocol(serverConfig.Protocol),
			})
			continue
		}

		client := &stdioClient{serverName: serverName, config: serverConfig, cwd: cwd}
		if err := client.start(ctx); err != nil {
			_ = client.close()
			summaries = append(summaries, tools.MCPServerSummary{
				Name:      serverName,
				Command:   serverConfig.Command,
				Status:    "error",
				ToolCount: 0,
				Error:     err.Error(),
				Protocol:  configuredProtocol(serverConfig.Protocol),
			})
			continue
		}
		clients = append(clients, client)

		descriptors, err := client.listTools(ctx)
		if err != nil {
			descriptors = nil
		}
		resources, err := client.listResources(ctx)
		if err != nil {
			resources = nil
		}
		prompts, err := client.listPrompts(ctx)
		if err != nil {
			prompts = nil
		}

		for _, resource := range resources {
			resourceIndex[serverName+":"+resource.URI] = resourceEntry{serverName: serverName, resource: resource, client: client}
		}
		for _, prompt := range prompts {
			promptIndex[serverName+":"+prompt.Name] = promptEntry{serverName: serverName, prompt: prompt, client: client}
		}
		for _, descriptor := range descriptors {
			descriptor := descriptor
			wrappedName := "mcp__" + SanitizeToolSegment(serverName) + "__" + SanitizeToolSegment(descriptor.Name)
			description := strings.TrimSpace(descriptor.Description)
			if description == "" {
				description = "Call MCP tool " + descriptor.Name + " from server " + serverName + "."
			}
			definitions = append(definitions, tools.Definition{
				Name:        wrappedName,
				Description: description,
				InputSchema: normalizeInputSchema(descriptor.InputSchema),
				Run: func(ctx context.Context, raw json.RawMessage, _ tools.Context) tools.Result {
					var input any = map[string]any{}
					if len(raw) > 0 {
						_ = json.Unmarshal(raw, &input)
					}
					return client.callTool(ctx, descriptor.Name, input)
				},
			})
		}

		summaries = append(summaries, tools.MCPServerSummary{
			Name:          serverName,
			Command:       serverConfig.Command,
			Status:        "connected",
			ToolCount:     len(descriptors),
			Protocol:      client.protocol,
			ResourceCount: len(resources),
			PromptCount:   len(prompts),
		})
	}

	definitions = append(definitions, resourceTools(resourceIndex)...)
	definitions = append(definitions, promptTools(promptIndex)...)

	return Result{
		Tools:   definitions,
		Servers: summaries,
		Dispose: func(context.Context) error {
			var firstErr error
			for _, client := range clients {
				if err := client.close(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	}
}

func (c *stdioClient) start(ctx context.Context) error {
	if strings.TrimSpace(c.config.Command) == "" {
		return fmt.Errorf("MCP server %q has no command configured", c.serverName)
	}
	var lastErr error
	for _, protocol := range protocolCandidates(c.config.Protocol) {
		if err := c.spawn(protocol); err != nil {
			lastErr = err
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := c.request(requestCtx, "initialize", map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "mini-code", "version": "0.1.0"},
		})
		cancel()
		if err == nil {
			_ = c.notify("notifications/initialized", map[string]any{})
			return nil
		}
		lastErr = err
		_ = c.close()
	}
	return lastErr
}

func (c *stdioClient) spawn(protocol string) error {
	commandCWD := c.cwd
	if c.config.CWD != "" {
		commandCWD = filepath.Join(c.cwd, c.config.CWD)
	}
	cmd := exec.Command(c.config.Command, c.config.Args...)
	cmd.Dir = commandCWD
	cmd.Env = os.Environ()
	for key, value := range c.config.Env {
		cmd.Env = append(cmd.Env, key+"="+fmt.Sprint(value))
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	c.stdin = stdin
	c.reader = bufio.NewReader(stdout)
	c.protocol = protocol
	c.nextID = 1
	c.pending = map[int]chan rpcResponse{}
	c.stderrLines = nil
	go c.captureStderr(stderr)
	go c.readLoop()
	return nil
}

func (c *stdioClient) listTools(ctx context.Context) ([]toolDescriptor, error) {
	var out struct {
		Tools []toolDescriptor `json:"tools"`
	}
	if err := c.requestInto(ctx, "tools/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

func (c *stdioClient) listResources(ctx context.Context) ([]resourceDescriptor, error) {
	var out struct {
		Resources []resourceDescriptor `json:"resources"`
	}
	if err := c.requestInto(ctx, "resources/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Resources, nil
}

func (c *stdioClient) listPrompts(ctx context.Context) ([]promptDescriptor, error) {
	var out struct {
		Prompts []promptDescriptor `json:"prompts"`
	}
	if err := c.requestInto(ctx, "prompts/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Prompts, nil
}

func (c *stdioClient) callTool(ctx context.Context, name string, input any) tools.Result {
	result, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": input})
	if err != nil {
		return tools.Error(err.Error())
	}
	return formatToolCallResult(result)
}

func (c *stdioClient) readResource(ctx context.Context, uri string) tools.Result {
	result, err := c.request(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return tools.Error(err.Error())
	}
	return formatReadResourceResult(result)
}

func (c *stdioClient) getPrompt(ctx context.Context, name string, args map[string]string) tools.Result {
	result, err := c.request(ctx, "prompts/get", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return tools.Error(err.Error())
	}
	return formatPromptResult(result)
}

func (c *stdioClient) requestInto(ctx context.Context, method string, params any, target any) error {
	result, err := c.request(ctx, method, params)
	if err != nil {
		return err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (c *stdioClient) request(ctx context.Context, method string, params any) (any, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	if err := c.send(jsonRPCMessage{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("MCP %s: request timed out for %s%s", c.serverName, method, c.stderrSuffix())
	case result := <-ch:
		if result.err != nil {
			return nil, result.err
		}
		if result.msg.Error != nil {
			return nil, fmt.Errorf("MCP %s: %s", c.serverName, result.msg.Error.Message)
		}
		var decoded any
		if len(result.msg.Result) > 0 {
			if err := json.Unmarshal(result.msg.Result, &decoded); err != nil {
				return nil, err
			}
		}
		return decoded, nil
	}
}

func (c *stdioClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.send(jsonRPCMessage{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *stdioClient) send(msg jsonRPCMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if c.protocol == "newline-json" {
		_, err = fmt.Fprintln(c.stdin, string(body))
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func (c *stdioClient) readMessage() (jsonRPCMessage, error) {
	var data []byte
	var err error
	if c.protocol == "newline-json" {
		data, err = c.reader.ReadBytes('\n')
	} else {
		data, err = readContentLengthMessage(c.reader)
	}
	if err != nil {
		return jsonRPCMessage{}, err
	}
	var msg jsonRPCMessage
	return msg, json.Unmarshal(bytes.TrimSpace(data), &msg)
}

func (c *stdioClient) readLoop() {
	for {
		msg, err := c.readMessage()
		if err != nil {
			c.failPending(err)
			return
		}
		id, ok := messageID(msg.ID)
		if !ok {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- rpcResponse{msg: msg}
		}
	}
}

func (c *stdioClient) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[int]chan rpcResponse{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcResponse{err: err}
	}
}

func (c *stdioClient) captureStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		c.stderrMu.Lock()
		c.stderrLines = append(c.stderrLines, line)
		if len(c.stderrLines) > 8 {
			c.stderrLines = c.stderrLines[len(c.stderrLines)-8:]
		}
		c.stderrMu.Unlock()
	}
}

func (c *stdioClient) stderrSuffix() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	if len(c.stderrLines) == 0 {
		return ""
	}
	return "\n" + strings.Join(c.stderrLines, "\n")
}

func (c *stdioClient) close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	c.cmd = nil
	c.failPending(fmt.Errorf("MCP server %q is not running", c.serverName))
	return nil
}

func readContentLengthMessage(reader io.Reader) ([]byte, error) {
	header := []byte{}
	buf := make([]byte, 1)
	for !bytes.Contains(header, []byte("\r\n\r\n")) {
		if _, err := reader.Read(buf); err != nil {
			return nil, err
		}
		header = append(header, buf[0])
	}
	parts := bytes.SplitN(header, []byte("\r\n\r\n"), 2)
	contentLength := 0
	for _, line := range strings.Split(string(parts[0]), "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			contentLength, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]))
			break
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	return body, err
}

type resourceEntry struct {
	serverName string
	resource   resourceDescriptor
	client     *stdioClient
}

type promptEntry struct {
	serverName string
	prompt     promptDescriptor
	client     *stdioClient
}

func resourceTools(index map[string]resourceEntry) []tools.Definition {
	if len(index) == 0 {
		return nil
	}
	return []tools.Definition{
		{
			Name:        "list_mcp_resources",
			Description: "List available MCP resources exposed by connected MCP servers.",
			InputSchema: objectSchema(map[string]any{"server": map[string]any{"type": "string"}}, nil),
			Run: func(_ context.Context, raw json.RawMessage, _ tools.Context) tools.Result {
				var input struct {
					Server string `json:"server"`
				}
				_ = json.Unmarshal(raw, &input)
				lines := []string{}
				for _, entry := range index {
					if input.Server != "" && input.Server != entry.serverName {
						continue
					}
					line := entry.serverName + ": " + entry.resource.URI
					if entry.resource.Name != "" {
						line += " (" + entry.resource.Name + ")"
					}
					if entry.resource.Description != "" {
						line += " - " + entry.resource.Description
					}
					lines = append(lines, line)
				}
				if len(lines) == 0 {
					return tools.Success("No MCP resources available.")
				}
				return tools.Success(strings.Join(lines, "\n"))
			},
		},
		{
			Name:        "read_mcp_resource",
			Description: "Read a resource exposed by a connected MCP server.",
			InputSchema: objectSchema(map[string]any{"server": map[string]any{"type": "string"}, "uri": map[string]any{"type": "string"}}, []string{"uri"}),
			Run: func(ctx context.Context, raw json.RawMessage, _ tools.Context) tools.Result {
				var input struct {
					Server string `json:"server"`
					URI    string `json:"uri"`
				}
				if err := json.Unmarshal(raw, &input); err != nil {
					return tools.Error(err.Error())
				}
				entry, ok := findResource(index, input.Server, input.URI)
				if !ok {
					return tools.Error("Unknown MCP resource: " + input.URI)
				}
				return entry.client.readResource(ctx, entry.resource.URI)
			},
		},
	}
}

func promptTools(index map[string]promptEntry) []tools.Definition {
	if len(index) == 0 {
		return nil
	}
	return []tools.Definition{
		{
			Name:        "list_mcp_prompts",
			Description: "List available MCP prompts exposed by connected MCP servers.",
			InputSchema: objectSchema(map[string]any{"server": map[string]any{"type": "string"}}, nil),
			Run: func(_ context.Context, raw json.RawMessage, _ tools.Context) tools.Result {
				var input struct {
					Server string `json:"server"`
				}
				_ = json.Unmarshal(raw, &input)
				lines := []string{}
				for _, entry := range index {
					if input.Server != "" && input.Server != entry.serverName {
						continue
					}
					line := entry.serverName + ": " + entry.prompt.Name
					if entry.prompt.Description != "" {
						line += " - " + entry.prompt.Description
					}
					lines = append(lines, line)
				}
				if len(lines) == 0 {
					return tools.Success("No MCP prompts available.")
				}
				return tools.Success(strings.Join(lines, "\n"))
			},
		},
		{
			Name:        "get_mcp_prompt",
			Description: "Get a prompt exposed by a connected MCP server.",
			InputSchema: objectSchema(map[string]any{"server": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "arguments": map[string]any{"type": "object"}}, []string{"name"}),
			Run: func(ctx context.Context, raw json.RawMessage, _ tools.Context) tools.Result {
				var input struct {
					Server    string            `json:"server"`
					Name      string            `json:"name"`
					Arguments map[string]string `json:"arguments"`
				}
				if err := json.Unmarshal(raw, &input); err != nil {
					return tools.Error(err.Error())
				}
				entry, ok := findPrompt(index, input.Server, input.Name)
				if !ok {
					return tools.Error("Unknown MCP prompt: " + input.Name)
				}
				return entry.client.getPrompt(ctx, entry.prompt.Name, input.Arguments)
			},
		},
	}
}

func findResource(index map[string]resourceEntry, server, uri string) (resourceEntry, bool) {
	if server != "" {
		entry, ok := index[server+":"+uri]
		return entry, ok
	}
	for _, entry := range index {
		if entry.resource.URI == uri {
			return entry, true
		}
	}
	return resourceEntry{}, false
}

func findPrompt(index map[string]promptEntry, server, name string) (promptEntry, bool) {
	if server != "" {
		entry, ok := index[server+":"+name]
		return entry, ok
	}
	for _, entry := range index {
		if entry.prompt.Name == name {
			return entry, true
		}
	}
	return promptEntry{}, false
}

func formatToolCallResult(result any) tools.Result {
	data, _ := json.Marshal(result)
	var parsed struct {
		Content           []map[string]any `json:"content"`
		StructuredContent any              `json:"structuredContent"`
		IsError           bool             `json:"isError"`
	}
	_ = json.Unmarshal(data, &parsed)
	parts := []string{}
	for _, block := range parsed.Content {
		if block["type"] == "text" {
			parts = append(parts, fmt.Sprint(block["text"]))
		} else {
			parts = append(parts, mustPretty(block))
		}
	}
	if parsed.StructuredContent != nil {
		parts = append(parts, "STRUCTURED_CONTENT:\n"+mustPretty(parsed.StructuredContent))
	}
	if len(parts) == 0 {
		parts = append(parts, mustPretty(result))
	}
	return tools.Result{OK: !parsed.IsError, Output: strings.TrimSpace(strings.Join(parts, "\n\n"))}
}

func formatReadResourceResult(result any) tools.Result {
	data, _ := json.Marshal(result)
	var parsed struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
			Blob     string `json:"blob"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return tools.Error(err.Error())
	}
	if len(parsed.Contents) == 0 {
		return tools.Success("No resource contents returned.")
	}
	parts := []string{}
	for _, item := range parsed.Contents {
		header := "URI: " + defaultString(item.URI, "(unknown)")
		if item.MimeType != "" {
			header += "\nMIME: " + item.MimeType
		}
		body := item.Text
		if body == "" && item.Blob != "" {
			body = "BLOB:\n" + item.Blob
		}
		parts = append(parts, header+"\n\n"+body)
	}
	return tools.Success(strings.Join(parts, "\n\n"))
}

func formatPromptResult(result any) tools.Result {
	data, _ := json.Marshal(result)
	var parsed struct {
		Description string `json:"description"`
		Messages    []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return tools.Error(err.Error())
	}
	parts := []string{}
	if parsed.Description != "" {
		parts = append(parts, "DESCRIPTION: "+parsed.Description)
	}
	for _, msg := range parsed.Messages {
		role := defaultString(msg.Role, "unknown")
		content := fmt.Sprint(msg.Content)
		if text, ok := msg.Content.(string); ok {
			content = text
		} else {
			content = mustPretty(msg.Content)
		}
		parts = append(parts, "["+role+"]\n"+content)
	}
	if len(parts) == 0 {
		return tools.Success(mustPretty(result))
	}
	return tools.Success(strings.Join(parts, "\n\n"))
}

func normalizeInputSchema(schema map[string]any) map[string]any {
	if schema != nil {
		return schema
	}
	return map[string]any{"type": "object", "additionalProperties": true}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if required != nil {
		schema["required"] = required
	}
	return schema
}

func protocolCandidates(protocol string) []string {
	switch protocol {
	case "content-length":
		return []string{"content-length"}
	case "newline-json":
		return []string{"newline-json"}
	default:
		return []string{"content-length", "newline-json"}
	}
}

func configuredProtocol(protocol string) string {
	if protocol == "" || protocol == "auto" {
		return ""
	}
	return protocol
}

func sameID(got any, want int) bool {
	switch typed := got.(type) {
	case float64:
		return int(typed) == want
	case int:
		return typed == want
	case json.Number:
		value, _ := typed.Int64()
		return int(value) == want
	default:
		return fmt.Sprint(got) == strconv.Itoa(want)
	}
}

func messageID(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	default:
		parsed, err := strconv.Atoi(fmt.Sprint(value))
		return parsed, err == nil
	}
}

func SanitizeToolSegment(value string) string {
	lower := strings.ToLower(value)
	re := regexp.MustCompile(`[^a-z0-9_-]+`)
	out := strings.Trim(re.ReplaceAllString(lower, "_"), "_")
	if out == "" {
		return "tool"
	}
	return out
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func mustPretty(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
