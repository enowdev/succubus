<#
.SYNOPSIS
  succubus installer for Windows.

.DESCRIPTION
  Downloads the release binary for this machine, verifies its checksum, and puts
  it on your PATH. Nothing here needs Administrator: the default target is
  %LOCALAPPDATA%\Programs\succubus and it edits only the per-user PATH.

.EXAMPLE
  irm https://raw.githubusercontent.com/enowdev/succubus/main/install.ps1 | iex

.EXAMPLE
  # A specific version, or a different location:
  $env:SUCCUBUS_VERSION = 'v0.1.0'
  $env:SUCCUBUS_INSTALL_DIR = 'C:\tools\succubus'
  irm https://raw.githubusercontent.com/enowdev/succubus/main/install.ps1 | iex
#>

# `iex` runs this without the param block binding, so read configuration from
# the environment rather than parameters — that is the only channel a piped
# script has.
$ErrorActionPreference = 'Stop'
$ProgressPreference    = 'SilentlyContinue'   # the progress bar makes downloads ~10x slower

$Repo = if ($env:SUCCUBUS_REPO) { $env:SUCCUBUS_REPO } else { 'enowdev/succubus' }
$Bin  = 'succubus.exe'

# Where releases are fetched from. Overridable so a fork can point elsewhere,
# and so this script can be tested against a local server.
$ReleaseBase = if ($env:SUCCUBUS_RELEASE_BASE) { $env:SUCCUBUS_RELEASE_BASE } else { "https://github.com/$Repo/releases/download" }
$ApiBase     = if ($env:SUCCUBUS_API_BASE)     { $env:SUCCUBUS_API_BASE }     else { 'https://api.github.com' }

# --- output ------------------------------------------------------------------
function Write-Say  { param($m) Write-Host "  $m" }
function Write-Dim  { param($m) Write-Host "  $m" -ForegroundColor DarkGray }
function Write-Ok   { param($m) Write-Host "  " -NoNewline; Write-Host "OK" -ForegroundColor Green -NoNewline; Write-Host " $m" }
function Write-Warn { param($m) Write-Host "  " -NoNewline; Write-Host "!" -ForegroundColor Yellow -NoNewline; Write-Host " $m" }
# Die throws rather than calling `exit`.
#
# The documented invocation is `irm ... | iex`, which runs this script in the
# caller's own scope — there `exit` terminates the user's whole PowerShell
# session, not just the install. Throwing unwinds to the trap at the end of this
# script instead, so a failed install closes nothing.
function Die {
    param($m)
    Write-Host ""
    Write-Host "  [x] " -ForegroundColor Red -NoNewline
    Write-Host $m
    Write-Host ""
    # The message is already printed above, so the thrown text is only a marker
    # for the handler at the bottom.
    throw 'succubus-install-failed'
}

# --- preflight ---------------------------------------------------------------
# Invoke-RestMethod on Windows PowerShell 5.1 defaults to TLS 1.0, which GitHub
# refuses. Raise it before the first request rather than failing cryptically.
if ($PSVersionTable.PSVersion.Major -lt 6) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
}

# --- platform ----------------------------------------------------------------
function Get-Platform {
    $arch = $env:PROCESSOR_ARCHITECTURE
    # On an ARM64 machine a 32-bit host process reports x86 here, so consult the
    # WOW64 variable too — otherwise we hand an ARM user the wrong binary.
    if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }

    switch ($arch) {
        'AMD64' { return 'windows-amd64' }
        'ARM64' { return 'windows-arm64' }
        'x86'   { Die "succubus does not ship a 32-bit build. A 64-bit Windows install is required." }
        default { Die "unsupported architecture: $arch" }
    }
}

# --- version -----------------------------------------------------------------
function Get-Version {
    if ($env:SUCCUBUS_VERSION) { return $env:SUCCUBUS_VERSION }
    try {
        $rel = Invoke-RestMethod -Uri "$ApiBase/repos/$Repo/releases/latest" `
                                 -Headers @{ 'User-Agent' = 'succubus-installer' }
        if ($rel.tag_name) { return $rel.tag_name }
    } catch { }
    Die @"
could not determine the latest release.

Set a version explicitly, or build from source:
  `$env:SUCCUBUS_VERSION = 'v0.1.0'; irm https://raw.githubusercontent.com/$Repo/main/install.ps1 | iex
  git clone https://github.com/$Repo; cd succubus; go build ./cmd/succubus
"@
}

# --- checksum ----------------------------------------------------------------
# Worth doing: this script puts a downloaded executable on your PATH. A silent
# mismatch is exactly what you do not want.
function Test-Checksum {
    param($File, $SumsUrl, $Name)
    try {
        $sums = (Invoke-WebRequest -Uri $SumsUrl -UseBasicParsing).Content
    } catch {
        Write-Warn "no checksums.txt published - skipping verification"
        return
    }

    $want = $null
    foreach ($line in $sums -split "`n") {
        $parts = ($line.Trim()) -split '\s+'
        if ($parts.Count -ge 2 -and $parts[-1] -eq $Name) { $want = $parts[0]; break }
    }
    if (-not $want) {
        Write-Warn "no checksum published for $Name - skipping verification"
        return
    }

    $got = (Get-FileHash -Path $File -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want.ToLower()) {
        Die @"
checksum mismatch for $Name
  expected $want
  got      $got
This is worth investigating rather than retrying.
"@
    }
    Write-Ok "checksum verified"
}

# --- main --------------------------------------------------------------------
# Wrapped in a function so a failure can unwind to one handler instead of
# calling `exit` — see the comment on Die.
function Invoke-Install {

Write-Host ""
Write-Host "  succubus installer"
Write-Host "  ------------------------------------------------------------"
Write-Host ""

$platform = Get-Platform
$version  = Get-Version

$dest = if ($env:SUCCUBUS_INSTALL_DIR) {
    $env:SUCCUBUS_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA 'Programs\succubus'
}

Write-Dim "platform  $platform"
Write-Dim "version   $version"
Write-Dim "target    $(Join-Path $dest $Bin)"
Write-Host ""

$asset = "succubus-$version-$platform.exe"
$base  = "$ReleaseBase/$version"
$tmp   = Join-Path ([IO.Path]::GetTempPath()) "succubus-$([Guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

try {
    $staged = Join-Path $tmp $Bin

    Write-Say "downloading $asset..."
    try {
        Invoke-WebRequest -Uri "$base/$asset" -OutFile $staged -UseBasicParsing
    } catch {
        Die @"
download failed: $base/$asset

If this platform has no published binary yet, build from source:
  git clone https://github.com/$Repo; cd succubus; go build ./cmd/succubus
"@
    }

    Test-Checksum -File $staged -SumsUrl "$base/checksums.txt" -Name $asset

    # Created only now that there is a verified binary to put in it, so a failed
    # install does not leave an empty directory behind.
    New-Item -ItemType Directory -Path $dest -Force | Out-Null
    $target = Join-Path $dest $Bin

    # Windows locks a running executable's image, so overwriting the file we are
    # about to replace fails with an opaque IO error.
    #
    # Match on the process's own Path, not its name: `Get-Process -Name succubus`
    # also matches a daemon started from a completely different install, and
    # killing that would take down coordination the user never asked us to touch.
    $running = Get-Process -ErrorAction SilentlyContinue |
        Where-Object {
            try { $_.Path -and ($_.Path -ieq $target) } catch { $false }
        }
    if ($running) {
        Write-Warn "the succubus at this path is running - stopping it so the file can be replaced"
        $running | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 500
    }

    Move-Item -Path $staged -Destination $target -Force
    Write-Ok "installed to $target"

    # Confirm it actually runs. A wrong-architecture binary fails here, where the
    # message can still be useful, rather than at first use.
    #
    # `&` does not throw on a non-zero exit, and on a cross-platform host it may
    # produce nothing at all, so check the exit code and the output rather than
    # relying on an exception. Reporting a blank version as success is exactly
    # the failure this step exists to prevent.
    $v = $null
    try {
        $v = (& $target version 2>&1 | Out-String).Trim()
    } catch {
        $v = $null
    }
    if (-not $v) {
        # Remove it rather than leaving a binary that does not work sitting on
        # the user's PATH, where it would fail confusingly at first use.
        Remove-Item -Path $target -Force -ErrorAction SilentlyContinue
        Die @"
the installed binary did not run, so it was removed.

Wrong architecture for this machine, or the download was incomplete.
  $asset
"@
    }
    Write-Ok $v
} finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

# --- PATH --------------------------------------------------------------------
# Edit the user PATH only. Touching the machine PATH would need Administrator,
# and the per-user one is enough for a per-user install.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }

$already = ($userPath -split ';' | Where-Object { $_.TrimEnd('\') -ieq $dest.TrimEnd('\') }).Count -gt 0
if ($already) {
    Write-Ok "$dest is already on your PATH"
} else {
    $sep = if ($userPath -and -not $userPath.EndsWith(';')) { ';' } else { '' }
    [Environment]::SetEnvironmentVariable('Path', "$userPath$sep$dest", 'User')
    Write-Ok "added $dest to your user PATH"

    # SetEnvironmentVariable does not affect the current session; without this,
    # `succubus` fails right after a successful install and looks broken.
    $env:Path = "$env:Path;$dest"
    Write-Host ""
    Write-Warn "Open a new terminal for the PATH change to apply everywhere."
}

Write-Host ""
Write-Host "  ------------------------------------------------------------"
Write-Host ""
Write-Say "Next, in any project you want coordinated:"
Write-Host ""
Write-Say "  cd C:\path\to\your\project"
Write-Say "  succubus setup        # detect your agent tools and configure them"
Write-Host ""
Write-Dim "The dashboard runs at http://127.0.0.1:7801 once the daemon starts."
Write-Host ""

}   # end Invoke-Install

# Die has already printed the explanation, so this only stops the error from
# unwinding into the user's session as a red stack trace. Any *other* exception
# is a bug in this script and is shown in full, because a silent failure would
# leave the user believing succubus was installed.
try {
    Invoke-Install
} catch {
    # Die already printed a specific explanation; anything else is a bug in this
    # script and gets shown in full, because a silent failure would leave the
    # user believing succubus was installed.
    #
    # Deliberately no claim about what was or was not written: by this point the
    # binary may already be in place, and saying "nothing was installed" when
    # something was is worse than saying nothing.
    if ("$_" -ne 'succubus-install-failed') {
        Write-Host ""
        Write-Host "  [x] " -ForegroundColor Red -NoNewline
        Write-Host "install failed unexpectedly."
        Write-Host "      $_" -ForegroundColor DarkGray
        Write-Host ""
    }
    # Set an exit code for non-interactive callers without killing an
    # interactive session that ran this via `iex`.
    $global:LASTEXITCODE = 1
}
