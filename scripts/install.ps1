# Installs sprawl on Windows from its releases repository (binaries only). Requires gh (logged in
# with read access to that repository) and Git for Windows 2.31 or newer.
#
#   & ([scriptblock]::Create((gh release download TAG -R __SPRAWL_REPO__ -p install.ps1 -O - | Out-String)))
#   & ([scriptblock]::Create((gh release download TAG -R __SPRAWL_REPO__ -p install.ps1 -O - | Out-String))) -Name ai -Alias sx
#
# It installs the release it was downloaded from (TAG). Parameters mirror install.sh: -Name
# (command name), -Alias (comma separated; '' for none), -Version vX.Y.Z (another release),
# -Force (accept a name another program uses), -Repo OWNER/NAME, and -AddToProfile (load tab
# completion from your PowerShell profile). Without -Name and -Alias, sprawl asks for the names.
# Errors are reported without closing the PowerShell session that ran the script.
# Keep this file ASCII: Windows PowerShell 5.1 misreads UTF-8 without a BOM.
param(
    [string]$Name = "",
    [string]$Alias = $null,
    [string]$Version = "",
    [switch]$Force,
    [switch]$AddToProfile,
    [string]$Repo = "__SPRAWL_REPO__"
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
        $answer = Read-Host "Load sprawl tab completion from your PowerShell profile? [Y/n]"
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
        Write-Host "sprawl is installed; for tab completion add this line to your profile yourself:  $Line"
        return
    }
    Write-Host "tab completion added to $profilePath"
    if ((Get-ExecutionPolicy) -eq "Restricted") {
        Write-Host "note: profiles do not load under the Restricted policy; run: Set-ExecutionPolicy -Scope CurrentUser RemoteSigned" -ForegroundColor Yellow
    }
}

function Install-Sprawl {
    param([string]$Name, [AllowNull()][string]$Alias, [bool]$AliasGiven, [string]$Version, [bool]$Force, [bool]$AddToProfile, [string]$Repo)
    # Both are local to this function: nothing leaks into the session that ran the script.
    $ErrorActionPreference = "Stop"
    Set-StrictMode -Version 2

    # Public keys allowed to sign releases (ssh-ed25519 lines); empty in unreleased builds.
    $signers = @'
__SPRAWL_SIGNERS__
'@
    # The release this script was published with; a placeholder in a source checkout.
    $releaseTag = "__SPRAWL_VERSION__"

    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { throw "gh is required: https://cli.github.com (then: gh auth login)" }
    $git = Get-Command git -ErrorAction SilentlyContinue
    if (-not $git) { throw "Git for Windows is required: winget install --id Git.Git -e" }
    $gitVersion = (Invoke-Native git @("version") | Out-String).Trim() -replace '^git version ', ''
    if ($gitVersion -notmatch '^(\d+)\.(\d+)') { throw "cannot read the git version ($gitVersion)" }
    if ([int]$Matches[1] -lt 2 -or ([int]$Matches[1] -eq 2 -and [int]$Matches[2] -lt 31)) {
        throw "git $gitVersion is too old; sprawl needs git 2.31 or newer (upgrade: winget install --id Git.Git -e)"
    }
    switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { $arch = "amd64" }
        "ARM64" { $arch = "arm64" }
        default { throw "unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" }
    }

    if (-not $Version -and $releaseTag -match '^v\d') { $Version = $releaseTag }
    if (-not $Version) {
        $Version = (Invoke-Native gh @("release", "view", "--repo", $Repo, "--json", "tagName", "--jq", ".tagName") | Out-String).Trim()
        if ($LASTEXITCODE -ne 0 -or -not $Version) { throw "cannot read releases of $Repo; check gh auth status and your access" }
    }
    $asset = "sprawl_$($Version.TrimStart('v'))_windows_$arch.zip"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("sprawl-install-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $keys = @($signers -split "`n" | ForEach-Object { $_.Trim() } | Where-Object { $_ -like "ssh-ed25519 *" })
        if ($keys.Count -gt 0) {
            # The signature first and on its own, so a release without one is refused clearly.
            $out = Invoke-Native gh @("release", "download", $Version, "--repo", $Repo, "--pattern", "checksums.txt.sig", "--dir", $tmp) -Merge | Out-String
            if ($LASTEXITCODE -ne 0) {
                if ($out -match "no assets match") { throw "release $Version has no signature (checksums.txt.sig); refusing to install" }
                throw "cannot download release $Version from ${Repo}: $($out.Trim())"
            }
        }
        $out = Invoke-Native gh @("release", "download", $Version, "--repo", $Repo, "--pattern", $asset, "--pattern", "checksums.txt", "--dir", $tmp) -Merge | Out-String
        if ($LASTEXITCODE -ne 0) {
            if ($out -match "no assets match") { throw "release $Version has no $asset (no Windows build in that release?)" }
            throw "cannot download $asset from ${Repo}: $($out.Trim())"
        }

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
            [IO.File]::WriteAllLines($allowed, [string[]]@($keys | ForEach-Object { "sprawl $_" }))
            # Feed the exact bytes of checksums.txt on stdin.
            $p = Start-Process -FilePath $sshKeygen -ArgumentList @("-Y", "verify", "-f", "`"$allowed`"", "-I", "sprawl", "-n", "sprawl-release", "-s", "`"$sums.sig`"") `
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

        # Build provenance, where GitHub provides it (public repositories, Enterprise Cloud). Signed
        # releases do not depend on it.
        $out = Invoke-Native gh @("attestation", "verify", (Join-Path $tmp $asset), "--owner", ($Repo -split "/")[0]) -Merge | Out-String
        if ($LASTEXITCODE -eq 0) {
            Write-Host "verified build provenance of $asset"
        } elseif ($out -match "no attestations found|unknown command|HTTP 404|HTTP 403") {
            if ($keys.Count -eq 0) { Write-Host "note: $asset has no build attestation" -ForegroundColor Yellow }
        } else {
            throw "build provenance check failed for ${asset}: $($out.Trim())"
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
        # sprawl asks for the names itself (on the console) unless they were given.
        if (-not $Name -and -not $AliasGiven -and -not $env:CI -and [Environment]::UserInteractive) { $installArgs += "--interactive" }
        Invoke-Native (Join-Path $release "sprawl.exe") $installArgs
        if ($LASTEXITCODE -ne 0) { throw "sprawl self-install failed" }

        # sprawl's data dir, as sprawl resolves it: an absolute XDG_DATA_HOME wins.
        $dataHome = Join-Path $env:USERPROFILE ".local\share"
        if ($env:XDG_DATA_HOME -and $env:XDG_DATA_HOME -match '^([A-Za-z]:[\\/]|\\\\)') { $dataHome = $env:XDG_DATA_HOME }
        $completion = Join-Path $dataHome "sprawl\completion.ps1"
        $quoted = "'" + ($completion -replace "'", "''") + "'"
        $line = "if (Test-Path $quoted) { . $quoted }"
        # sprawl is installed by now: the profile step only ever costs completion, never the install.
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
    Install-Sprawl -Name $Name -Alias $Alias -AliasGiven ($PSBoundParameters -and $PSBoundParameters.ContainsKey("Alias")) -Version $Version `
        -Force $Force.IsPresent -AddToProfile $AddToProfile.IsPresent -Repo $Repo
} catch {
    Write-Host "install.ps1: $($_.Exception.Message)" -ForegroundColor Red
    # Run as a file, the caller sees the failure in the exit code; run as a script block (the
    # documented way) or through Invoke-Expression, exit would close the user's PowerShell.
    if ($PSCommandPath) { exit 1 }
}
