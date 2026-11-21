package provider

import (
	"encoding/json"
	"testing"
)

func TestDropSquadrnHooks(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{"model":"m","hooks":{
		"BeforeTool":[
			{"matcher":"run_shell_command","hooks":[{"type":"command","name":"squadrn-guard","command":"\"/d/plugin/bin/squadrn-hook\" guard-git --no-ask --agent gemini"}]},
			{"matcher":"write_file","hooks":[{"type":"command","command":"lint"},{"type":"command","command":"& \"C:\\s\\bin\\squadrn.exe\" hook notify"}]},
			"odd"
		],
		"AfterAgent":[{"hooks":[{"type":"command","command":"squadrn-hook notify"}]}],
		"Notification":[{"hooks":"odd"}],
		"SessionEnd":{"odd":true}
	}}`), &doc); err != nil {
		t.Fatal(err)
	}
	DropSquadrnHooks(doc)
	got, _ := json.Marshal(doc)
	want := `{"hooks":{"BeforeTool":[{"hooks":[{"command":"lint","type":"command"}],"matcher":"write_file"},"odd"],"Notification":[{"hooks":"odd"}],"SessionEnd":{"odd":true}},"model":"m"}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	only := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"command": "squadrn-hook x"}}}}}}
	DropSquadrnHooks(only)
	if _, ok := only["hooks"]; ok {
		t.Fatalf("empty hooks kept: %v", only)
	}
	DropSquadrnHooks(map[string]any{"hooks": "odd"})
}

func TestSplitAndAddHooks(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{"hooks":{"BeforeTool":[
		{"matcher":"run_shell_command","hooks":[{"command":"lint"},{"command":"squadrn-hook guard-git"}]}]}}`), &doc); err != nil {
		t.Fatal(err)
	}
	ours := SplitSquadrnHooks(doc)
	got, _ := json.Marshal(ours)
	if string(got) != `{"BeforeTool":[{"hooks":[{"command":"squadrn-hook guard-git"}],"matcher":"run_shell_command"}]}` {
		t.Fatalf("split = %s", got)
	}
	AddHooks(doc, ours)
	got, _ = json.Marshal(doc)
	want := `{"hooks":{"BeforeTool":[{"hooks":[{"command":"lint"}],"matcher":"run_shell_command"},{"hooks":[{"command":"squadrn-hook guard-git"}],"matcher":"run_shell_command"}]}}`
	if string(got) != want {
		t.Fatalf("added = %s", got)
	}
	empty := map[string]any{}
	AddHooks(empty, nil)
	AddHooks(empty, map[string][]any{"Stop": {"x"}})
	if got, _ := json.Marshal(empty); string(got) != `{"hooks":{"Stop":["x"]}}` {
		t.Fatalf("into empty = %s", got)
	}
	if SplitSquadrnHooks(map[string]any{}) != nil {
		t.Fatal("no hooks")
	}
}

func TestSquadrnCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		`"/x/plugin/bin/squadrn-hook" guard-git`: true,
		`& "C:\x\squadrn.exe" hook guard-git`:    true,
		`my-linter`:                              false,
		`squadrn hook`:                           false,
	} {
		if got := SquadrnCommand(cmd); got != want {
			t.Errorf("SquadrnCommand(%q) = %v", cmd, got)
		}
	}
}
