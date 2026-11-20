package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// mcpServer is a server definition in Claude Code's mcpServers form, the form squadrn shares.
type mcpServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (c CLI) HasMCP(ctx context.Context, configDir, name string) bool {
	_, err := c.Output(ctx, configDir, "mcp", "get", name)
	return err == nil
}

// AddMCP adds a server with `codex mcp add`, from its definition in Claude Code's form.
func (c CLI) AddMCP(ctx context.Context, configDir, name string, config []byte) error {
	args, err := mcpAddArgs(name, config)
	if err != nil {
		return err
	}
	_, err = c.Output(ctx, configDir, args...)
	return err
}

func mcpAddArgs(name string, config []byte) ([]string, error) {
	var s mcpServer
	if err := json.Unmarshal(config, &s); err != nil {
		return nil, fmt.Errorf("MCP server %s: %w", name, err)
	}
	switch {
	case len(s.Headers) > 0:
		return nil, fmt.Errorf("MCP server %s: codex mcp add cannot set HTTP headers", name)
	case s.URL != "" && (s.Type == "" || s.Type == "http" || s.Type == "streamable_http"):
		return []string{"mcp", "add", name, "--url", s.URL}, nil
	case s.URL != "":
		return nil, fmt.Errorf("MCP server %s: Codex does not support %s servers", name, s.Type)
	case s.Command == "":
		return nil, fmt.Errorf("MCP server %s: no command or url", name)
	}
	args := []string{"mcp", "add", name}
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		args = append(args, "--env", k+"="+s.Env[k])
	}
	return append(append(args, "--", s.Command), s.Args...), nil
}

// ListMCP reads `codex mcp list --json` into Claude Code's mcpServers form.
func (c CLI) ListMCP(ctx context.Context, configDir string) (map[string]json.RawMessage, error) {
	out, err := c.Output(ctx, configDir, "mcp", "list", "--json")
	if err != nil {
		return nil, err
	}
	return parseMCPList(out)
}

func parseMCPList(out []byte) (map[string]json.RawMessage, error) {
	var list []struct {
		Name      string `json:"name"`
		Transport struct {
			Type        string            `json:"type"`
			Command     string            `json:"command"`
			Args        []string          `json:"args"`
			Env         map[string]string `json:"env"`
			URL         string            `json:"url"`
			HTTPHeaders map[string]string `json:"http_headers"`
		} `json:"transport"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse codex mcp list: %w", err)
	}
	servers := make(map[string]json.RawMessage, len(list))
	for _, e := range list {
		t := e.Transport
		var s mcpServer
		switch t.Type {
		case "stdio":
			s = mcpServer{Command: t.Command, Args: t.Args, Env: t.Env}
		case "streamable_http":
			s = mcpServer{Type: "http", URL: t.URL, Headers: t.HTTPHeaders}
		default:
			continue
		}
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		if e.Name == "" {
			return nil, errors.New("parse codex mcp list: a server has no name")
		}
		servers[e.Name] = raw
	}
	return servers, nil
}
