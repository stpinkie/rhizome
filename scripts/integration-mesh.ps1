# Build rhizome and run a two-node mesh integration test on Windows.
# 1. Create three node identities (A, B, C).
# 2. Start daemon A and daemon B (B bootstraps to A).
# 3. Ping A from C.
# 4. Write a file in A's workspace and wait for B to converge.
# 5. Write a file in B's workspace and wait for A to converge (bidirectional).
# 6. Restart daemon B and verify a fresh edit on A propagates after reconnect.

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

    # Write a file on B and wait for it to appear on A (bidirectional sync).
    Set-Content -Path (Join-Path $bWorkspace "REPLY.md") -Value "hello from B" -NoNewline

    Write-Host "Waiting for workspace sync from B to A..."
    $synced = $false
    for ($i = 0; $i -lt 60; $i++) {
        $target = Join-Path $aWorkspace "REPLY.md"
        if (Test-Path $target) {
            $content = Get-Content $target -Raw -ErrorAction SilentlyContinue
            if ($content -eq "hello from B") {
                $synced = $true
                break
            }
        }
        Start-Sleep -Seconds 1
    }

    if (-not $synced) { throw "workspace sync from B to A failed" }

    # Restart daemon B to verify announce-on-reconnect catch-up.
    Write-Host "Restarting daemon B to test reconnect catch-up..."
    if (-not $bJob.HasExited) {
        Stop-Process -Id $bJob.Id -Force -ErrorAction SilentlyContinue
        $bJob.WaitForExit()
    }
    $processes = $processes | Where-Object { $_.Id -ne $bJob.Id }

    # Commit a change on A while B is down.
    Set-Content -Path (Join-Path $aWorkspace "AFTER-RECONNECT.md") -Value "second edit" -NoNewline

    $bOut = Join-Path $bHome "daemon2.out.log"
    $bErr = Join-Path $bHome "daemon2.err.log"
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
    if (-not $bOnline) { throw "timed out waiting for restarted daemon B" }

    Write-Host "Waiting for post-reconnect sync from A to B..."
    $synced = $false
    for ($i = 0; $i -lt 60; $i++) {
        $target = Join-Path $bWorkspace "AFTER-RECONNECT.md"
        if (Test-Path $target) {
            $content = Get-Content $target -Raw -ErrorAction SilentlyContinue
            if ($content -eq "second edit") {
                $synced = $true
                break
            }
        }
        Start-Sleep -Seconds 1
    }

    if (-not $synced) { throw "workspace sync after reconnect failed" }

    Write-Host "Integration test passed: ping, bidirectional workspace sync, and reconnect catch-up all work."
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
