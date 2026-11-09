param(
    [Parameter(Mandatory)][string]$BinaryPath,
    [Parameter(Mandatory)][string]$ConfigPath,
    [Parameter(Mandatory)][string]$DataDir,
    [string]$TaskName = 'RainClassReminder',
    [DateTimeOffset]$NotBefore = ([DateTimeOffset](Get-Date).Date.AddDays(1)),
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'RainClassReminder'),
    [switch]$PrepareOnly
)

$ErrorActionPreference = 'Stop'
foreach ($path in @($BinaryPath, $ConfigPath, $DataDir)) {
    if (-not (Test-Path -LiteralPath $path)) { throw 'An installation input path is missing.' }
}
$BinaryPath = (Resolve-Path -LiteralPath $BinaryPath).Path
$ConfigPath = (Resolve-Path -LiteralPath $ConfigPath).Path
$DataDir = (Resolve-Path -LiteralPath $DataDir).Path
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$acl = New-Object Security.AccessControl.DirectorySecurity
$acl.SetAccessRuleProtection($true, $false)
foreach ($sid in @($identity.User, [Security.Principal.SecurityIdentifier]'S-1-5-18')) {
    $rule = New-Object Security.AccessControl.FileSystemAccessRule($sid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
    $acl.AddAccessRule($rule)
}
Set-Acl -LiteralPath $InstallDir -AclObject $acl

$backup = Join-Path $InstallDir ('rollback/' + (Get-Date -Format 'yyyyMMdd-HHmmss-ffff'))
New-Item -ItemType Directory -Path $backup -Force | Out-Null
foreach ($name in @('settings.json', 'start-observed.ps1', 'launch-hidden.vbs')) {
    $existingFile = Join-Path $InstallDir $name
    if (Test-Path -LiteralPath $existingFile) { Copy-Item -LiteralPath $existingFile -Destination $backup }
}
$existingTask = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existingTask) {
    Export-ScheduledTask -TaskName $TaskName | Set-Content -LiteralPath (Join-Path $backup 'task.xml') -Encoding Unicode
    foreach ($oldAction in $existingTask.Actions) {
        if (($oldAction.Execute -match '(?i)(?:^|[\\/])(?:wscript|cscript)(?:\.exe)?$') -and
            ($oldAction.Arguments -match '^"([^"]+\.vbs)"$') -and (Test-Path -LiteralPath $Matches[1])) {
            Copy-Item -LiteralPath $Matches[1] -Destination (Join-Path $backup 'original-launcher.vbs')
        }
    }
}

$hash = (Get-FileHash -LiteralPath $BinaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
$installedBinary = Join-Path $InstallDir ("rainclass-reminder-" + $hash.Substring(0, 12) + '.exe')
if (-not (Test-Path -LiteralPath $installedBinary)) { Copy-Item -LiteralPath $BinaryPath -Destination $installedBinary }
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'start-observed.ps1') -Destination (Join-Path $InstallDir 'start-observed.ps1') -Force
$settingsPath = Join-Path $InstallDir 'settings.json'
@{
    binary = $installedBinary
    binary_sha256 = $hash
    config = $ConfigPath
    data_dir = $DataDir
    observation_dir = (Join-Path $InstallDir 'observations')
    observation_enabled = $true
    not_before = $NotBefore.ToString('o')
} | ConvertTo-Json | Set-Content -LiteralPath $settingsPath -Encoding UTF8

$powershell = Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe'
$wrapper = Join-Path $InstallDir 'start-observed.ps1'
$arguments = '-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $wrapper + '" -SettingsPath "' + $settingsPath + '"'

if (-not $PrepareOnly) {
    $action = New-ScheduledTaskAction -Execute $powershell -Argument $arguments -WorkingDirectory (Split-Path -Parent $powershell)
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity.Name
    $trigger.StartBoundary = $NotBefore.ToString('yyyy-MM-ddTHH:mm:sszzz')
    $trigger.Delay = 'PT30S'
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -MultipleInstances IgnoreNew -RestartInterval (New-TimeSpan -Minutes 5) -RestartCount 3 -ExecutionTimeLimit ([TimeSpan]::Zero)
    $principal = New-ScheduledTaskPrincipal -UserId $identity.Name -LogonType Interactive -RunLevel Limited
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Description 'RainClass Reminder: passive observation on next login' -Force | Out-Null
}
Write-Output ("Prepared {0}; earliest start {1}; no process started." -f $TaskName, $NotBefore.ToString('o'))
