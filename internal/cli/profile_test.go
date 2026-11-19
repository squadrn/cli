package cli

import "testing"

func TestWithoutProfileLines(t *testing.T) {
	c := `C:\Users\o'neil\.local\share\squadrn\completion.ps1`
	if got := profileLine(c); got != `if (Test-Path 'C:\Users\o''neil\.local\share\squadrn\completion.ps1') { . 'C:\Users\o''neil\.local\share\squadrn\completion.ps1' }` {
		t.Fatalf("profileLine = %s", got)
	}
	in := "Import-Module posh-git\r\n" + `. "` + c + `"` + "\r\n" + profileLine(c) + "\n" +
		`. "C:\elsewhere\completion.ps1"` + "\r\n# end"
	got, removed := withoutProfileLines(in, c)
	if want := "Import-Module posh-git\r\n" + `. "C:\elsewhere\completion.ps1"` + "\r\n# end"; !removed || got != want {
		t.Fatalf("got %q, %v", got, removed)
	}
	if _, removed := withoutProfileLines("# nothing of ours\n", c); removed {
		t.Fatal("removed a foreign line")
	}
}
