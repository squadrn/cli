# End-to-end check of a Windows install, run by CI on windows-latest (and by hand on a Windows
# machine): install, command copies, guard hook, reinstall over a running binary, rollback,
# uninstall. Uses a throwaway USERPROFILE so nothing of the machine's user is touched.
$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2
function Check([bool]$ok, [string]$what) {
    if (-not $ok) { throw "FAILED: $what" }
    Write-Host "ok  $what"
}

$root = Join-Path ([IO.Path]::GetTempPath()) ("squadrn-e2e-" + [guid]::NewGuid())
$env:USERPROFILE = Join-Path $root "home"
$env:HOME = $env:USERPROFILE
foreach ($k in "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME") { Remove-Item "Env:$k" -ErrorAction SilentlyContinue }
$env:SQUADRN_NO_UPDATE_CHECK = "1"
New-Item -ItemType Directory -Force -Path (Join-Path $env:USERPROFILE ".claude") | Out-Null

function Release([string]$dir, [string]$version) {
    New-Item -ItemType Directory -Force -Path "$dir\plugin\hooks", "$dir\.claude-plugin" | Out-Null
    & go build -trimpath -ldflags "-X github.com/squadrn/squadrn/internal/cli.Version=$version" -o "$dir\squadrn.exe" ./cmd/squadrn
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    Copy-Item -Recurse -Force plugin\.claude-plugin, plugin\agents, plugin\README.md "$dir\plugin\"
    Copy-Item -Force scripts\hooks.windows.json "$dir\plugin\hooks\hooks.json"
    Copy-Item -Force .claude-plugin\marketplace.json "$dir\.claude-plugin\"
}

Release "$root\rel1" "1.0.0"
Release "$root\rel2" "1.1.0"
$bin = Join-Path $env:USERPROFILE ".local\bin"
$data = Join-Path $env:USERPROFILE ".local\share\squadrn"

& "$root\rel1\squadrn.exe" self-install "$root\rel1" --alias sx
Check ($LASTEXITCODE -eq 0) "self-install 1.0.0"
Check (Test-Path "$bin\srn.exe") "srn.exe copy in .local\bin"
Check (-not (Test-Path "$bin\squadrn.exe")) "no squadrn.exe: --alias replaces the default alias"
Check (Test-Path "$bin\sx.exe") "alias sx.exe"
Check ((& "$bin\srn.exe" version) -eq "squadrn 1.0.0") "version through the copy"
Check (Test-Path "$data\git-hooks\pre-push") "git hook shims"
$hooksJson = Get-Content "$data\plugin\hooks\hooks.json" -Raw
Check (($hooksJson -match '"onFailure": "block"') -and ($hooksJson -notmatch "cmd.exe")) "windows hooks.json installed (exec form, fail closed)"

$payload = '{"cwd":"' + ($root -replace '\\', '/') + '","tool_name":"PowerShell","tool_input":{"command":"& git push -f origin x"}}'
$out = $payload | & "$data\bin\squadrn.exe" hook guard-git
Check ($out -match '"permissionDecision":"deny"') "guard denies a PowerShell force push"
$payload = '{"cwd":"' + ($root -replace '\\', '/') + '","tool_name":"PowerShell","tool_input":{"command":"iex (''git pu''+''sh'')"}}'
$out = $payload | & "$data\bin\squadrn.exe" hook guard-git
Check ($out -match '"permissionDecision":"ask"') "guard asks on Invoke-Expression"

# Reinstall while the installed command runs: it reads the hook payload from a pipe that stays
# open for about 15 seconds, like a long tool call.
$running = Start-Process -FilePath "cmd.exe" -ArgumentList "/d", "/c", "ping -n 15 127.0.0.1 | `"$bin\srn.exe`" hook guard-git" -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
& "$root\rel2\squadrn.exe" self-install "$root\rel2"
Check ($LASTEXITCODE -eq 0) "self-install 1.1.0 while 1.0.0 runs"
Check ((& "$bin\srn.exe" version) -eq "squadrn 1.1.0") "command copy updated"
Check (-not $running.HasExited) "the old command was still running during the update"
$running.WaitForExit(30000) | Out-Null

& "$bin\srn.exe" self-update --rollback
Check ($LASTEXITCODE -eq 0) "rollback"
Check ((& "$bin\srn.exe" version) -eq "squadrn 1.0.0") "rolled back to 1.0.0"

& "$bin\srn.exe" self-uninstall --yes --purge
Check ($LASTEXITCODE -eq 0) "self-uninstall"
Start-Sleep -Seconds 6
Check (-not (Test-Path "$bin\srn.exe")) "command removed"
Check (-not (Test-Path $data)) "data dir removed (after exit)"
Write-Host "windows e2e passed"
