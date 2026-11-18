# End-to-end check of install.ps1 as users run it (the documented script block), under Windows
# PowerShell 5.1 and PowerShell 7, by CI on windows-latest. Hermetic: a fake gh serves a release
# built from this checkout; nothing is downloaded. The fixture dir holds an ed25519 key pair
# (key, key.pub) and build/install.ps1 must be rendered with that key and $Tag:
#   scripts/render-install.sh test/sprawl-releases v1.2.0-rc.99 FIXTURE/key.pub
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
$root = Join-Path ([IO.Path]::GetTempPath()) ("sprawl-ps1-" + [guid]::NewGuid())
$pub = Join-Path $root "pub"
$rel = Join-Path $root "rel"
$fakebin = Join-Path $root "fakebin"
New-Item -ItemType Directory -Force -Path $pub, "$rel\plugin\hooks", "$rel\.claude-plugin", $fakebin | Out-Null

# The release: what goreleaser puts into the Windows zip, built from this checkout.
& go build -trimpath -ldflags "-X sprawling-agents/internal/cli.Version=$version" -o "$rel\sprawl.exe" ./cmd/sprawl
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Copy-Item -Recurse -Force plugin\.claude-plugin, plugin\agents, plugin\bin, plugin\README.md "$rel\plugin\"
Copy-Item -Force scripts\hooks.windows.json "$rel\plugin\hooks\hooks.json"
Copy-Item -Force .claude-plugin\marketplace.json "$rel\.claude-plugin\"
$zip = Join-Path $pub "sprawl_${version}_windows_amd64.zip"
Compress-Archive -Path "$rel\*" -DestinationPath $zip
$hash = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
[IO.File]::WriteAllText((Join-Path $pub "checksums.txt"), "$hash  $(Split-Path -Leaf $zip)`n")
& "$env:ProgramFiles\Git\usr\bin\ssh-keygen.exe" -Y sign -q -f (Join-Path $Fixture "key") -n sprawl-release (Join-Path $pub "checksums.txt")
if ($LASTEXITCODE -ne 0) { throw "signing failed" }
Copy-Item -Force build\install.ps1 $pub

# A fake gh (gh.cmd; the script has another name so PowerShell does not run it in-process):
# release download (files or -O -), release view (the latest stable has no Windows
# build, like v1.1.0), attestation verify (none).
@'
$a = @($args)
if ($a.Count -ge 3 -and $a[0] -eq "release" -and $a[1] -eq "download") {
    if ($a[2] -ne $env:FAKE_GH_TAG) { [Console]::Error.WriteLine("release not found"); exit 1 }
    $pats = @(); $dir = "."; $out = $null
    for ($i = 3; $i -lt $a.Count; $i++) {
        switch -regex ($a[$i]) {
            '^(-p|--pattern)$' { $i++; $pats += $a[$i] }
            '^(-D|--dir)$' { $i++; $dir = $a[$i] }
            '^(-O|--output)$' { $i++; $out = $a[$i] }
            '^(-R|--repo)$' { $i++ }
        }
    }
    foreach ($p in $pats) {
        $f = Join-Path $env:FAKE_GH_PUB $p
        if (-not (Test-Path $f)) { [Console]::Error.WriteLine("no assets match the file pattern"); exit 1 }
        if ($out -eq "-") { [Console]::Out.Write([IO.File]::ReadAllText($f)) } else { Copy-Item $f $dir }
    }
    exit 0
}
if ($a.Count -ge 2 -and $a[0] -eq "release" -and $a[1] -eq "view") { "v1.1.0"; exit 0 }
if ($a.Count -ge 1 -and $a[0] -eq "attestation") { [Console]::Error.WriteLine("no attestations found for subject"); exit 1 }
[Console]::Error.WriteLine("unexpected gh $a"); exit 1
'@ | Set-Content -Encoding ascii (Join-Path $fakebin "fakegh.ps1")
"@pwsh -NoProfile -ExecutionPolicy Bypass -File `"%~dp0fakegh.ps1`" %*`r`n@exit /b %ERRORLEVEL%`r`n" | Set-Content -Encoding ascii -NoNewline (Join-Path $fakebin "gh.cmd")
$env:PATH = "$fakebin;$env:PATH"
$env:FAKE_GH_PUB = $pub
$env:FAKE_GH_TAG = $Tag
$env:SPRAWL_NO_UPDATE_CHECK = "1"
foreach ($k in "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME") { Remove-Item "Env:$k" -ErrorAction SilentlyContinue }

# Exactly the README command, with the tag the release was published under.
$doc = "& ([scriptblock]::Create((gh release download $Tag -R test/sprawl-releases -p install.ps1 -O - | Out-String)))"
foreach ($shell in "powershell.exe", "pwsh") {
    $env:USERPROFILE = Join-Path $root "home-$shell"
    $env:HOME = $env:USERPROFILE
    New-Item -ItemType Directory -Force -Path (Join-Path $env:USERPROFILE ".claude") | Out-Null
    $bin = Join-Path $env:USERPROFILE ".local\bin"

    # No -Version: the script installs its own release, not the latest stable one. The fake home
    # has no Documents yet: Windows PowerShell 5.1 then has no profile path, which must only cost
    # completion.
    $run = "$doc -AddToProfile; 'EAP=' + `$ErrorActionPreference; 'PROFILE=' + `$PROFILE.CurrentUserAllHosts"
    $out = & $shell -NoProfile -Command $run 2>&1 | Out-String
    Write-Host $out
    Check ($out -match "verified release signature") "${shell}: signature verified" $out
    Check ((& "$bin\sprawl.exe" version) -eq "sprawl $version") "${shell}: installed $Tag without -Version"
    Check ($out -notmatch "install\.ps1:") "${shell}: the install reports no error" $out
    Check ($out -match "EAP=Continue") "${shell}: the session keeps its ErrorActionPreference" $out
    Check ($out -match "following the beta channel") "${shell}: a pre-release follows the beta channel" $out
    $cfg = Join-Path $env:USERPROFILE ".config\sprawl\config.toml"
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

    # A failure is reported in red and the session goes on.
    $out = & $shell -NoProfile -Command "$doc -Version v9.9.9; 'still here'" 2>&1 | Out-String
    Check ($out -match "install.ps1: .*v9\.9\.9") "${shell}: missing release reported" $out
    Check ($out -match "still here") "${shell}: the session survives a failed install" $out

    # Uninstall takes sprawl's line out of the profile, so PowerShell still starts cleanly.
    & "$bin\sprawl.exe" self-uninstall --yes --purge
    Check ($LASTEXITCODE -eq 0) "${shell}: self-uninstall"
    Check (-not ((Test-Path $profilePath) -and ((Get-Content -Raw $profilePath) -match "sprawl\\completion\.ps1"))) "${shell}: completion line removed from $profilePath"
    $out = & $shell -Command "'profile ok'" 2>&1 | Out-String
    Check ($out -match "profile ok" -and $out -notmatch "completion\.ps1") "${shell}: the profile loads after uninstall" $out
    Start-Sleep -Seconds 6
}
Write-Host "install.ps1 e2e passed"
