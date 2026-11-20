//go:build windows

package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"

	"github.com/squadrn/squadrn/internal/proc"
)

// toastAppID is the AppUserModelID toasts are shown under. Windows shows a toast from an
// unpackaged app only for a registered id: HKCU\Software\Classes\AppUserModelId\<id> with a
// DisplayName (the registration Microsoft's toast compat library uses for desktop apps).
const toastAppID = "Squadrn"

const toastKey = `Software\Classes\AppUserModelId\` + toastAppID

// registerToastApp registers toastAppID for the current user; it is idempotent.
func registerToastApp() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, toastKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	return k.SetStringValue("DisplayName", "Squadrn")
}

// unregisterToastApp removes the registration (self-uninstall).
func unregisterToastApp() error {
	err := registry.DeleteKey(registry.CURRENT_USER, toastKey)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

// findNotifier uses Windows PowerShell 5.1, which can load the WinRT toast API (PowerShell 7
// cannot).
func findNotifier() (notifier, bool) {
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return notifier{Tool: "toast", Path: p}, true
	}
	return notifier{Note: "Windows PowerShell (powershell.exe) is not on PATH"}, false
}

// toastScript shows one toast. The text comes from environment variables, never from the script
// source, and goes into the XML as text nodes, so it cannot change the toast's markup. A toast
// with the same tag and group replaces the session's previous one.
const toastScript = `$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null
$x = New-Object Windows.Data.Xml.Dom.XmlDocument
$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text/><text/></binding></visual><audio/></toast>')
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode($env:SQUADRN_TOAST_TITLE)) > $null
$t.Item(1).AppendChild($x.CreateTextNode($env:SQUADRN_TOAST_BODY)) > $null
if ($env:SQUADRN_TOAST_SILENT -eq '1') { $x.GetElementsByTagName('audio').Item(0).SetAttribute('silent', 'true') }
$n = New-Object Windows.UI.Notifications.ToastNotification $x
if ($env:SQUADRN_TOAST_TAG) { $n.Tag = $env:SQUADRN_TOAST_TAG; $n.Group = 'squadrn' }
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:SQUADRN_TOAST_APP).Show($n)
`

func (n notifier) send(ctx context.Context, d delivery, _ string) (string, error) {
	if err := registerToastApp(); err != nil {
		return "", err
	}
	_, err := proc.Output(ctx, proc.Options{Env: toastEnv(os.Environ(), d)}, n.Path,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodePowerShell(toastScript))
	return "", err
}

func toastEnv(env []string, d delivery) []string {
	silent := "1"
	if d.Sound {
		silent = "0"
	}
	tag := d.Group
	if len(tag) > 64 { // the toast tag limit
		tag = tag[:64]
	}
	return append(env, "SQUADRN_TOAST_TITLE="+d.Title, "SQUADRN_TOAST_BODY="+d.Body,
		"SQUADRN_TOAST_SILENT="+silent, "SQUADRN_TOAST_TAG="+tag, "SQUADRN_TOAST_APP="+toastAppID)
}

// encodePowerShell is the -EncodedCommand form: base64 of the UTF-16LE script.
func encodePowerShell(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func terminalBundle() string { return "" }

func terminalFrontmost(context.Context) (front, known bool) { return false, false }

func focusSources() string { return "unknown on Windows: every notification is shown" }
