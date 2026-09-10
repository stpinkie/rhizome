# Shared helpers for managing Windows Firewall allow rules for Rhizome binaries.
# Dot-source this from dev-firewall-setup.ps1, test-windows.ps1, and the
# integration-*.ps1 scripts.

function Test-IsAdministrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-RhizomeRuleName {
    param([Parameter(Mandatory)] [string] $Path)
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($Path.ToLowerInvariant())
    $stream = [System.IO.MemoryStream]::new($bytes)
    $hash = (Get-FileHash -InputStream $stream -Algorithm SHA1).Hash
    $stream.Dispose()
    return "rhizome-$hash"
}

function Add-RhizomeExeFirewallRules {
    param(
        [Parameter(Mandatory)] [string] $Path,
        [string] $DisplayName
    )
    $abs = (Resolve-Path $Path -ErrorAction Stop).Path
    if (-not (Test-Path $abs)) {
        throw "Binary not found: $abs"
    }

    $ruleName = Get-RhizomeRuleName -Path $abs
    if ([string]::IsNullOrWhiteSpace($DisplayName)) {
        $DisplayName = "Rhizome allow $([System.IO.Path]::GetFileName($abs))"
    }

    $profiles = @("Domain", "Private", "Public")
    $existingIn = Get-NetFirewallRule -Name "$ruleName-in" -ErrorAction SilentlyContinue
    $existingOut = Get-NetFirewallRule -Name "$ruleName-out" -ErrorAction SilentlyContinue

    if (-not $existingIn) {
        New-NetFirewallRule `
            -Name "$ruleName-in" `
            -DisplayName "$DisplayName inbound" `
            -Direction Inbound `
            -Action Allow `
            -Program $abs `
            -Profile $profiles `
            -Enabled True | Out-Null
        Write-Host "  [+] inbound rule for $abs"
    } else {
        Write-Host "  [=] inbound rule already exists for $abs"
    }

    if (-not $existingOut) {
        New-NetFirewallRule `
            -Name "$ruleName-out" `
            -DisplayName "$DisplayName outbound" `
            -Direction Outbound `
            -Action Allow `
            -Program $abs `
            -Profile $profiles `
            -Enabled True | Out-Null
        Write-Host "  [+] outbound rule for $abs"
    } else {
        Write-Host "  [=] outbound rule already exists for $abs"
    }
}

function Get-RhizomeProjectExes {
    param([Parameter(Mandatory)] [string] $RepoRoot)
    $dirs = @(
        (Join-Path $RepoRoot 'build'),
        (Join-Path (Join-Path $RepoRoot 'web') 'build')
    )
    $exes = @()
    foreach ($d in $dirs) {
        if (Test-Path $d) {
            $exes += Get-ChildItem -Path $d -Recurse -Filter '*.exe' -ErrorAction SilentlyContinue
        }
    }
    return $exes
}

function Get-PackageTestBinaryName {
    param(
        [Parameter(Mandatory)] [string] $ImportPath,
        [Parameter(Mandatory)] [string] $RepoRoot
    )
    $module = 'github.com/stpinkie/rhizome'
    $rel = $ImportPath
    if ($rel -eq $module) {
        $rel = 'root'
    } elseif ($rel.StartsWith("$module/")) {
        $rel = $rel.Substring($module.Length + 1)
    }
    $rel = $rel -replace '[/\\]', '_'
    return "$rel.test.exe"
}
