param([Parameter(Mandatory)][string]$SettingsPath)

$ErrorActionPreference = 'Stop'
$settings = Get-Content -LiteralPath $SettingsPath -Raw -Encoding UTF8 | ConvertFrom-Json
$notBefore = [DateTimeOffset]::Parse($settings.not_before)
if ([DateTimeOffset]::Now -lt $notBefore) { exit 0 }

foreach ($path in @($settings.binary, $settings.config, $settings.data_dir)) {
    if (-not (Test-Path -LiteralPath $path)) { throw 'A configured startup path is missing.' }
}
$env:YKT_DATA_DIR = $settings.data_dir
if ($settings.observation_enabled -ne $false) {
    $env:YKT_OBSERVE_FROM = $settings.not_before
    $env:YKT_OBSERVE_DIR = $settings.observation_dir
} else {
    Remove-Item Env:YKT_OBSERVE_FROM -ErrorAction SilentlyContinue
    Remove-Item Env:YKT_OBSERVE_DIR -ErrorAction SilentlyContinue
}
Set-Location -LiteralPath (Split-Path -Parent $settings.binary)

$startupLog = Join-Path (Split-Path -Parent $SettingsPath) 'startup.log'
if ((Test-Path -LiteralPath $startupLog) -and (Get-Item -LiteralPath $startupLog).Length -gt 8MB) {
    Move-Item -LiteralPath $startupLog -Destination "$startupLog.1" -Force
}
$ErrorActionPreference = 'Continue'
& $settings.binary --config $settings.config run >> $startupLog 2>&1
$result = $LASTEXITCODE
exit $result
