# Canonical native Windows build for CodexLoom. Mirrors the Makefile:
# build the WebUI first (Go embeds internal/webui/dist at compile time),
# then the binaries, then verify the compiled Hub embeds the current WebUI.
#
#   powershell -ExecutionPolicy Bypass -File build.ps1
#
# Do not replace this with a bare `go build`: Go embeds whatever was already
# present in internal/webui/dist, silently shipping a stale frontend.
#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Version = "0.1.0-dev",
    # Skip the npm step when internal/webui/dist is already current.
    [switch]$SkipWeb
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

function Invoke-Checked {
    param([string]$Program, [string[]]$Arguments)
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Program $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

$commit = cmd /c "git rev-parse --short=12 HEAD 2>nul"
if (-not $commit) { $commit = "unknown" }
$porcelain = cmd /c "git status --porcelain 2>nul"
if ($porcelain) { $commit = "$commit-dirty" }
$builtAt = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$module = "github.com/yan5xu/codex-loom"
$ldflags = "-X $module/internal/buildinfo.Version=$Version " +
    "-X $module/internal/buildinfo.Commit=$commit " +
    "-X $module/internal/buildinfo.BuiltAt=$builtAt"

if (-not $SkipWeb) {
    Push-Location web
    try {
        Invoke-Checked npm @("install")
        Invoke-Checked npm @("run", "build")
    } finally {
        Pop-Location
    }
}

New-Item -ItemType Directory -Force -Path bin | Out-Null

$targets = [ordered]@{
    "codex-loom.exe"          = "./cmd/codex-loom"
    "codex-loom-reloader.exe" = "./cmd/codex-loom-reloader"
    "loom.exe"                = "./cmd/loom"
    "loom-gateway.exe"        = "./cmd/loom-gateway"
    "loom-feishu-gateway.exe" = "./cmd/loom-feishu-gateway"
    "loom-slack-gateway.exe"  = "./cmd/loom-slack-gateway"
    "loom-parall-gateway.exe" = "./cmd/loom-parall-gateway"
}
foreach ($name in $targets.Keys) {
    Write-Host "building bin/$name"
    Invoke-Checked go @("build", "-ldflags", $ldflags, "-o", "bin/$name", $targets[$name])
}

# Compatibility aliases retained for existing operator instructions.
Copy-Item bin/codex-loom.exe bin/codex-hub.exe -Force
Copy-Item bin/codex-loom-reloader.exe bin/codex-hub-reloader.exe -Force
Copy-Item bin/loom.exe bin/chub.exe -Force
Copy-Item bin/loom-gateway.exe bin/chub-gateway.exe -Force

# Fail if the compiled server does not contain the current Vite entrypoint.
$index = Get-Content internal/webui/dist/index.html -Raw
if ($index -notmatch 'src="/([^"?]*\.js)"') {
    throw "cannot identify WebUI entrypoint in internal/webui/dist/index.html"
}
$asset = $Matches[1]
$binaryText = [System.Text.Encoding]::ASCII.GetString([System.IO.File]::ReadAllBytes("$PSScriptRoot/bin/codex-loom.exe"))
if (-not $binaryText.Contains($asset)) {
    throw "bin/codex-loom.exe does not embed $asset; rebuild without -SkipWeb"
}
Write-Host "verified embedded WebUI: $asset"
Write-Host "build complete: version=$Version commit=$commit"
