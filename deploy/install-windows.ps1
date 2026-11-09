# 在 Windows 上注册开机自启（计划任务）
#
# 用法（普通用户权限即可，不需要管理员）：
#   powershell -ExecutionPolicy Bypass -File deploy\install-windows.ps1
#
# 卸载：
#   powershell -ExecutionPolicy Bypass -File deploy\install-windows.ps1 -Uninstall

param(
    [string]$BinaryPath = "",
    [string]$TaskName = "RainClassReminder",
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"

if ($Uninstall) {
    $existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    if ($existing) {
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
        Write-Host "已移除计划任务 $TaskName"
    } else {
        Write-Host "未找到计划任务 $TaskName"
    }
    $launcher = Join-Path $env:APPDATA "rainclass-reminder\launch-hidden.vbs"
    if (Test-Path $launcher) {
        Remove-Item $launcher -Force
        Write-Host "已移除隐藏启动器 $launcher"
    }
    return
}

# 未指定时，默认使用脚本上一级目录里的 rainclass-reminder.exe
if (-not $BinaryPath) {
    $root = Split-Path -Parent $PSScriptRoot
    $BinaryPath = Join-Path $root "rainclass-reminder.exe"
}

if (-not (Test-Path $BinaryPath)) {
    throw "找不到可执行文件：$BinaryPath`n请先编译，或显式传 -BinaryPath。"
}

$BinaryPath = (Resolve-Path $BinaryPath).Path
Write-Host "可执行文件：$BinaryPath"

# 生成隐藏启动器。
#
# Go 编译的是控制台程序，由计划任务直接执行会弹出黑窗口。计划任务没有
# "隐藏控制台"开关（只有"不管用户是否登录"才隐藏，但那样拿不到交互式
# 桌面会话，桌面通知就发不出去），因此用 WScript 以窗口模式 0 拉起子进程。
$launcher = Join-Path $env:APPDATA "rainclass-reminder\launch-hidden.vbs"
$launcherDir = Split-Path -Parent $launcher
if (-not (Test-Path $launcherDir)) {
    New-Item -ItemType Directory -Path $launcherDir -Force | Out-Null
}

# 0 = 隐藏窗口，False = 不等待子进程退出（计划任务不会以为程序秒退）
#
# 注意编码：必须用 Unicode(UTF-16LE)。WScript 能正确识别，
# 而 ASCII 会把中文路径写成问号，
# 结果就是静默启动失败、日志里什么都没有。
$vbs = @"
Set sh = CreateObject("WScript.Shell")
sh.Run """$BinaryPath"" run", 0, False
"@
Set-Content -Path $launcher -Value $vbs -Encoding Unicode
Write-Host "隐藏启动器：$launcher"

$action = New-ScheduledTaskAction `
    -Execute "wscript.exe" `
    -Argument "`"$launcher`""

# 登录时启动（不需要管理员权限），并略延迟以等待网络就绪。
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$trigger.Delay = "PT30S"

$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -RestartInterval (New-TimeSpan -Minutes 1) `
    -RestartCount 999 `
    -ExecutionTimeLimit ([TimeSpan]::Zero)

$principal = New-ScheduledTaskPrincipal `
    -UserId "$env:USERDOMAIN\$env:USERNAME" `
    -LogonType Interactive `
    -RunLevel Limited

Register-ScheduledTask `
    -TaskName $TaskName `
    -Action $action `
    -Trigger $trigger `
    -Settings $settings `
    -Principal $principal `
    -Description "长江雨课堂截止提醒助手（后台常驻，登录时自动启动）" `
    -Force | Out-Null

Write-Host ""
Write-Host "已注册计划任务：$TaskName"
Write-Host "  - 触发：登录后 30 秒"
Write-Host "  - 崩溃自动重启（最多每 1 分钟一次）"
Write-Host ""
Write-Host "立即启动：Start-ScheduledTask -TaskName $TaskName"
Write-Host "查看状态：Get-ScheduledTask -TaskName $TaskName | Get-ScheduledTaskInfo"
Write-Host "移除自启：powershell -File deploy\install-windows.ps1 -Uninstall"
Write-Host ""
Write-Host "提示：首次使用请先完成登录："
Write-Host "  $BinaryPath auth login"
