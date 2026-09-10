# scripts/test-windows.ps1
# Compile all Go tests to stable paths under build\tests\ and run them.
# Use this on Windows instead of `go test` or `make test` to avoid firewall popups.
#
# Usage:
#   .\scripts\test-windows.ps1
#   .\scripts\test-windows.ps1 -Run '^TestMesh'
#   .\scripts\test-windows.ps1 -Package ./pkg/rhizome/network
#   .\scripts\test-windows.ps1 -NoV
#   .\scripts\test-windows.ps1 -SkipFirewall

param(
    [string] $Package = './...',
    [string[]] $Run,
    [string] $Tags = 'goolm,stdjson',
    [string] $Timeout = '5m',
    [switch] $NoV,
    [switch] $SkipGenerate,
    [switch] $SkipFirewall
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
. "$PSScriptRoot\windows-firewall-utils.ps1"

if (-not $SkipFirewall -and -not (Test-IsAdministrator)) {
    Write-Error "This script must be run as Administrator to create firewall rules. Use -SkipFirewall to run without adding rules (you will get firewall prompts)."
    exit 1
}

$env:CGO_ENABLED = '0'

$testDir = Join-Path $repoRoot 'build\tests'
New-Item -ItemType Directory -Path $testDir -Force | Out-Null

if (-not $SkipGenerate) {
    Write-Host "Running go generate..."
    Push-Location $repoRoot
    try {
        & go generate ./...
        if ($LASTEXITCODE -ne 0) { throw "go generate failed" }
    } finally {
        Pop-Location
    }
}

Write-Host "Listing testable packages..."
Push-Location $repoRoot
try {
    $packages = & go list -tags $Tags -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}|{{.Dir}}{{end}}' $Package
    if ($LASTEXITCODE -ne 0) { throw "go list failed" }
} finally {
    Pop-Location
}

$testFlags = @()
if (-not $NoV) { $testFlags += '-test.v' }
if ($Timeout) { $testFlags += "-test.timeout=$Timeout" }
foreach ($r in $Run) { $testFlags += "-test.run=$r" }

$failed = @()
$passed = 0

foreach ($line in $packages) {
    $parts = $line -split '\|', 2
    if ($parts.Length -ne 2) { continue }
    $importPath, $pkgDir = $parts[0], $parts[1]

    $exeName = Get-PackageTestBinaryName -ImportPath $importPath -RepoRoot $repoRoot
    $exe = Join-Path $testDir $exeName

    Write-Host "`n--- $importPath ---"

    # Remove a stale binary so go test -c can write the new one.
    if (Test-Path $exe) {
        Remove-Item -Path $exe -Force -ErrorAction SilentlyContinue
    }

    # Compile the test binary to the stable path.
    & go test -tags $Tags -c -o $exe $importPath
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "compile failed: $importPath"
        $failed += "compile $importPath"
        continue
    }

    # Add firewall allow rules for this exact .exe before running it.
    if (-not $SkipFirewall) {
        Add-RhizomeExeFirewallRules -Path $exe
    }

    # Run the binary from the package source directory.
    Push-Location $pkgDir
    try {
        & $exe @testFlags
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "test failed: $importPath"
            $failed += "run $importPath"
        } else {
            $passed++
        }
    } finally {
        Pop-Location
    }
}

Write-Host "`n==================================="
Write-Host "Passed: $passed"
Write-Host "Failed: $($failed.Count)"
if ($failed.Count -gt 0) {
    Write-Host "Failures:"
    foreach ($f in $failed) { Write-Host "  - $f" }
    exit 1
}
