param(
    [string]$BinDir = "$HOME\.local\bin",
    [switch]$InstallOnly,
    [switch]$WithAttachments,
    [switch]$WithBrowser,
    [switch]$WithSlackdump
)

$ErrorActionPreference = "Stop"

$SkillDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$BinaryName = "slack-mgmt.exe"
$BuildOutput = Join-Path $SkillDir $BinaryName
$AgentsDest = Join-Path $HOME ".agents\skills\slack-management"
$ClaudeDest = Join-Path $HOME ".claude\skills\slack-management"
$CodexDest = Join-Path $HOME ".codex\skills\slack-management"
$ConfigDir = Join-Path ([Environment]::GetFolderPath("ApplicationData")) "slack-mgmt"
$InstallStatePath = Join-Path $ConfigDir "install.json"
$BuildVersion = "dev"
$BuildCommit = "unknown"
$BuildDate = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$BuildLdflags = ""

function Write-Info([string]$Message) {
    Write-Host $Message -ForegroundColor Green
}

function Write-Warn([string]$Message) {
    Write-Host $Message -ForegroundColor Yellow
}

function Ensure-Go {
    if (Get-Command go -ErrorAction SilentlyContinue) {
        Write-Info "Go already installed: $(go version)"
        return
    }

    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        throw "Go is missing and winget is not available. Install Go first."
    }

    Write-Warn "Go not found. Installing via winget..."
    winget install --exact --id GoLang.Go --accept-package-agreements --accept-source-agreements
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "Go install completed but go is still not on PATH. Restart the shell and rerun setup."
    }
    Write-Info "Go installed: $(go version)"
}

function Get-VersionMetadata {
    if (Get-Command git -ErrorAction SilentlyContinue) {
        try {
            Push-Location $SkillDir
            try {
                $script:BuildVersion = (git describe --tags --always 2>$null)
                if (-not $script:BuildVersion) {
                    $script:BuildVersion = "dev"
                }

                $script:BuildCommit = (git rev-parse --short HEAD 2>$null)
                if (-not $script:BuildCommit) {
                    $script:BuildCommit = "unknown"
                }
            }
            finally {
                Pop-Location
            }
        }
        catch {
            $script:BuildVersion = "dev"
            $script:BuildCommit = "unknown"
        }
    }

    $script:BuildLdflags = "-X main.Version=$script:BuildVersion -X main.Commit=$script:BuildCommit -X main.BuildDate=$script:BuildDate"
}

function Build-Cli {
    Write-Info "Building $BinaryName ..."
    Push-Location $SkillDir
    try {
        go build -trimpath -ldflags $BuildLdflags -o $BuildOutput ./cmd/slack-mgmt
    }
    finally {
        Pop-Location
    }
    Write-Info "Built: $BuildOutput"
}

function Install-Binary {
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
    $Destination = Join-Path $BinDir $BinaryName
    $Temporary = "$Destination.tmp.$PID"
    Copy-Item $BuildOutput $Temporary -Force
    Move-Item $Temporary $Destination -Force
    Write-Info "Installed binary: $Destination"
}

function Install-BashShim {
    $ShimPath = Join-Path $BinDir "slack-mgmt"
    $ShimContent = @"
#!/usr/bin/env sh
DIR=`$(CDPATH= cd -- "`$(dirname -- "`$0")" && pwd)
exec "`$DIR/slack-mgmt.exe" "`$@"
"@
    $Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    $NormalizedShimContent = $ShimContent.TrimStart() -replace "`r`n?", "`n"
    [System.IO.File]::WriteAllText($ShimPath, $NormalizedShimContent, $Utf8NoBom)
    Write-Info "Installed bash shim: $ShimPath"
}

function Scrub-GitMetadata([string]$Path) {
    if (-not (Test-Path $Path)) { return }
    Get-ChildItem -LiteralPath $Path -Force -Recurse -ErrorAction Stop |
        Where-Object { @(".git", ".gitignore", ".gitattributes", ".gitmodules") -contains $_.Name } |
        Sort-Object { $_.FullName.Length } -Descending |
        ForEach-Object { Remove-Item -LiteralPath $_.FullName -Recurse -Force }
}

function Install-SkillArtifact {
    $Exclude = @(".git", ".gitignore", ".gitattributes", ".gitmodules", "AGENTS.md", "CLAUDE.md", "LOGBOOK.md", ".temp", ".task-board", ".agents", ".claude", ".codex", ".local", ".planning", ".research", ".spec", "task-board.config.json", "dist", "slack-mgmt", "slack-mgmt.exe")
    if (Test-Path $AgentsDest) {
        Remove-Item $AgentsDest -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $AgentsDest | Out-Null

    Get-ChildItem -LiteralPath $SkillDir -Force | Where-Object { $Exclude -notcontains $_.Name } | ForEach-Object {
        Copy-Item $_.FullName -Destination $AgentsDest -Recurse -Force
    }

    @("scripts\verify-agent-safety-contract.sh", "scripts\verify-agent-safety-contract-test.sh") | ForEach-Object {
        $LocalOnlyPath = Join-Path $AgentsDest $_
        if (Test-Path $LocalOnlyPath) {
            Remove-Item -LiteralPath $LocalOnlyPath -Force
        }
    }

    Scrub-GitMetadata $AgentsDest
    Write-Info "Installed skill artifact: $AgentsDest"
}

function Install-OptionalIntegrations {
    if ($WithAttachments) {
        if (-not (Get-Command agents-infra -ErrorAction SilentlyContinue)) {
            throw "-WithAttachments requires agents-infra on PATH. Install relux-agents-infra, then rerun setup."
        }
        $LauncherPath = Join-Path $BinDir "agents-attachments.cmd"
        $Launcher = "@echo off`r`nsetlocal`r`nset `"DIR=%~dp0`"`r`nif exist `"%DIR%agents-infra.cmd`" (`r`n  `"%DIR%agents-infra.cmd`" attachments %*`r`n  exit /b`r`n)`r`nif exist `"%DIR%agents-infra.exe`" (`r`n  `"%DIR%agents-infra.exe`" attachments %*`r`n  exit /b`r`n)`r`nagents-infra attachments %*`r`nexit /b`r`n"
        $Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText($LauncherPath, $Launcher, $Utf8NoBom)
        Write-Info "Installed optional attachment bridge: $LauncherPath"
    }

    if ($WithBrowser) {
        throw "-WithBrowser is unavailable on Windows; browser transport is macOS-only."
    }

    if ($WithSlackdump) {
        $SlackdumpCommand = Get-Command slackdump -ErrorAction SilentlyContinue
        if (-not $SlackdumpCommand) {
            throw "-WithSlackdump requires an official Slackdump v4 executable on PATH."
        }
        $SlackdumpVersion = (& $SlackdumpCommand.Source version 2>&1 | Out-String).Trim()
        if ($LASTEXITCODE -ne 0 -or $SlackdumpVersion -notmatch '(?i)\bv?4\.') {
            throw "Unsupported Slackdump version: $SlackdumpVersion (expected v4)."
        }
        Write-Info "Verified optional Slackdump provider: $SlackdumpVersion"
    }
}

function New-DirLink([string]$LinkPath, [string]$TargetPath) {
    $Parent = Split-Path -Parent $LinkPath
    New-Item -ItemType Directory -Force -Path $Parent | Out-Null
    if (Test-Path $LinkPath) {
        Remove-Item $LinkPath -Recurse -Force
    }
    New-Item -ItemType Junction -Path $LinkPath -Target $TargetPath | Out-Null
}

function Refresh-Links {
    New-DirLink $ClaudeDest $AgentsDest
    New-DirLink $CodexDest $AgentsDest
    Write-Info "Refreshed Claude/Codex skill links"
}

function Write-InstallState {
    New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null
    $Payload = @{
        repoPath = $SkillDir
        installedSkillPath = $AgentsDest
        binDir = $BinDir
        platform = "windows"
        arch = [System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString().ToLowerInvariant()
        version = $BuildVersion
        commit = $BuildCommit
        buildDate = $BuildDate
        installOnly = [bool]$InstallOnly
        attachmentsEnabled = [bool]$WithAttachments
        browserEnabled = [bool]$WithBrowser
        slackdumpEnabled = [bool]$WithSlackdump
    } | ConvertTo-Json
    Set-Content -Path $InstallStatePath -Value $Payload
    Write-Info "Install state: $InstallStatePath"
}

function Ensure-UserPath {
    $CurrentUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $Parts = @()
    if ($CurrentUserPath) {
        $Parts = $CurrentUserPath -split ';'
    }

    if ($Parts -notcontains $BinDir) {
        $NewPath = (($Parts + $BinDir) | Where-Object { $_ -and $_.Trim() -ne "" } | Select-Object -Unique) -join ';'
        [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
        Write-Warn "Added $BinDir to the user PATH. Restart the shell if needed."
    }

    if (($env:Path -split ';') -notcontains $BinDir) {
        $env:Path = "$BinDir;$env:Path"
    }
}

function Invoke-NativeChecked {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Description,

        [Parameter(Mandatory = $true)]
        [string]$FilePath,

        [Parameter(Mandatory = $true)]
        [string[]]$ArgumentList
    )

    & $FilePath @ArgumentList | Out-Null
    $ExitCode = $LASTEXITCODE
    if ($ExitCode -ne 0) {
        throw "$Description failed with exit code $ExitCode"
    }
}

function Verify-Install {
    $InstalledBinary = Join-Path $BinDir $BinaryName
    $InstalledShim = Join-Path $BinDir "slack-mgmt"
    if (-not (Test-Path $InstalledBinary)) {
        throw "Missing installed binary: $InstalledBinary"
    }
    if (-not (Test-Path $InstalledShim)) {
        throw "Missing installed bash shim: $InstalledShim"
    }
    if (-not (Test-Path (Join-Path $AgentsDest "SKILL.md"))) {
        throw "Installed skill artifact is missing SKILL.md"
    }
    if (-not (Test-Path (Join-Path $AgentsDest "LICENSE"))) {
        throw "Installed skill artifact is missing LICENSE"
    }
    if (-not (Test-Path (Join-Path $ClaudeDest "SKILL.md"))) {
        throw "Claude skill junction is not usable: $ClaudeDest"
    }
    if (-not (Test-Path (Join-Path $CodexDest "SKILL.md"))) {
        throw "Codex skill junction is not usable: $CodexDest"
    }
    $GitDebris = Get-ChildItem -LiteralPath $AgentsDest -Force -Recurse -ErrorAction Stop |
        Where-Object { @(".git", ".gitignore", ".gitattributes", ".gitmodules") -contains $_.Name } |
        Select-Object -First 1
    if ($GitDebris) {
        throw "Installed skill artifact still contains Git metadata: $($GitDebris.FullName)"
    }
    if ($WithAttachments) {
        if (-not (Test-Path (Join-Path $BinDir "agents-attachments.cmd"))) {
            throw "Missing attachment bridge"
        }
        Invoke-NativeChecked -Description "agents-infra version smoke" -FilePath (Get-Command agents-infra).Source -ArgumentList @("version")
    }

    Invoke-NativeChecked -Description "version smoke" -FilePath $InstalledBinary -ArgumentList @("version")
    Invoke-NativeChecked -Description "query schema smoke" -FilePath $InstalledBinary -ArgumentList @("q", "schema()", "--format", "json")
    Invoke-NativeChecked -Description "provider capabilities smoke" -FilePath $InstalledBinary -ArgumentList @("q", "provider_capabilities()", "--format", "json")
    Invoke-NativeChecked -Description "mutation schema smoke" -FilePath $InstalledBinary -ArgumentList @("m", "schema()", "--format", "json")
    Invoke-NativeChecked -Description "auth config-path smoke" -FilePath $InstalledBinary -ArgumentList @("auth", "config-path")
    Write-Info "Verified binary and skill artifact"
}

Write-Host ""
Write-Info "=== slack-management setup ==="
Write-Host ""
if ($InstallOnly) {
    Write-Warn "Running safe reinstall flow (--InstallOnly)"
}

Ensure-Go
Get-VersionMetadata
Build-Cli
Install-Binary
Install-BashShim
Install-SkillArtifact
Install-OptionalIntegrations
Refresh-Links
Write-InstallState
Ensure-UserPath
Verify-Install

Write-Host ""
Write-Info "=== Done ==="
