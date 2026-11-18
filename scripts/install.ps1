# Installs sprawl on Windows from its releases repository (binaries only). Requires gh (logged in
# with read access to that repository) and Git for Windows 2.31 or newer.
#
#   gh release download -R __SPRAWL_REPO__ -p install.ps1 -O - | Out-String | Invoke-Expression
#   & ([scriptblock]::Create((gh release download -R __SPRAWL_REPO__ -p install.ps1 -O - | Out-String))) -Name ai -Alias sx
#
# Parameters mirror install.sh: -Name (command name), -Alias (comma separated; '' for none),
# -Version vX.Y.Z, -Force (accept a name another program uses), -Repo OWNER/NAME, and
# -AddToProfile (load tab completion from your PowerShell profile).
# Keep this file ASCII: Windows PowerShell 5.1 misreads UTF-8 without a BOM.
param(
    [string]$Name = "",
    [string]$Alias = $null,
    [string]$Version = "",
    [switch]$Force,
    [switch]$AddToProfile,
    [string]$Repo = "__SPRAWL_REPO__"
)
$ErrorActionPreference = "Stop"
Set-StrictMode -Version 2

function Fail([string]$Message) {
    Write-Host "install.ps1: $Message" -ForegroundColor Red
    exit 1
}

# Public keys allowed to sign releases (ssh-ed25519 lines); empty in unreleased builds.
$signers = @'
__SPRAWL_SIGNERS__
'@

if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { Fail "gh is required: https://cli.github.com (then: gh auth login)" }
$git = Get-Command git -ErrorAction SilentlyContinue
if (-not $git) { Fail "Git for Windows is required: winget install --id Git.Git -e" }
$gitVersion = (& git version) -replace '^git version ', ''
if ($gitVersion -notmatch '^(\d+)\.(\d+)') { Fail "cannot read the git version ($gitVersion)" }
if ([int]$Matches[1] -lt 2 -or ([int]$Matches[1] -eq 2 -and [int]$Matches[2] -lt 31)) {
    Fail "git $gitVersion is too old; sprawl needs git 2.31 or newer (upgrade: winget install --id Git.Git -e)"
}
switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $arch = "amd64" }
    "ARM64" { $arch = "arm64" }
    default { Fail "unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" }
}

if (-not $Version) {
    $Version = (& gh release view --repo $Repo --json tagName --jq .tagName)
    if ($LASTEXITCODE -ne 0 -or -not $Version) { Fail "cannot read releases of $Repo; check gh auth status and your access" }
}
$asset = "sprawl_$($Version.TrimStart('v'))_windows_$arch.zip"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("sprawl-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $keys = @($signers -split "`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ -like "ssh-ed25519 *" })
    if ($keys.Count -gt 0) {
        # The signature first and on its own, so a release without one is refused clearly.
        & gh release download $Version --repo $Repo --pattern checksums.txt.sig --dir $tmp 2>$null
        if ($LASTEXITCODE -ne 0) { Fail "release $Version has no signature (checksums.txt.sig); refusing to install" }
    }
    & gh release download $Version --repo $Repo --pattern $asset --pattern checksums.txt --dir $tmp
    if ($LASTEXITCODE -ne 0) { Fail "cannot download $asset from $Repo" }

    $sums = Join-Path $tmp "checksums.txt"
    if ($keys.Count -gt 0) {
        # ssh-keygen from Git for Windows first; the OpenSSH built into Windows only from 8.1,
        # the first version with -Y verify. Absolute paths only.
        $gitRoot = Split-Path (Split-Path $git.Source)
        $candidates = @((Join-Path $gitRoot "usr\bin\ssh-keygen.exe"), (Join-Path $env:SystemRoot "System32\OpenSSH\ssh-keygen.exe"))
        $sshKeygen = $null
        foreach ($c in $candidates) {
            if (-not (Test-Path $c)) { continue }
            if ($c -like "*System32*") {
                $v = (& (Join-Path (Split-Path $c) "ssh.exe") -V 2>&1 | Out-String)
                if ($v -notmatch 'OpenSSH_for_Windows_(\d+)\.(\d+)' -or [int]$Matches[1] -lt 8 -or ([int]$Matches[1] -eq 8 -and [int]$Matches[2] -lt 1)) { continue }
            }
            $sshKeygen = $c
            break
        }
        if (-not $sshKeygen) { Fail "no ssh-keygen that can verify signatures (install Git for Windows); refusing to install" }
        $allowed = Join-Path $tmp "allowed_signers"
        [IO.File]::WriteAllLines($allowed, @($keys | ForEach-Object { "sprawl $_" }))
        # Feed the exact bytes of checksums.txt on stdin.
        $p = Start-Process -FilePath $sshKeygen -ArgumentList @("-Y", "verify", "-f", "`"$allowed`"", "-I", "sprawl", "-n", "sprawl-release", "-s", "`"$sums.sig`"") `
            -RedirectStandardInput $sums -RedirectStandardOutput (Join-Path $tmp "verify.out") -RedirectStandardError (Join-Path $tmp "verify.err") `
            -NoNewWindow -Wait -PassThru
        if ($p.ExitCode -ne 0) { Fail "the release signature is not valid; refusing to install" }
        Write-Host "verified release signature"
    } else {
        Write-Host "note: this installer has no signing key; verifying by checksum only" -ForegroundColor Yellow
    }

    $expected = (Get-Content $sums | Where-Object { $_ -match "^([0-9a-f]{64})\s+\*?$([regex]::Escape($asset))$" } | ForEach-Object { $Matches[1] }) | Select-Object -First 1
    if (-not $expected) { Fail "checksums.txt has no entry for $asset" }
    $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLower()
    if ($actual -ne $expected) { Fail "checksum mismatch for $asset" }

    $release = Join-Path $tmp "release"
    Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $release
    # %USERPROFILE%\.local\bin on the user PATH (Claude Code's installer usually added it), before
    # self-install so it does not tell the user to do it.
    $bin = Join-Path $env:USERPROFILE ".local\bin"
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
    $path = [string]$key.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $key.Close()
    if (-not (($path -split ";") -contains $bin)) {
        [Environment]::SetEnvironmentVariable("Path", (($path.TrimEnd(";"), $bin) -join ";").TrimStart(";"), "User")
        Write-Host "added $bin to your PATH; open a new terminal"
    }
    # This session too, so self-install (and the user, right after) finds the command.
    if (-not (($env:Path -split ";") -contains $bin)) { $env:Path = "$env:Path;$bin" }
    $installArgs = @("self-install", $release)
    if ($Name) { $installArgs += @("--command", $Name) }
    if ($PSBoundParameters.ContainsKey("Alias")) { $installArgs += "--alias=$Alias" }
    if ($Force) { $installArgs += "--force" }
    & (Join-Path $release "sprawl.exe") @installArgs
    if ($LASTEXITCODE -ne 0) { Fail "sprawl self-install failed" }
    # Pre-release installs follow the beta channel, or they would never be offered the next rc.
    if ($Version -match '-') { & (Join-Path $release "sprawl.exe") config set update_channel beta | Out-Null }

    $completion = Join-Path $env:USERPROFILE ".local\share\sprawl\completion.ps1"
    $line = ". `"$completion`""
    $profilePath = $PROFILE.CurrentUserAllHosts
    $loaded = (Test-Path $profilePath) -and ((Get-Content $profilePath -Raw) -like "*$completion*")
    if (-not $loaded -and (Test-Path $completion)) {
        if (-not $AddToProfile -and [Environment]::UserInteractive -and $Host.UI.RawUI) {
            $answer = Read-Host "Load sprawl tab completion from your PowerShell profile? [Y/n]"
            $AddToProfile = ($answer -eq "" -or $answer -match "^[Yy]")
        }
        if ($AddToProfile) {
            # Documents can be redirected (OneDrive) or guarded (Controlled Folder Access); sprawl
            # is installed by now, so a profile that cannot be written only costs completion.
            try {
                New-Item -ItemType Directory -Force -Path (Split-Path $profilePath) | Out-Null
                Add-Content -Path $profilePath -Value $line
                Write-Host "tab completion added to $profilePath"
                if ((Get-ExecutionPolicy) -eq "Restricted") {
                    Write-Host "note: profiles do not load under the Restricted policy; run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned" -ForegroundColor Yellow
                }
            } catch {
                Write-Host "warning: cannot write $profilePath ($($_.Exception.Message))" -ForegroundColor Yellow
                Write-Host "sprawl is installed; for tab completion add this line to your profile yourself:  $line"
            }
        } else {
            Write-Host "tab completion: add this line to $profilePath :  $line"
        }
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
