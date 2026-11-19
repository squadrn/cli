# Checks the facts ADR 0008 could not verify from documentation, on a real Windows machine with
# Claude Code installed and logged in. Run it from the repository root in PowerShell:
#
#   powershell -ExecutionPolicy Bypass -File scripts\windows-spike.ps1
#
# It creates a scratch project under %TEMP% with project-level hooks that only record what Claude
# Code sends them (nothing is blocked), asks you to run two short prompts there, then writes
# spike-results.json next to this script. Nothing outside %TEMP% is changed.
$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2

$results = [ordered]@{
    windows   = [Environment]::OSVersion.VersionString
    claude    = (& claude --version 2>$null | Out-String).Trim()
    git       = (& git version 2>$null | Out-String).Trim()
    ssh       = (& ssh -V 2>&1 | Out-String).Trim()
    timestamp = (Get-Date).ToString("o")
}

# S4/S11: signature tools.
$gitRoot = Split-Path (Split-Path (Get-Command git).Source)
$results["ssh_keygen_git_for_windows"] = Test-Path (Join-Path $gitRoot "usr\bin\ssh-keygen.exe")
$results["ssh_keygen_builtin"] = Test-Path (Join-Path $env:SystemRoot "System32\OpenSSH\ssh-keygen.exe")

# S6/S12: symlink as this user, junction.
$scratch = Join-Path $env:TEMP ("squadrn spike " + [guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Force -Path "$scratch\target" | Out-Null
try {
    New-Item -ItemType SymbolicLink -Path "$scratch\symlink" -Target "$scratch\target" -ErrorAction Stop | Out-Null
    $results["symlink_without_admin"] = $true
} catch {
    $results["symlink_without_admin"] = $false
    $results["symlink_error"] = $_.Exception.Message
}
cmd.exe /d /c "mklink /J `"$scratch\junction`" `"$scratch\target`"" | Out-Null
$results["junction"] = Test-Path "$scratch\junction"

# S1/S2/S13: a project whose hooks record their input. Exec form through cmd.exe, as squadrn's
# Windows hooks.json does; `more` copies stdin to a file.
$project = Join-Path $scratch "a b\project"
$log = Join-Path $scratch "log"
New-Item -ItemType Directory -Force -Path "$project\.claude", $log | Out-Null
$hook = @{
    hooks = @{
        PreToolUse = @(@{
                matcher = "Bash|PowerShell"
                hooks   = @(@{ type = "command"; command = "cmd.exe"; args = @("/d", "/c", "more > `"$log\pretool-%RANDOM%%RANDOM%.json`""); timeout = 5 })
            })
    }
}
$hook | ConvertTo-Json -Depth 8 | Set-Content -Encoding ASCII "$project\.claude\settings.json"
& git -C $project init -q

Write-Host ""
Write-Host "1. Open a new terminal, then run:" -ForegroundColor Cyan
Write-Host "     cd `"$project`""
Write-Host "     claude"
Write-Host "   Accept the folder trust prompt, then send these two prompts:"
Write-Host "     Run this with the Bash tool: echo spike-bash"
Write-Host "     Run this with the PowerShell tool: Set-Alias zz git; Get-Date"
Write-Host "     Run this with the PowerShell tool: Get-Alias zz"
Write-Host "   Then quit claude (/exit)."
Write-Host "2. Come back here and press Enter." -ForegroundColor Cyan
[void](Read-Host)

$payloads = @(Get-ChildItem $log -Filter "pretool-*.json" | ForEach-Object { Get-Content $_.FullName -Raw | ConvertFrom-Json })
$results["S1_exec_form_hook_ran"] = $payloads.Count -gt 0
$results["S2_tool_names"] = @($payloads | ForEach-Object { $_.tool_name } | Sort-Object -Unique)
$results["S2_powershell_payload"] = @($payloads | Where-Object { $_.tool_name -eq "PowerShell" } | Select-Object -First 1 | ForEach-Object { $_.tool_input })
$results["S13_commands"] = @($payloads | ForEach-Object { $_.tool_input.command })

# S3: how Claude Code names the project folder for "$project".
$projects = Join-Path $env:USERPROFILE ".claude\projects"
$results["S3_project_path"] = $project
$results["S3_project_folders"] = @(Get-ChildItem $projects -Directory -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 3 | ForEach-Object { $_.Name })

$out = Join-Path $PSScriptRoot "spike-results.json"
$results | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 $out
Write-Host ""
Write-Host "Written: $out" -ForegroundColor Green
Write-Host "S13: if the third prompt printed the alias zz, PowerShell tool sessions keep state between calls."
Write-Host "Send spike-results.json (and the answer to S13) back; it holds no credentials."
Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
