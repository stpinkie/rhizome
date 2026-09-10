# Build rhizome and run a two-node swarm integration test on Windows.
# 1. Create node identities A and B.
# 2. Write configs: mesh + swarm enabled, mutual trust, shared swarm "ops".
# 3. Start daemon A and daemon B (B bootstraps to A).
# 4. Wait until both sides see each other in the "ops" roster.
# 5. Verify roster persistence survives a daemon restart.

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
. "$PSScriptRoot\windows-firewall-utils.ps1"

$buildDir = Join-Path $repoRoot 'build\integration-swarm'
$testDir = [System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), [System.Guid]::NewGuid().ToString())
$rhizomeBin = Join-Path $buildDir "rhizome.exe"

$processes = @()

function Get-PeerID($nodeHome, $bin) {
    $env:RHIZOME_HOME = $nodeHome
    $out = & $bin network status --json 2>$null | Out-String
    if ($LASTEXITCODE -ne 0) { throw "network status failed for $nodeHome" }
    if ($out -match '"peer_id"\s*:\s*"([^"]+)"') { return $Matches[1] }
    throw "peer_id not found in status output for $nodeHome"
}

function Write-SwarmConfig($nodeHome, $peerID, $trustedPeer, $bootstrap) {
    $mesh = [ordered]@{
        enabled       = $true
        trusted_peers = @($trustedPeer)
        dht_enabled   = $false
    }
    if (-not [string]::IsNullOrEmpty($bootstrap)) {
        $mesh["bootstrap_peers"] = @($bootstrap)
    }
    $cfg = [ordered]@{
        mesh  = $mesh
        swarm = [ordered]@{
            enabled     = $true
            memberships = @("ops")
            presence    = [ordered]@{
                heartbeat_interval = "5s"
                expire_after       = "30s"
            }
        }
    }
    # Write without BOM — Go's json decoder rejects it.
    [System.IO.File]::WriteAllText(
        (Join-Path $nodeHome "config.json"),
        ($cfg | ConvertTo-Json -Depth 6))

    # sanity: keep $peerID referenced so the signature is used meaningfully
    if ([string]::IsNullOrEmpty($peerID)) { throw "peer id required" }
}

function Wait-ForMember($nodeHome, $peerID, $timeoutSec) {
    $swarmsFile = Join-Path $nodeHome "swarms.json"
    for ($i = 0; $i -lt $timeoutSec; $i++) {
        # Prefer a live CLI readback; fall back to the persisted file.
        $env:RHIZOME_HOME = $nodeHome
        $membersOut = cmd /c "`"$rhizomeBin`" swarm members ops 2>&1" | Out-String
        if ($membersOut.Contains($peerID)) { return $true }

        if (Test-Path $swarmsFile) {
            $data = Get-Content $swarmsFile -Raw -ErrorAction SilentlyContinue | ConvertFrom-Json -ErrorAction SilentlyContinue
            if ($data.swarms.ops.members -and ($data.swarms.ops.members | Where-Object { $_.peer_id -eq $peerID })) {
                return $true
            }
        }
        Start-Sleep -Seconds 1
    }
    return $false
}

try {
    New-Item -ItemType Directory -Path $buildDir -Force | Out-Null
    New-Item -ItemType Directory -Path $testDir -Force | Out-Null

    Write-Host "Building rhizome..."
    Push-Location $repoRoot
    $env:CGO_ENABLED = '0'
    & go build -tags goolm,stdjson -o $rhizomeBin ./cmd/rhizome
    if ($LASTEXITCODE -ne 0) { throw "build failed" }
    Pop-Location

    Write-Host "Adding firewall allow rule for $rhizomeBin ..."
    Add-RhizomeExeFirewallRules -Path $rhizomeBin

    $aHome = Join-Path $testDir "a"
    $bHome = Join-Path $testDir "b"
    New-Item -ItemType Directory -Path $aHome, $bHome -Force | Out-Null

    Write-Host "Onboarding nodes..."
    $env:RHIZOME_HOME = $aHome
    & $rhizomeBin network onboard --generate --name a --node-index 0 --encrypt none --yes --non-interactive | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "onboard A failed" }
    $env:RHIZOME_HOME = $bHome
    & $rhizomeBin network onboard --generate --name b --node-index 1 --encrypt none --yes --non-interactive | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "onboard B failed" }

    $aPeer = Get-PeerID $aHome $rhizomeBin
    $bPeer = Get-PeerID $bHome $rhizomeBin
    Write-Host "A: $aPeer"
    Write-Host "B: $bPeer"

    # A listens on a fixed loopback port so B's config can embed the bootstrap
    # address before A starts.
    $aAddr = "/ip4/127.0.0.1/tcp/18797/p2p/$aPeer"

    Write-SwarmConfig -nodeHome $aHome -peerID $aPeer -trustedPeer $bPeer -bootstrap ""
    Write-SwarmConfig -nodeHome $bHome -peerID $bPeer -trustedPeer $aPeer -bootstrap $aAddr

    Write-Host "Starting daemon A..."
    $aOut = Join-Path $aHome "daemon.out.log"
    $aErr = Join-Path $aHome "daemon.err.log"
    $env:RHIZOME_HOME = $aHome
    $aJob = Start-Process -FilePath $rhizomeBin -ArgumentList @("daemon", "--allow-empty", "--no-dht", "--no-gateway", "--listen", "/ip4/127.0.0.1/tcp/18797") -RedirectStandardOutput $aOut -RedirectStandardError $aErr -PassThru -WindowStyle Hidden
    $processes += $aJob

    $aOnline = $false
    for ($i = 0; $i -lt 50; $i++) {
        if (Test-Path $aOut) {
            if (Select-String -Path $aOut -Pattern "Rhizome daemon online" -Quiet) {
                $aOnline = $true
                break
            }
        }
        Start-Sleep -Milliseconds 200
    }
    if (-not $aOnline) {
        Get-Content $aOut -ErrorAction SilentlyContinue | Select-Object -Last 20
        Get-Content $aErr -ErrorAction SilentlyContinue | Select-Object -Last 20
        throw "timed out waiting for daemon A"
    }

    Write-Host "Starting daemon B..."
    $bOut = Join-Path $bHome "daemon.out.log"
    $bErr = Join-Path $bHome "daemon.err.log"
    $env:RHIZOME_HOME = $bHome
    $bJob = Start-Process -FilePath $rhizomeBin -ArgumentList @("daemon", "--allow-empty", "--no-dht", "--no-gateway", "--listen", "/ip4/127.0.0.1/tcp/0") -RedirectStandardOutput $bOut -RedirectStandardError $bErr -PassThru -WindowStyle Hidden
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
    if (-not $bOnline) {
        Get-Content $bOut -ErrorAction SilentlyContinue | Select-Object -Last 20
        Get-Content $bErr -ErrorAction SilentlyContinue | Select-Object -Last 20
        throw "timed out waiting for daemon B"
    }

    Write-Host "Waiting for mutual swarm rosters..."
    if (-not (Wait-ForMember $aHome $bPeer 60)) {
        Write-Host "--- daemon A log ---" -ForegroundColor Yellow
        Get-Content $aOut -ErrorAction SilentlyContinue | Select-Object -Last 30
        Get-Content $aErr -ErrorAction SilentlyContinue | Select-Object -Last 30
        Write-Host "--- daemon B log ---" -ForegroundColor Yellow
        Get-Content $bOut -ErrorAction SilentlyContinue | Select-Object -Last 30
        Get-Content $bErr -ErrorAction SilentlyContinue | Select-Object -Last 30
        throw "daemon A never saw B in the ops roster"
    }
    if (-not (Wait-ForMember $bHome $aPeer 30)) {
        throw "daemon B never saw A in the ops roster"
    }
    Write-Host "Both daemons see each other in swarm 'ops'."

    # CLI readback (run through cmd so native stderr writes don't trip EAP).
    $env:RHIZOME_HOME = $aHome
    $membersOut = cmd /c "`"$rhizomeBin`" swarm members ops 2>&1" | Out-String
    if (-not $membersOut.Contains($bPeer)) {
        throw "swarm members did not list B"
    }

    # Restart B and verify the roster is re-established (persistence + rejoin).
    Write-Host "Restarting daemon B to verify roster persistence..."
    if (-not $bJob.HasExited) {
        Stop-Process -Id $bJob.Id -Force -ErrorAction SilentlyContinue
        $bJob.WaitForExit()
    }
    $processes = @($processes | Where-Object { $_.Id -ne $bJob.Id })

    $bOut = Join-Path $bHome "daemon2.out.log"
    $bErr = Join-Path $bHome "daemon2.err.log"
    $env:RHIZOME_HOME = $bHome
    $bJob = Start-Process -FilePath $rhizomeBin -ArgumentList @("daemon", "--allow-empty", "--no-dht", "--no-gateway", "--listen", "/ip4/127.0.0.1/tcp/0") -RedirectStandardOutput $bOut -RedirectStandardError $bErr -PassThru -WindowStyle Hidden
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

    if (-not (Wait-ForMember $bHome $aPeer 60)) {
        throw "daemon B lost the ops roster after restart"
    }

    Write-Host "Swarm integration test passed: join, roster exchange, presence, and restart persistence all work."
}
finally {
    foreach ($p in $processes) {
        if (-not $p.HasExited) {
            Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-Item -Recurse -Force $testDir -ErrorAction SilentlyContinue
}
