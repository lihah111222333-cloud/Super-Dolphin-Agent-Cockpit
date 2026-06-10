param(
    [ValidateSet('start', 'stop', 'restart', 'status', 'logs', 'down', 'pull', 'urls')]
    [string]$Action = 'start',

    [string]$StackVersion = $env:ELASTIC_STACK_VERSION
)

$ErrorActionPreference = 'Stop'

$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$ComposeFile = Join-Path $RepoRoot 'deploy\elk\docker-compose.yml'
$ProjectName = 'super-dolphin-elk'

if ([string]::IsNullOrWhiteSpace($StackVersion)) {
    $StackVersion = '9.4.2'
}
$env:ELASTIC_STACK_VERSION = $StackVersion

function Invoke-ElasticCompose {
    param([string[]]$Arguments)

    & docker compose --project-name $ProjectName -f $ComposeFile @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose failed: $($Arguments -join ' ')"
    }
}

function Ensure-LogDirectories {
    New-Item -ItemType Directory -Force -Path (Join-Path $RepoRoot '.tmp') | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $RepoRoot '.tmp\run-new-ui-desktop') | Out-Null
}

function Write-LocalUrls {
    Write-Host 'Elasticsearch: http://127.0.0.1:9200'
    Write-Host 'Kibana:        http://127.0.0.1:5601'
    Write-Host 'Index pattern: super-dolphin-logs-*'
    Write-Host 'Log source:    .tmp/**/*.log'
}

switch ($Action) {
    'start' {
        Ensure-LogDirectories
        Invoke-ElasticCompose @('up', '-d')
        Write-LocalUrls
        Invoke-ElasticCompose @('ps')
    }
    'stop' {
        Invoke-ElasticCompose @('stop')
    }
    'restart' {
        Ensure-LogDirectories
        Invoke-ElasticCompose @('restart')
        Write-LocalUrls
        Invoke-ElasticCompose @('ps')
    }
    'status' {
        Invoke-ElasticCompose @('ps')
    }
    'logs' {
        Invoke-ElasticCompose @('logs', '-f', '--tail', '120')
    }
    'down' {
        Invoke-ElasticCompose @('down')
    }
    'pull' {
        Invoke-ElasticCompose @('pull')
    }
    'urls' {
        Write-LocalUrls
    }
}
