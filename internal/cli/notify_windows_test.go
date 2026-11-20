package cli

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestToastEnv(t *testing.T) {
	d := delivery{Title: "<b>t</b>", Body: "Done", Group: strings.Repeat("s", 80)}
	env := toastEnv(nil, d)
	for _, want := range []string{"SQUADRN_TOAST_TITLE=<b>t</b>", "SQUADRN_TOAST_BODY=Done", "SQUADRN_TOAST_SILENT=1", "SQUADRN_TOAST_TAG=" + strings.Repeat("s", 64), "SQUADRN_TOAST_APP=Squadrn"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %q", want, env)
		}
	}
	d.Sound = true
	if env := toastEnv(nil, d); !slices.Contains(env, "SQUADRN_TOAST_SILENT=0") {
		t.Errorf("sound: %q", env)
	}
}

func TestEncodePowerShell(t *testing.T) {
	b, err := base64.StdEncoding.DecodeString(encodePowerShell("é$x"))
	if err != nil || len(b)%2 != 0 {
		t.Fatal(err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	if got := string(utf16.Decode(u)); got != "é$x" {
		t.Errorf("decoded %q", got)
	}
	for _, r := range toastScript {
		if r > 127 {
			t.Fatalf("the toast script must stay ASCII: %q", r)
		}
	}
}
