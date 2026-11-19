# End-to-end check of install.ps1 as users run it ("irm ... | iex" and the script block form),
# under Windows PowerShell 5.1 and PowerShell 7, by CI on windows-latest. Hermetic: the release is
# built from this checkout into a local directory that SQUADRN_INSTALL_BASE points at; nothing is
# downloaded. The fixture dir holds an ed25519 key pair (key, key.pub) and build/install.ps1 must
# be rendered with that key and $Tag:
#   scripts/render-install.sh squadrn/squadrn v1.2.0-rc.99 FIXTURE/key.pub
param(
    [Parameter(Mandatory = $true)][string]$Fixture,
    [string]$Tag = "v1.2.0-rc.99"
)
$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2
function Check([bool]$ok, [string]$what, [string]$detail = "") {
    if (-not $ok) { throw "FAILED: $what`n$detail" }
    Write-Host "ok  $what"
}

function ProfileOf([string]$out) {
    $m = [regex]::Match($out, '(?m)^PROFILE=(.*?)\s*$')
    if ($m.Success) { return $m.Groups[1].Value }
    return ""
}

$version = $Tag.TrimStart("v")
$root = Join-Path ([IO.Path]::GetTempPath()) ("squadrn-ps1-" + [guid]::NewGuid())
$base = Join-Path $root "pub"
$pub = Join-Path $base $Tag
$rel = Join-Path $root "rel"
New-Item -ItemType Directory -Force -Path $pub, "$rel\plugin\hooks", "$rel\.claude-plugin" | Out-Null

# The release: what goreleaser puts into the Windows zip, built from this checkout.
& go build -trimpath -ldflags "-X github.com/squadrn/squadrn/internal/cli.Version=$version" -o "$rel\squadrn.exe" ./cmd/squadrn
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Copy-Item -Recurse -Force plugin\.claude-plugin, plugin\agents, plugin\README.md "$rel\plugin\"
Copy-Item -Force scripts\hooks.windows.json "$rel\plugin\hooks\hooks.json"
Copy-Item -Force .claude-plugin\marketplace.json "$rel\.claude-plugin\"
$zip = Join-Path $pub "squadrn_${version}_windows_amd64.zip"
Compress-Archive -Path "$rel\*" -DestinationPath $zip
$hash = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
[IO.File]::WriteAllText((Join-Path $pub "checksums.txt"), "$hash  $(Split-Path -Leaf $zip)`n")
& "$env:ProgramFiles\Git\usr\bin\ssh-keygen.exe" -Y sign -q -f (Join-Path $Fixture "key") -n squadrn-release (Join-Path $pub "checksums.txt")
if ($LASTEXITCODE -ne 0) { throw "signing failed" }
Copy-Item -Force build\install.ps1 $pub

$env:SQUADRN_INSTALL_BASE = $base
$env:SQUADRN_NO_UPDATE_CHECK = "1"
foreach ($k in "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME") { Remove-Item "Env:$k" -ErrorAction SilentlyContinue }

# The README commands, with the script read from the release dir instead of irm.
$script = Join-Path $pub "install.ps1"
$doc = "& ([scriptblock]::Create((Get-Content -Raw '$script')))"
$iex = "Get-Content -Raw '$script' | iex"
foreach ($shell in "powershell.exe", "pwsh") {
    $env:USERPROFILE = Join-Path $root "home-$shell"
    $env:HOME = $env:USERPROFILE
    New-Item -ItemType Directory -Force -Path (Join-Path $env:USERPROFILE ".claude") | Out-Null
    $bin = Join-Path $env:USERPROFILE ".local\bin"

    # No -Version: the script installs its own release, not the latest stable one. -Command is
    # install.sh's --command. The fake home has no Documents yet: Windows PowerShell 5.1 then has no
    # profile path, which must only cost completion.
    $run = "$doc -Command squadrn -AddToProfile; 'EAP=' + `$ErrorActionPreference; 'PROFILE=' + `$PROFILE.CurrentUserAllHosts"
    $out = & $shell -NoProfile -Command $run 2>&1 | Out-String
    Write-Host $out
    Check ($out -match "verified release signature") "${shell}: signature verified" $out
    Check ((& "$bin\squadrn.exe" version) -eq "squadrn $version") "${shell}: installed $Tag without -Version"
    Check ($out -notmatch "install\.ps1:") "${shell}: the install reports no error" $out
    Check ($out -match "EAP=Continue") "${shell}: the session keeps its ErrorActionPreference" $out
    Check ($out -match "following the beta channel") "${shell}: a pre-release follows the beta channel" $out
    $cfg = Join-Path $env:USERPROFILE ".config\squadrn\config.toml"
    Check ((Test-Path $cfg) -and ((Get-Content -Raw $cfg) -match 'update_channel = "beta"')) "${shell}: update_channel = beta"
    $profilePath = ProfileOf $out
    if (-not $profilePath) {
        Check ($out -match "PowerShell has no profile path here") "${shell}: no profile path is a hint, not a failure" $out
        New-Item -ItemType Directory -Force -Path (Join-Path $env:USERPROFILE "Documents") | Out-Null
        $out = & $shell -NoProfile -Command $run 2>&1 | Out-String
        Write-Host $out
        Check ($out -notmatch "install\.ps1:") "${shell}: the reinstall reports no error" $out
        $profilePath = ProfileOf $out
        Check ([bool]$profilePath) "${shell}: a profile path once Documents exists" $out
    }
    Check ((Test-Path $profilePath) -and ((Get-Content -Raw $profilePath) -match [regex]::Escape("if (Test-Path '"))) "${shell}: guarded completion line in $profilePath" $out

    # "| iex" takes its names from the environment.
    & "$bin\squadrn.exe" self-uninstall --yes
    Start-Sleep -Seconds 6 # uninstall removes the running binary's files once it has exited
    $env:SQUADRN_COMMAND = "bx"
    $env:SQUADRN_ALIASES = "by"
    $out = & $shell -NoProfile -Command "$iex; 'still here'" 2>&1 | Out-String
    Remove-Item Env:SQUADRN_COMMAND, Env:SQUADRN_ALIASES
    Write-Host $out
    Check ($out -match "verified release signature" -and $out -match "still here") "${shell}: irm | iex form" $out
    Check ((Test-Path "$bin\bx.exe") -and (Test-Path "$bin\by.exe")) "${shell}: names from SQUADRN_COMMAND and SQUADRN_ALIASES" $out

    # A failure is reported in red and the session goes on.
    $out = & $shell -NoProfile -Command "$doc -Version v9.9.9; 'still here'" 2>&1 | Out-String
    Check ($out -match "install.ps1: .*v9\.9\.9") "${shell}: missing release reported" $out
    Check ($out -match "still here") "${shell}: the session survives a failed install" $out

    # Uninstall takes squadrn's line out of the profile, so PowerShell still starts cleanly.
    & "$bin\bx.exe" self-uninstall --yes --purge
    Check ($LASTEXITCODE -eq 0) "${shell}: self-uninstall"
    Check (-not ((Test-Path $profilePath) -and ((Get-Content -Raw $profilePath) -match "squadrn\\completion\.ps1"))) "${shell}: completion line removed from $profilePath"
    $out = & $shell -Command "'profile ok'" 2>&1 | Out-String
    Check ($out -match "profile ok" -and $out -notmatch "completion\.ps1") "${shell}: the profile loads after uninstall" $out
    Start-Sleep -Seconds 6
}
Write-Host "install.ps1 e2e passed"
