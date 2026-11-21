package gemini

import (
	"encoding/json"
	"path/filepath"

	"github.com/squadrn/squadrn/internal/provider"
)

// Guard reads Gemini's BeforeTool payload and replies in its form.
type Guard struct{}

// shellTool is Gemini's shell tool.
const shellTool = "run_shell_command"

// ParseTool reads tool_input.command and its directory: tool_input.dir_path as the model wrote
// it, relative to cwd, else cwd. Any other tool has no Shell (the guard cannot judge it).
func (Guard) ParseTool(raw []byte) (provider.ToolCall, error) {
	var in struct {
		Cwd       string `json:"cwd"`
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
			DirPath string `json:"dir_path"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return provider.ToolCall{}, err
	}
	call := provider.ToolCall{Command: in.ToolInput.Command, Dir: in.Cwd, Tool: in.ToolName}
	if in.ToolName == shellTool {
		call.Shell = shell
	}
	if d := in.ToolInput.DirPath; d != "" {
		if filepath.IsAbs(d) || in.Cwd == "" {
			call.Dir = d
		} else {
			call.Dir = filepath.Join(in.Cwd, d)
		}
	}
	return call, nil
}

// Block: exit 2 with the reason on stderr blocks the call in Gemini, also in yolo mode; JSON in
// Claude Code's form would be ignored.
func (Guard) Block(reason string) (stdout, stderr string, code int) {
	return "", reason + "\n", 2
}
