# Build rhizome and run a two-node mesh integration test on Windows.
# 1. Create three node identities (A, B, C).
# 2. Start daemon A and daemon B (B bootstraps to A).
# 3. Ping A from C.
# 4. Write a file in A's workspace and wait for B to converge.

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$buildDir = [System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), [System.Guid]::NewGuid().ToString())
$testDir = [System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), [System.Guid]::NewGuid().ToString())
$rhizomeBin = Join-Path $buildDir "rhizome.exe"

$processes = @()

try {
    New-Item -ItemType Directory -Path $buildDir -Force | Out-Null
    New-Item -ItemType Directory -Path $testDir -Force | Out-Null

    Write-Host "Building rhizome..."
    Push-Location $repoRoot
    $env:CGO_ENABLED = '0'
    & go build -tags goolm,stdjson -o $rhizomeBin ./cmd/rhizome
    if ($LASTEXITCODE -ne 0) { throw "build failed" }
    Pop-Location

    $aHome = Join-Path $testDir "a"
    $bHome = Join-Path $testDir "b"
    $cHome = Join-Path $testDir "c"
    New-Item -ItemType Directory -Path $aHome, $bHome, $cHome -Force | Out-Null

    Write-Host "Onboarding nodes..."
    $env:RHIZOME_HOME = $aHome
    & $rhizomeBin network onboard --generate --name a --node-index 0 --encrypt none --yes --non-interactive
    if ($LASTEXITCODE -ne 0) { throw "onboard A failed" }

    $env:RHIZOME_HOME = $bHome
    & $rhizomeBin network onboard --generate --name b --node-index 1 --encrypt none --yes --non-interactive
    if ($LASTEXITCODE -ne 0) { throw "onboard B failed" }

    $env:RHIZOME_HOME = $cHome
    & $rhizomeBin network onboard --generate --name c --node-index 2 --encrypt none --yes --non-interactive
    if ($LASTEXITCODE -ne 0) { throw "onboard C failed" }

    Write-Host "Starting daemon A..."
    $aOut = Join-Path $aHome "daemon.out.log"
    $aErr = Join-Path $aHome "daemon.err.log"
    $env:RHIZOME_HOME = $aHome
    $aJob = Start-Process -FilePath $rhizomeBin -ArgumentList @("daemon", "--allow-empty", "--no-dht", "--no-gateway", "--listen", "/ip4/127.0.0.1/tcp/0", "--sync-commit-interval", "1s", "--sync-announce-interval", "1s") -RedirectStandardOutput $aOut -RedirectStandardError $aErr -PassThru -WindowStyle Hidden
    $processes += $aJob

    $aAddr = $null
    for ($i = 0; $i -lt 50; $i++) {
        if (Test-Path $aOut) {
            $line = Get-Content $aOut -ErrorAction SilentlyContinue | Select-String -Pattern "Addrs:" | Select-Object -First 1
            if ($line) {
                $aAddr = $line.Line -replace '.*Addrs:\s*', ''
                break
            }
        }
        Start-Sleep -Milliseconds 200
    }

    if (-not $aAddr) {
        throw "timed out waiting for daemon A"
    }
    Write-Host "Daemon A is listening on: $aAddr"

    Write-Host "Pinging A from C..."
    $env:RHIZOME_HOME = $cHome
    & $rhizomeBin network ping $aAddr
    if ($LASTEXITCODE -ne 0) { throw "ping from C to A failed" }

    Write-Host "Starting daemon B..."
    $bOut = Join-Path $bHome "daemon.out.log"
    $bErr = Join-Path $bHome "daemon.err.log"
    $env:RHIZOME_HOME = $bHome
    $bJob = Start-Process -FilePath $rhizomeBin -ArgumentList @("daemon", "--allow-empty", "--no-dht", "--no-gateway", "--listen", "/ip4/127.0.0.1/tcp/0", "--bootstrap", $aAddr, "--sync-commit-interval", "1s", "--sync-announce-interval", "1s") -RedirectStandardOutput $bOut -RedirectStandardError $bErr -PassThru -WindowStyle Hidden
    $processes += $bJob

    $bOnline = $false
    for ($i = 0; $i -lt 50; $i++) {
        if (Test-Path $bOut) {
            if (Select-String -Path $bOut -Pattern "Rhizome daemon online" -Quiet) {
                $bOnline = $true
                break
            }
        }
        Start-Sleep -Milliseconds 200
    }
    if (-not $bOnline) { throw "timed out waiting for daemon B" }

    Start-Sleep -Seconds 2

    $aWorkspace = Join-Path $aHome "workspace"
    $bWorkspace = Join-Path $bHome "workspace"
    New-Item -ItemType Directory -Path $aWorkspace -Force | Out-Null
    Set-Content -Path (Join-Path $aWorkspace "AGENT.md") -Value "hello from A" -NoNewline

    Write-Host "Waiting for workspace sync from A to B..."
    $synced = $false
    for ($i = 0; $i -lt 60; $i++) {
        $target = Join-Path $bWorkspace "AGENT.md"
        if (Test-Path $target) {
            $content = Get-Content $target -Raw -ErrorAction SilentlyContinue
            if ($content -eq "hello from A") {
                $synced = $true
                break
            }
        }
        Start-Sleep -Seconds 1
    }

    if (-not $synced) { throw "workspace sync from A to B failed" }

    Write-Host "Integration test passed: ping and workspace sync both work."
}
finally {
    foreach ($p in $processes) {
        if (-not $p.HasExited) {
            Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-Item -Recurse -Force $buildDir -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $testDir -ErrorAction SilentlyContinue
}
