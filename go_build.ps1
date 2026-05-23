# go_build.ps1
# Build Go binary as static, stripped binary for Baota (Linux amd64 by default)
# Features:
#   - Reads required Go version from go.mod
#   - Auto-detects service entry point from cmd/ subdirectories containing main.go
#   - Binary name can be derived from module name or service name (configurable)
#   - Local module cache (.gocache, .gomodcache)
#   - Optional UPX compression

param(
    [string]$ServiceName,                     # Entry point subdir under cmd/. Auto-detected if omitted.
    [string]$OutputName,                      # Optional: override binary output name (highest priority).
    [ValidateSet('Module', 'Service')]
    [string]$NameSource = "Module",           # Source for binary name when -OutputName not specified.
    [string]$GoOS = "linux",
    [string]$GoArch = "amd64",
    [switch]$CompressWithUpx,
    [string]$LdFlags = "-s -w"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

Set-Location $PSScriptRoot

# -----------------------------------------------------------------------------
# Helper functions
# -----------------------------------------------------------------------------
function Fail-Build {
    param([string]$Message)
    Write-Host "ERROR: $Message" -ForegroundColor Red
    exit 1
}

function Quote-ProcessArgument {
    param([string]$Argument)

    if ($null -eq $Argument -or $Argument.Length -eq 0) {
        return '""'
    }
    if ($Argument -notmatch '[\s"]') {
        return $Argument
    }

    $result = '"'
    $backslashes = 0
    foreach ($char in $Argument.ToCharArray()) {
        if ($char -eq '\') {
            $backslashes++
            continue
        }
        if ($char -eq '"') {
            if ($backslashes -gt 0) {
                $result += ('\' * ($backslashes * 2))
            }
            $result += '\"'
            $backslashes = 0
            continue
        }
        if ($backslashes -gt 0) {
            $result += ('\' * $backslashes)
            $backslashes = 0
        }
        $result += $char
    }
    if ($backslashes -gt 0) {
        $result += ('\' * ($backslashes * 2))
    }
    $result += '"'

    return $result
}

function Invoke-GoCapture {
    param([string[]]$Arguments)

    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = "go"
    $startInfo.Arguments = ($Arguments | ForEach-Object { Quote-ProcessArgument $_ }) -join " "
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo

    try {
        [void]$process.Start()
        $stdout = $process.StandardOutput.ReadToEnd()
        $stderr = $process.StandardError.ReadToEnd()
        $process.WaitForExit()
        $output = (($stdout, $stderr) -join "").Trim()
        $exitCode = $process.ExitCode
    } finally {
        $process.Dispose()
    }

    [pscustomobject]@{
        ExitCode = $exitCode
        Output   = $output
    }
}

function Get-GoVersionFromText {
    param([string]$VersionOutput)

    if ($VersionOutput -notmatch "go version go(?<Version>\d+\.\d+(?:\.\d+)?)") {
        return $null
    }
    return [version]$Matches.Version
}

function Get-BinaryNameFromModule {
    param([string]$ModuleName)

    # If the module name contains '/', take the last path segment
    if ($ModuleName -match '/') {
        $parts = $ModuleName -split '/'
        $lastPart = $parts[-1]
        if ([string]::IsNullOrEmpty($lastPart)) {
            return $null
        }
        return $lastPart
    }
    # Otherwise, use the whole module name as the binary name
    return $ModuleName
}

# -----------------------------------------------------------------------------
# Parse go.mod
# -----------------------------------------------------------------------------
$goModPath = Join-Path $PSScriptRoot "go.mod"
if (-not (Test-Path $goModPath)) {
    Fail-Build "go.mod not found in $PSScriptRoot"
}

Write-Host "Parsing go.mod ..." -ForegroundColor Cyan

$goModContent = Get-Content $goModPath -Raw

# Extract module name
$moduleMatch = [regex]::Match($goModContent, '^module\s+(\S+)', 'Multiline')
if (-not $moduleMatch.Success) {
    Fail-Build "go.mod does not contain a module declaration."
}
$moduleName = $moduleMatch.Groups[1].Value

# Extract required Go version
$goVersionMatch = [regex]::Match($goModContent, '^go\s+(\d+\.\d+(?:\.\d+)?)', 'Multiline')
if (-not $goVersionMatch.Success) {
    Fail-Build "go.mod does not specify a Go version (missing 'go 1.x' directive)."
}
$requiredGoVersion = [version]$goVersionMatch.Groups[1].Value
Write-Host "Module: $moduleName" -ForegroundColor Gray
Write-Host "Requires Go >= $requiredGoVersion" -ForegroundColor Gray

# Derive binary name from module (for possible use later)
$derivedModuleName = Get-BinaryNameFromModule $moduleName
if ([string]::IsNullOrEmpty($derivedModuleName)) {
    Fail-Build "Cannot derive binary name from module '$moduleName'. Please specify -OutputName."
}

# -----------------------------------------------------------------------------
# Detect service entry point from cmd/
# -----------------------------------------------------------------------------
$cmdPath = Join-Path $PSScriptRoot "cmd"
if (-not (Test-Path $cmdPath)) {
    Fail-Build "cmd/ directory not found at $cmdPath"
}

$availableServices = @(Get-ChildItem $cmdPath -Directory | Where-Object {
    Test-Path (Join-Path $_.FullName "main.go")
} | ForEach-Object { $_.Name })

if ($availableServices.Count -eq 0) {
    Fail-Build "No service found under cmd/ (no subdirectory contains a main.go file)."
}

if ([string]::IsNullOrEmpty($ServiceName)) {
    if ($availableServices.Count -eq 1) {
        $ServiceName = $availableServices[0]
        Write-Host "Auto-detected service entry point: $ServiceName" -ForegroundColor Yellow
    } else {
        Write-Host "Available services: $($availableServices -join ', ')" -ForegroundColor Yellow
        Fail-Build "Multiple services found. Please specify -ServiceName parameter."
    }
} else {
    if ($availableServices -notcontains $ServiceName) {
        Write-Host "Available services: $($availableServices -join ', ')" -ForegroundColor Yellow
        Fail-Build "Service '$ServiceName' not found under cmd/ (no main.go in cmd/$ServiceName)."
    }
}

# -----------------------------------------------------------------------------
# Determine final binary output name
# -----------------------------------------------------------------------------
if (-not [string]::IsNullOrEmpty($OutputName)) {
    $outputName = $OutputName
    Write-Host "Binary name overridden by -OutputName: $outputName" -ForegroundColor Gray
} else {
    switch ($NameSource) {
        'Module' {
            $outputName = $derivedModuleName
            Write-Host "Binary name derived from module (via -NameSource Module): $outputName" -ForegroundColor Gray
        }
        'Service' {
            $outputName = $ServiceName
            Write-Host "Binary name derived from service entry point (via -NameSource Service): $outputName" -ForegroundColor Gray
        }
        default {
            Fail-Build "Invalid -NameSource value: $NameSource. Must be 'Module' or 'Service'."
        }
    }
}

if ([string]::IsNullOrEmpty($outputName)) {
    Fail-Build "Binary name is empty. Check -OutputName or -NameSource settings."
}

# -----------------------------------------------------------------------------
# Prepare local caches
# -----------------------------------------------------------------------------
Write-Host "[1/5] Preparing local Go caches..." -ForegroundColor Cyan
$env:GOCACHE = Join-Path $PSScriptRoot ".gocache"
$env:GOMODCACHE = Join-Path $PSScriptRoot ".gomodcache"
New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null
New-Item -ItemType Directory -Force -Path $env:GOMODCACHE | Out-Null

# -----------------------------------------------------------------------------
# Check Go toolchain version
# -----------------------------------------------------------------------------
Write-Host "[2/5] Checking Go toolchain..." -ForegroundColor Cyan
$goToolchainResult = Invoke-GoCapture @("env", "GOTOOLCHAIN")
if ($goToolchainResult.ExitCode -ne 0) {
    Fail-Build "Unable to read GOTOOLCHAIN. Output: $($goToolchainResult.Output)"
}
$goToolchain = $goToolchainResult.Output

$goVersionResult = Invoke-GoCapture @("version")
if ($goVersionResult.ExitCode -ne 0) {
    Fail-Build "Unable to run go version. Install Go $requiredGoVersion or newer, or allow GOTOOLCHAIN=auto to select it. Output: $($goVersionResult.Output)"
}

$goVersionText = $goVersionResult.Output
$currentGoVersion = Get-GoVersionFromText $goVersionText
if ($null -eq $currentGoVersion) {
    Fail-Build "Unable to parse Go version from: $goVersionText"
}

if ($currentGoVersion -lt $requiredGoVersion) {
    Fail-Build "Detected $goVersionText with GOTOOLCHAIN=$goToolchain.`nThis build requires Go $requiredGoVersion or newer; install a newer Go toolchain or set GOTOOLCHAIN=auto."
}

Write-Host "Using $goVersionText (GOTOOLCHAIN=$goToolchain)" -ForegroundColor Gray

# -----------------------------------------------------------------------------
# Build
# -----------------------------------------------------------------------------
Write-Host "[3/5] Building Go binary for $GoOS/$GoArch ..." -ForegroundColor Cyan

$env:GOOS = $GoOS
$env:GOARCH = $GoArch
$env:CGO_ENABLED = "0"

$entryPoint = "./cmd/$ServiceName"
$buildArgs = @(
    "build",
    "-trimpath",
    "-buildvcs=false",
    "-ldflags=$LdFlags",
    "-o", $outputName,
    $entryPoint
)

Write-Host "Running: go $($buildArgs -join ' ')" -ForegroundColor Gray
$buildResult = Invoke-GoCapture $buildArgs
if ($buildResult.Output) {
    Write-Host $buildResult.Output
}
if ($buildResult.ExitCode -ne 0) {
    Fail-Build "Build failed"
}

Write-Host "[4/5] Build succeeded: $outputName" -ForegroundColor Green

$fileInfo = Get-Item $outputName
Write-Host "File size: $([math]::Round($fileInfo.Length / 1MB, 2)) MB" -ForegroundColor Yellow

# -----------------------------------------------------------------------------
# Optional UPX compression
# -----------------------------------------------------------------------------
if ($CompressWithUpx) {
    Write-Host "[5/5] Compressing with UPX..." -ForegroundColor Cyan
    if (Get-Command upx -ErrorAction SilentlyContinue) {
        upx --best --lzma $outputName
        $compressedInfo = Get-Item $outputName
        Write-Host "Compressed size: $([math]::Round($compressedInfo.Length / 1MB, 2)) MB" -ForegroundColor Green
    } else {
        Write-Host "Warning: UPX not found. Skipping compression." -ForegroundColor Yellow
    }
} else {
    Write-Host "[5/5] Skipping UPX compression." -ForegroundColor Gray
}

Write-Host "Done. Binary: $outputName" -ForegroundColor Cyan