# build-boot-wim.ps1 — build the deployserver WinPE boot.wim.
#
# Referenced by docs/WINDOWS.md since Phase 5; requires a Windows host
# with the Windows ADK + WinPE add-on installed. Run from an elevated
# "Deployment and Imaging Tools Environment" prompt (so copype/dism are
# on PATH), from the repo root:
#
#   powershell -ExecutionPolicy Bypass -File winpe\scripts\build-boot-wim.ps1 `
#       -DeployCaPem C:\certs\deploy-ca.pem `
#       -OutWim C:\out\boot.wim
#
# What it does:
#   1. copype amd64 into a work dir
#   2. mount boot.wim
#   3. add the WinPE optional components the deploy flow needs:
#      WMI (hardware fingerprinting), PowerShell + .NET (JSON parsing,
#      Expand-Archive for driver packs), SecureStartup (BitLocker-aware
#      imaging), EnhancedStorage
#   4. install winpe/scripts/startnet.cmd (fetches deploy.cmd from the
#      server at boot — deployment logic updates WITHOUT rebuilding
#      this wim) and the pinned deploy CA at X:\deploy\deploy-ca.pem
#   5. set a scratch space large enough for driver staging
#   6. unmount/commit and export the finished boot.wim
#
# The produced wim is what images.media.bootwim_url should point at
# (upload via the UI's image version panel or `deployctl images upload`).

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$DeployCaPem,

    [Parameter(Mandatory = $true)]
    [string]$OutWim,

    [string]$Arch = "amd64",

    [string]$WorkDir = (Join-Path $env:TEMP "deployserver-winpe"),

    # ADK root; default covers the standard install location.
    [string]$AdkRoot = "${env:ProgramFiles(x86)}\Windows Kits\10\Assessment and Deployment Kit"
)

$ErrorActionPreference = "Stop"

function Assert-Path([string]$Path, [string]$What) {
    if (-not (Test-Path $Path)) {
        throw "$What not found at '$Path'"
    }
}

Assert-Path $DeployCaPem "deploy CA PEM"
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$StartNet = Join-Path $RepoRoot "winpe\scripts\startnet.cmd"
Assert-Path $StartNet "startnet.cmd"

$PeRoot = Join-Path $AdkRoot "Windows Preinstallation Environment"
$OcDir = Join-Path $PeRoot "$Arch\WinPE_OCs"
Assert-Path $OcDir "WinPE optional components (is the WinPE add-on installed?)"

# --- 1. copype -------------------------------------------------------
if (Test-Path $WorkDir) {
    Write-Host "==> removing stale work dir $WorkDir"
    # Unmount a leftover mount first or Remove-Item will fail.
    & dism /Unmount-Image /MountDir:(Join-Path $WorkDir "mount") /Discard 2>$null | Out-Null
    Remove-Item -Recurse -Force $WorkDir
}
Write-Host "==> copype $Arch $WorkDir"
& copype $Arch $WorkDir
if ($LASTEXITCODE -ne 0) { throw "copype failed ($LASTEXITCODE)" }

$BootWim = Join-Path $WorkDir "media\sources\boot.wim"
$Mount = Join-Path $WorkDir "mount"
New-Item -ItemType Directory -Force $Mount | Out-Null

# --- 2. mount --------------------------------------------------------
Write-Host "==> mounting $BootWim"
& dism /Mount-Image /ImageFile:$BootWim /Index:1 /MountDir:$Mount
if ($LASTEXITCODE -ne 0) { throw "mount failed ($LASTEXITCODE)" }

try {
    # --- 3. optional components (order matters: base packages before
    #        their language packs; each OC before anything depending on it)
    $Components = @(
        "WinPE-WMI",
        "WinPE-NetFx",
        "WinPE-Scripting",
        "WinPE-PowerShell",
        "WinPE-SecureStartup",
        "WinPE-EnhancedStorage"
    )
    foreach ($c in $Components) {
        $cab = Join-Path $OcDir "$c.cab"
        Assert-Path $cab "optional component $c"
        Write-Host "==> adding $c"
        & dism /Image:$Mount /Add-Package /PackagePath:$cab
        if ($LASTEXITCODE -ne 0) { throw "Add-Package $c failed ($LASTEXITCODE)" }
        $lang = Join-Path $OcDir "en-us\$c`_en-us.cab"
        if (Test-Path $lang) {
            & dism /Image:$Mount /Add-Package /PackagePath:$lang
            if ($LASTEXITCODE -ne 0) { throw "Add-Package $c lang failed ($LASTEXITCODE)" }
        }
    }

    # --- 4. deploy assets -------------------------------------------
    Write-Host "==> installing startnet.cmd + pinned CA"
    Copy-Item $StartNet (Join-Path $Mount "Windows\System32\startnet.cmd") -Force
    $DeployDir = Join-Path $Mount "deploy"
    New-Item -ItemType Directory -Force $DeployDir | Out-Null
    Copy-Item $DeployCaPem (Join-Path $DeployDir "deploy-ca.pem") -Force

    # --- 5. scratch space (driver packs unpack here) -----------------
    & dism /Image:$Mount /Set-ScratchSpace:512
    if ($LASTEXITCODE -ne 0) { throw "Set-ScratchSpace failed ($LASTEXITCODE)" }

    # --- 6. commit ---------------------------------------------------
    Write-Host "==> committing"
    & dism /Unmount-Image /MountDir:$Mount /Commit
    if ($LASTEXITCODE -ne 0) { throw "unmount/commit failed ($LASTEXITCODE)" }
}
catch {
    Write-Warning "build failed: $_ — discarding mount"
    & dism /Unmount-Image /MountDir:$Mount /Discard 2>$null | Out-Null
    throw
}

# Export to squeeze out superseded component payloads.
Write-Host "==> exporting to $OutWim"
New-Item -ItemType Directory -Force (Split-Path $OutWim) | Out-Null
if (Test-Path $OutWim) { Remove-Item -Force $OutWim }
& dism /Export-Image /SourceImageFile:$BootWim /SourceIndex:1 /DestinationImageFile:$OutWim /Compress:max
if ($LASTEXITCODE -ne 0) { throw "export failed ($LASTEXITCODE)" }

$sha = (Get-FileHash -Algorithm SHA256 $OutWim).Hash.ToLower()
Write-Host ""
Write-Host "==> done"
Write-Host "    boot.wim: $OutWim"
Write-Host "    sha256:   $sha"
Write-Host ""
Write-Host "Upload it as an image version (deployctl images upload) or host it"
Write-Host "at /static/winpe/boot.wim and set images.media.bootwim_url."
