# Installs squadrn on Windows from its GitHub releases. Needs Git for Windows 2.31 or newer; no
# GitHub account or gh.
#
#   irm https://github.com/squadrn/squadrn/releases/latest/download/install.ps1 | iex
#   & ([scriptblock]::Create((irm https://github.com/squadrn/squadrn/releases/latest/download/install.ps1))) -Name ai -Alias sx
#
# It installs the release it was downloaded from (latest/download is the newest stable release).
# Parameters, for the script block form, mirror install.sh: -Name (command name), -Alias (comma
# separated; '' for none), -Version vX.Y.Z (another release), -Force (accept a name another
# program uses), -Repo OWNER/NAME, and -AddToProfile (load tab completion from your PowerShell
# profile). The "| iex" form takes no parameters; set SQUADRN_COMMAND, SQUADRN_ALIASES or
# SQUADRN_VERSION instead. Without names, squadrn asks for them. SQUADRN_INSTALL_BASE replaces
# https://github.com/OWNER/NAME/releases/download (a mirror, or a local directory holding
# <tag>\<files> for tests); signatures are verified all the same.
# Errors are reported without closing the PowerShell session that ran the script.
# Keep this file ASCII: Windows PowerShell 5.1 misreads UTF-8 without a BOM.
param(
    [string]$Name = $env:SQUADRN_COMMAND,
    [string]$Alias = $null,
    [string]$Version = $env:SQUADRN_VERSION,
    [switch]$Force,
    [switch]$AddToProfile,
    [string]$Repo = "__SQUADRN_REPO__"
)

function Invoke-Native {
    # Runs a program with $ErrorActionPreference relaxed: Windows PowerShell 5.1 turns redirected
    # stderr into errors, which 'Stop' would make fatal. Callers check $LASTEXITCODE.
    param([string]$FilePath, [string[]]$ArgumentList, [switch]$Quiet, [switch]$Merge)
    $ErrorActionPreference = "Continue"
    if ($Quiet) { & $FilePath @ArgumentList 2>$null }
    elseif ($Merge) { & $FilePath @ArgumentList 2>&1 | ForEach-Object { "$_" } }
    else { & $FilePath @ArgumentList }
}

function Add-Completion {
    param([string]$Completion, [string]$Line, [bool]$AddToProfile)
    if (-not (Test-Path $Completion)) { return }
    # Windows PowerShell 5.1 leaves the profile path empty when Documents does not exist.
    $profilePath = ""
    if ($null -ne $PROFILE -and $PROFILE.PSObject.Properties["CurrentUserAllHosts"]) { $profilePath = [string]$PROFILE.CurrentUserAllHosts }
    if (-not $profilePath) {
        Write-Host "tab completion: PowerShell has no profile path here; add this line to your profile yourself:  $Line"
        return
    }
    if ((Test-Path $profilePath) -and ((Get-Content $profilePath -Raw) -like "*$Completion*")) { return }
    if (-not $AddToProfile -and -not $env:CI -and [Environment]::UserInteractive -and $Host.UI.RawUI) {
        $answer = Read-Host "Load squadrn tab completion from your PowerShell profile? [Y/n]"
        $AddToProfile = ($answer -eq "" -or $answer -match "^[Yy]")
    }
    if (-not $AddToProfile) {
        Write-Host "tab completion: add this line to $profilePath :  $Line"
        return
    }
    # Documents can be redirected (OneDrive) or guarded (Controlled Folder Access).
    try {
        New-Item -ItemType Directory -Force -Path (Split-Path $profilePath) | Out-Null
        Add-Content -Path $profilePath -Value $Line
    } catch {
        Write-Host "warning: cannot write $profilePath ($($_.Exception.Message))" -ForegroundColor Yellow
        Write-Host "squadrn is installed; for tab completion add this line to your profile yourself:  $Line"
        return
    }
    Write-Host "tab completion added to $profilePath"
    if ((Get-ExecutionPolicy) -eq "Restricted") {
        Write-Host "note: profiles do not load under the Restricted policy; run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned" -ForegroundColor Yellow
    }
}

function Install-Squadrn {
    param([string]$Name, [AllowNull()][string]$Alias, [bool]$AliasGiven, [string]$Version, [bool]$Force, [bool]$AddToProfile, [string]$Repo)
    # Both are local to this function: nothing leaks into the session that ran the script.
    $ErrorActionPreference = "Stop"
    # The progress bar slows Invoke-WebRequest down considerably in Windows PowerShell 5.1.
    $ProgressPreference = "SilentlyContinue"
    Set-StrictMode -Version 2

    # Public keys allowed to sign releases (ssh-ed25519 lines); empty in unreleased builds.
    $signers = @'
__SQUADRN_SIGNERS__
'@
    # The release this script was published with; a placeholder in a source checkout.
    $releaseTag = "__SQUADRN_VERSION__"

    if ($Repo -notmatch '/') { $Repo = "squadrn/squadrn" }
    # Windows PowerShell 5.1 may default to TLS 1.0, which GitHub refuses.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $git = Get-Command git -ErrorAction SilentlyContinue
    if (-not $git) { throw "Git for Windows is required: winget install --id Git.Git -e" }
    $gitVersion = (Invoke-Native git @("version") | Out-String).Trim() -replace '^git version ', ''
    if ($gitVersion -notmatch '^(\d+)\.(\d+)') { throw "cannot read the git version ($gitVersion)" }
    if ([int]$Matches[1] -lt 2 -or ([int]$Matches[1] -eq 2 -and [int]$Matches[2] -lt 31)) {
        throw "git $gitVersion is too old; squadrn needs git 2.31 or newer (upgrade: winget install --id Git.Git -e)"
    }
    switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { $arch = "amd64" }
        "ARM64" { $arch = "arm64" }
        default { throw "unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" }
    }

    if (-not $Version -and $releaseTag -match '^v\d') { $Version = $releaseTag }
    if (-not $Version) {
        try { $Version = [string](Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$Repo/releases/latest").tag_name }
        catch { throw "cannot look up the latest release of $Repo (network, or GitHub API rate limit): $($_.Exception.Message)" }
        if (-not $Version) { throw "cannot read the latest release of $Repo" }
    }
    $base = "https://github.com/$Repo/releases/download"
    if ($env:SQUADRN_INSTALL_BASE) { $base = $env:SQUADRN_INSTALL_BASE.TrimEnd("/", "\") }
    $asset = "squadrn_$($Version.TrimStart('v'))_windows_$arch.zip"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("squadrn-install-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        # Get-ReleaseFile: one file of the release into $tmp; a local base directory is copied from.
        function Get-ReleaseFile([string]$File) {
            $dst = Join-Path $tmp $File
            if (Test-Path -LiteralPath $base -PathType Container) {
                $src = Join-Path (Join-Path $base $Version) $File
                if (-not (Test-Path -LiteralPath $src)) { return $false }
                Copy-Item -LiteralPath $src $dst
                return $true
            }
            try {
                Invoke-WebRequest -UseBasicParsing -Uri "$base/$Version/$File" -OutFile $dst
                return $true
            } catch { return $false }
        }
        $keys = @($signers -split "`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ -like "ssh-ed25519 *" })
        if ($keys.Count -gt 0) {
            # The signature first and on its own, so a release without one is refused clearly.
            if (-not (Get-ReleaseFile "checksums.txt.sig")) {
                throw "cannot download the signature of $Version (checksums.txt.sig): no such release, no signature, or no network; refusing to install"
            }
        }
        if (-not (Get-ReleaseFile "checksums.txt")) { throw "cannot download checksums.txt of $Version (no such release, or no network)" }
        if (-not (Get-ReleaseFile $asset)) { throw "cannot download $asset (no Windows build in $Version?)" }

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
                    $v = Invoke-Native (Join-Path (Split-Path $c) "ssh.exe") @("-V") -Merge | Out-String
                    if ($v -notmatch 'OpenSSH_for_Windows_(\d+)\.(\d+)' -or [int]$Matches[1] -lt 8 -or ([int]$Matches[1] -eq 8 -and [int]$Matches[2] -lt 1)) { continue }
                }
                $sshKeygen = $c
                break
            }
            if (-not $sshKeygen) { throw "no ssh-keygen that can verify signatures (install Git for Windows); refusing to install" }
            $allowed = Join-Path $tmp "allowed_signers"
            [IO.File]::WriteAllLines($allowed, [string[]]@($keys | ForEach-Object { "squadrn $_" }))
            # Feed the exact bytes of checksums.txt on stdin.
            $p = Start-Process -FilePath $sshKeygen -ArgumentList @("-Y", "verify", "-f", "`"$allowed`"", "-I", "squadrn", "-n", "squadrn-release", "-s", "`"$sums.sig`"") `
                -RedirectStandardInput $sums -RedirectStandardOutput (Join-Path $tmp "verify.out") -RedirectStandardError (Join-Path $tmp "verify.err") `
                -NoNewWindow -Wait -PassThru
            if ($p.ExitCode -ne 0) { throw "the release signature is not valid; refusing to install" }
            Write-Host "verified release signature"
        } else {
            Write-Host "note: this installer has no signing key; verifying by checksum only" -ForegroundColor Yellow
        }

        $expected = (Get-Content $sums | Where-Object { $_ -match "^([0-9a-f]{64})\s+\*?$([regex]::Escape($asset))$" } | ForEach-Object { $Matches[1] }) | Select-Object -First 1
        if (-not $expected) { throw "checksums.txt has no entry for $asset" }
        $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLower()
        if ($actual -ne $expected) { throw "checksum mismatch for $asset" }

        # Build provenance as an extra when gh is installed and logged in; the signature is what
        # the install relies on.
        if (Get-Command gh -ErrorAction SilentlyContinue) {
            $null = Invoke-Native gh @("auth", "status") -Quiet
            if ($LASTEXITCODE -eq 0) {
                $out = Invoke-Native gh @("attestation", "verify", (Join-Path $tmp $asset), "--repo", $Repo) -Merge | Out-String
                if ($LASTEXITCODE -eq 0) {
                    Write-Host "verified build provenance of $asset"
                } elseif ($out -notmatch "no attestations found|unknown command|HTTP 404") {
                    throw "build provenance check failed for ${asset}: $($out.Trim())"
                }
            }
        }

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
        if ($AliasGiven) { $installArgs += "--alias=$Alias" }
        if ($Force) { $installArgs += "--force" }
        # squadrn asks for the names itself (on the console) unless they were given.
        if (-not $Name -and -not $AliasGiven -and -not $env:CI -and [Environment]::UserInteractive) { $installArgs += "--interactive" }
        Invoke-Native (Join-Path $release "squadrn.exe") $installArgs
        if ($LASTEXITCODE -ne 0) { throw "squadrn self-install failed" }

        # squadrn's data dir, as squadrn resolves it: an absolute XDG_DATA_HOME wins.
        $dataHome = Join-Path $env:USERPROFILE ".local\share"
        if ($env:XDG_DATA_HOME -and $env:XDG_DATA_HOME -match '^([A-Za-z]:[\\/]|\\\\)') { $dataHome = $env:XDG_DATA_HOME }
        $completion = Join-Path $dataHome "squadrn\completion.ps1"
        $quoted = "'" + ($completion -replace "'", "''") + "'"
        $line = "if (Test-Path $quoted) { . $quoted }"
        # squadrn is installed by now: the profile step only ever costs completion, never the install.
        try { Add-Completion -Completion $completion -Line $line -AddToProfile $AddToProfile }
        catch {
            Write-Host "warning: tab completion not set up ($($_.Exception.Message))" -ForegroundColor Yellow
            Write-Host "for tab completion add this line to your PowerShell profile yourself:  $line"
        }
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
}

try {
    $aliasGiven = ($PSBoundParameters -and $PSBoundParameters.ContainsKey("Alias"))
    if (-not $aliasGiven -and (Test-Path Env:SQUADRN_ALIASES)) { $Alias = $env:SQUADRN_ALIASES; $aliasGiven = $true }
    Install-Squadrn -Name $Name -Alias $Alias -AliasGiven $aliasGiven -Version $Version `
        -Force $Force.IsPresent -AddToProfile $AddToProfile.IsPresent -Repo $Repo
} catch {
    Write-Host "install.ps1: $($_.Exception.Message)" -ForegroundColor Red
    # Run as a file, the caller sees the failure in the exit code; run as a script block (the
    # documented way) or through Invoke-Expression, exit would close the user's PowerShell.
    if ($PSCommandPath) { exit 1 }
}
