# scripts/flake-hunt.ps1 — repeat the networked test packages under go test's
# default package parallelism and report per-test pass rates.
#
# Package-level parallelism is where the libp2p timing flake family lived:
# multiple test binaries sharing the scheduler and the LAN. `-p 1` hides it;
# this script deliberately does NOT use it.
#
# Usage:
#   .\scripts\flake-hunt.ps1
#   .\scripts\flake-hunt.ps1 -Runs 10
#   .\scripts\flake-hunt.ps1 -Runs 3 -Packages './pkg/rhizome/swarm','./pkg/rhizome/sync'
#
# Env:  $env:GO_TAGS overrides the build tags (default: goolm,stdjson).
# Exit: 0 if every test passed in every run, 1 otherwise.
#       Run logs are kept under a printed directory when anything fails.

param(
    [int] $Runs = 5,
    [string[]] $Packages = @(),
    [string] $Tags = $(if ($env:GO_TAGS) { $env:GO_TAGS } else { 'goolm,stdjson' })
)

$ErrorActionPreference = 'Continue'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

if ($Packages.Count -eq 0) {
    $Packages = @(
        './pkg/rhizome/mesh', './pkg/rhizome/swarm', './pkg/rhizome/sync',
        './pkg/rhizome/network', './pkg/rhizome/stream', './pkg/rhizome/pair',
        './pkg/rhizome/blob', './pkg/rhizome/agentrpc', './pkg/rhizome/agenttask',
        './pkg/rhizome/blackboard', './pkg/gateway'
    )
}

$logDir = Join-Path $env:TEMP ("flake-hunt-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $logDir | Out-Null
$env:CGO_ENABLED = '0'

Write-Host "flake-hunt: $Runs run(s), tags=$Tags"
Write-Host "packages: $($Packages -join ' ')"

for ($i = 1; $i -le $Runs; $i++) {
    $log = Join-Path $logDir "run-$i.log"
    Write-Host -NoNewline "run $i/$Runs ... "
    & go test -count=1 -tags $Tags -v @Packages *> $log
    if ($LASTEXITCODE -eq 0) { Write-Host "ok" } else { Write-Host "FAILED" }
}

# Run-level failures: a package/binary that died mid-run (panic, timeout,
# build break) leaves a package FAIL line or a runtime panic; tests in flight
# never report --- FAIL individually, so surface these separately.
Write-Host ""
Write-Host "=== package/binary failures ==="
$panics = Get-ChildItem "$logDir\run-*.log" | Select-String -Pattern '^(panic|fatal error)' |
    ForEach-Object { $_.Line -replace '0x[0-9a-f]+', '0x...' } | Group-Object
$pkgFails = Get-ChildItem "$logDir\run-*.log" | Select-String -Pattern '^FAIL\s+github\.com/' |
    ForEach-Object { $_.Line.Trim() } | Group-Object
$anyFail = $false
foreach ($g in @($panics) + @($pkgFails)) {
    if ($g) { Write-Host ("{0,6}  {1}" -f $g.Count, $g.Name); $anyFail = $true }
}
if (-not $anyFail) { Write-Host "(none)" }

# Per-test tallies: each run prints one '--- PASS|FAIL|SKIP: Name' line per
# test (subtests included).
Write-Host ""
Write-Host "=== per-test results (failures first) ==="
Write-Host ("{0,6} {1,6} {2,6} {3,6}  {4}" -f "FAIL", "PASS", "SKIP", "RUNS", "TEST")

$rx = [regex]'^\s*--- (PASS|FAIL|SKIP): (\S+)'
$stats = @{}
foreach ($file in Get-ChildItem "$logDir\run-*.log") {
    foreach ($line in Get-Content $file.FullName) {
        $m = $rx.Match($line)
        if (-not $m.Success) { continue }
        $name = $m.Groups[2].Value
        if (-not $stats.ContainsKey($name)) {
            $stats[$name] = @{ Pass = 0; Fail = 0; Skip = 0 }
        }
        $stats[$name][$m.Groups[1].Value]++
    }
}

$flaky = 0
$rows = foreach ($name in $stats.Keys) {
    $s = $stats[$name]
    [pscustomobject]@{
        Name = $name; Fail = $s.Fail; Pass = $s.Pass; Skip = $s.Skip
        Total = $s.Fail + $s.Pass + $s.Skip
    }
}
foreach ($row in $rows | Sort-Object -Property @{e='Fail';Descending=$true}, Name) {
    if ($row.Fail -gt 0) { $flaky++ }
    Write-Host ("{0,6} {1,6} {2,6} {3,6}  {4}" -f $row.Fail, $row.Pass, $row.Skip, $row.Total, $row.Name)
}

Write-Host ""
Write-Host "=== summary: $($stats.Count) test(s), $flaky with at least one failure, $Runs run(s) ==="

if ($flaky -gt 0 -or $anyFail) {
    Write-Host "logs kept: $logDir"
    exit 1
}
Remove-Item -Recurse -Force $logDir
exit 0
