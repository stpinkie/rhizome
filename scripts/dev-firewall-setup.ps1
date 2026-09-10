#Requires -RunAsAdministrator
# scripts/dev-firewall-setup.ps1
# One-time setup: create Windows Firewall allow rules for all Rhizome .exe files
# under build\ and web\build\. Run as Administrator.
#
# Usage:
#   .\scripts\dev-firewall-setup.ps1
#   .\scripts\dev-firewall-setup.ps1 -BuildMain
#   .\scripts\dev-firewall-setup.ps1 -BuildMain -BuildLauncher

param(
    [switch] $BuildMain,
    [switch] $BuildLauncher
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
. "$PSScriptRoot\windows-firewall-utils.ps1"

if (-not (Test-IsAdministrator)) {
    Write-Error "This script must be run as Administrator to create firewall rules."
    exit 1
}

$env:CGO_ENABLED = '0'
$tags = 'goolm,stdjson'

function Build-Main {
    Write-Host "Building build\rhizome.exe ..."
    New-Item -ItemType Directory -Path "$repoRoot\build" -Force | Out-Null
    Push-Location $repoRoot
    try {
        & go build -tags $tags -o "$repoRoot\build\rhizome.exe" ./cmd/rhizome
        if ($LASTEXITCODE -ne 0) { throw "go build ./cmd/rhizome failed" }
    } finally {
        Pop-Location
    }
}

function Build-Launcher {
    Write-Host "Building build\rhizome-launcher.exe with make build-launcher ..."
    Push-Location $repoRoot
    try {
        & make build-launcher
        if ($LASTEXITCODE -ne 0) { throw "make build-launcher failed" }
    } finally {
        Pop-Location
    }
}

if ($BuildMain) { Build-Main }
if ($BuildLauncher) { Build-Launcher }

Write-Host "`nSearching for Rhizome executables..."
$exes = Get-RhizomeProjectExes -RepoRoot $repoRoot
if ($exes.Count -eq 0) {
    Write-Host "No .exe files found under build\ or web\build\."
    Write-Host "Build the project first, or re-run with -BuildMain / -BuildLauncher."
    exit 0
}

Write-Host "Found $($exes.Count) executable(s). Adding allow rules..."
foreach ($exe in $exes) {
    Add-RhizomeExeFirewallRules -Path $exe.FullName
}

Write-Host "`nDone. $($exes.Count) binary(s) are now allowed through Windows Firewall."
