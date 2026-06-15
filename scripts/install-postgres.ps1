#Requires -Version 5.1
<#
Retired: Super Dolphin no longer uses a local PostgreSQL onboarding flow.
Current local setup uses SQLite; see .env.example and README.md.
#>

[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

throw "scripts/install-postgres.ps1 is retired. Super Dolphin uses SQLite for local setup; configure SUPER_DOLPHIN_HOME or SUPER_DOLPHIN_SQLITE_PATH instead."
