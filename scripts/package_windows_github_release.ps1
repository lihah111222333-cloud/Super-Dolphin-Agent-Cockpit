[CmdletBinding()]
param(
    [string]$StageDir = $(if ($env:SUPER_DOLPHIN_RELEASE_STAGE_DIR) { $env:SUPER_DOLPHIN_RELEASE_STAGE_DIR } else { '' })
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

throw 'Windows package-owned update publishing is unsupported; check/install/publish capabilities are all disabled'
