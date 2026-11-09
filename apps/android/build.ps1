param(
    [ValidateSet('Debug', 'Release')][string]$Variant = 'Debug',
    [string]$Sdk = $env:ANDROID_HOME,
    [string]$Go = 'go',
    [string]$JavaHome = $env:JAVA_HOME,
    [string]$Stage = (Join-Path ([System.IO.Path]::GetTempPath()) 'rainclass-android-build'),
    [switch]$Test,
    [switch]$SkipBinding
)

$ErrorActionPreference = 'Stop'
$repository = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$stagePath = [System.IO.Path]::GetFullPath($Stage)
if ($stagePath -match '[^\x00-\x7F]') { throw 'Stage must use an ASCII path for Android native tools.' }
if ($stagePath.StartsWith($repository, [StringComparison]::OrdinalIgnoreCase)) { throw 'Stage must be outside the repository.' }
if (!$Sdk -or !(Test-Path -LiteralPath (Join-Path $Sdk 'platforms/android-35/android.jar'))) { throw 'Android SDK platform 35 is required; set ANDROID_HOME or -Sdk.' }
if (!$JavaHome -or !(Test-Path -LiteralPath (Join-Path $JavaHome 'bin/javac.exe'))) {
    if (!$JavaHome -or !(Test-Path -LiteralPath (Join-Path $JavaHome 'bin/javac'))) { throw 'JDK 17 is required; set JAVA_HOME or -JavaHome.' }
}
$goCommand = (Get-Command $Go -ErrorAction Stop).Source
if ($Variant -eq 'Release') {
    foreach ($variable in @('YKT_ANDROID_KEYSTORE', 'YKT_ANDROID_STORE_PASSWORD', 'YKT_ANDROID_KEY_ALIAS', 'YKT_ANDROID_KEY_PASSWORD')) {
        if (![Environment]::GetEnvironmentVariable($variable)) { throw "Release signing requires $variable." }
    }
}
$env:JAVA_HOME = $JavaHome
$env:ANDROID_HOME = $Sdk
$env:ANDROID_NDK_HOME = Join-Path $Sdk 'ndk/28.2.13676358'
$env:PATH = (Split-Path $goCommand) + [IO.Path]::PathSeparator + (Join-Path $JavaHome 'bin') + [IO.Path]::PathSeparator + $env:PATH
$env:GOTOOLCHAIN = 'local'

function Invoke-Checked {
    param([string]$Executable, [string[]]$Arguments)
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Build command failed: $Executable (exit $LASTEXITCODE)" }
}

function Remove-StageDirectory {
    param([string]$Path)
    $resolved = [System.IO.Path]::GetFullPath($Path)
    $prefix = $stagePath.TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (!$resolved.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing to remove a path outside Stage.' }
    if (Test-Path -LiteralPath $resolved) { Remove-Item -LiteralPath $resolved -Recurse -Force }
}

New-Item -ItemType Directory -Path $stagePath -Force | Out-Null
$marker = Join-Path $stagePath '.rainclass-build-stage'
if ((Get-ChildItem -LiteralPath $stagePath -Force).Count -gt 0 -and !(Test-Path -LiteralPath $marker)) {
    throw 'Stage is not an existing RainClass build directory. Choose an empty directory.'
}
Set-Content -LiteralPath $marker -Value 'rainclass-reminder Android build' -Encoding utf8

if (!$SkipBinding) {
    $bindingStage = Join-Path $stagePath 'source'
    New-Item -ItemType Directory -Path $bindingStage -Force | Out-Null
    # Go/NDK 编译也使用 ASCII 路径，避免源码所在用户目录或仓库名影响工具。
    foreach ($directory in @('internal', 'bindings')) {
        $target = Join-Path $bindingStage $directory
        Remove-StageDirectory $target
        Copy-Item -LiteralPath (Join-Path $repository $directory) -Destination $target -Recurse
    }
    Copy-Item -LiteralPath (Join-Path $repository 'go.mod'), (Join-Path $repository 'go.sum') -Destination $bindingStage
    $env:GOBIN = Join-Path $stagePath 'go-bin'
    $env:PATH = $env:GOBIN + [IO.Path]::PathSeparator + $env:PATH
    New-Item -ItemType Directory -Path $env:GOBIN -Force | Out-Null
    Push-Location (Join-Path $bindingStage 'bindings/mobile')
    try {
        $mobileVersion = 'v0.0.0-20261011034528-197770444867'
        Invoke-Checked $goCommand @('install', "golang.org/x/mobile/cmd/gomobile@$mobileVersion")
        Invoke-Checked $goCommand @('install', "golang.org/x/mobile/cmd/gobind@$mobileVersion")
        $mobile = Join-Path $env:GOBIN ('gomobile' + $(if ($IsWindows -or $env:OS -eq 'Windows_NT') { '.exe' } else { '' }))
        Invoke-Checked $mobile @('init')
        $aar = Join-Path $stagePath 'raincore.aar'
        Invoke-Checked $mobile @('bind', '-target=android/arm64,android/amd64', '-androidapi=26', '-trimpath', '-ldflags=-s -w', '-o', $aar, '.')
        $libraryDirectory = Join-Path $PSScriptRoot 'app/libs'
        New-Item -ItemType Directory -Path $libraryDirectory -Force | Out-Null
        Copy-Item -LiteralPath $aar -Destination $libraryDirectory -Force
    } finally { Pop-Location }
}

$androidStage = Join-Path $stagePath 'android'
New-Item -ItemType Directory -Path $androidStage -Force | Out-Null
foreach ($directory in @('app/src', 'app/libs', 'gradle')) {
    $target = Join-Path $androidStage $directory
    Remove-StageDirectory $target
    New-Item -ItemType Directory -Path (Split-Path $target) -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $directory) -Destination $target -Recurse
}
foreach ($file in @('settings.gradle.kts', 'build.gradle.kts', 'gradle.properties', 'gradlew', 'gradlew.bat', 'app/build.gradle.kts')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination (Join-Path $androidStage $file) -Force
}
Push-Location $androidStage
try {
    $wrapper = if ($IsWindows -or $env:OS -eq 'Windows_NT') { Join-Path $androidStage 'gradlew.bat' } else { Join-Path $androidStage 'gradlew' }
    $tasks = @('--no-daemon', "assemble$Variant", "lint$Variant")
    if ($Test) { $tasks += 'connectedDebugAndroidTest' }
    Invoke-Checked $wrapper $tasks
    $output = Join-Path $repository 'dist/android'
    New-Item -ItemType Directory -Path $output -Force | Out-Null
    $apkDirectory = Join-Path $androidStage "app/build/outputs/apk/$($Variant.ToLowerInvariant())"
    $metadata = Get-Content -LiteralPath (Join-Path $apkDirectory 'output-metadata.json') -Raw | ConvertFrom-Json
    $artifacts = @(Get-ChildItem -LiteralPath $apkDirectory -Filter '*.apk' | ForEach-Object {
        Copy-Item -LiteralPath $_.FullName -Destination $output -Force
        $copy = Join-Path $output $_.Name
        $hash = (Get-FileHash -LiteralPath $copy -Algorithm SHA256).Hash.ToLowerInvariant()
        Set-Content -LiteralPath "$copy.sha256" -Value "$hash  $($_.Name)" -Encoding ascii
        $element = $metadata.elements | Where-Object outputFile -EQ $_.Name | Select-Object -First 1
        @{ file = $_.Name; sha256 = $hash; versionName = $element.versionName; versionCode = $element.versionCode }
    })
    $revision = 'unknown'
    $dirty = $true
    if (Test-Path -LiteralPath (Join-Path $repository '.git')) {
        $revision = (& git -C $repository rev-parse HEAD).Trim()
        $dirty = @(& git -C $repository status --porcelain).Count -gt 0
    }
    @{
        applicationId = $metadata.applicationId
        variant = $Variant.ToLowerInvariant()
        sourceCommit = $revision
        sourceDirty = $dirty
        goVersion = (& $goCommand version).Trim()
        artifacts = $artifacts
    } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $output "build-info-$($Variant.ToLowerInvariant()).json") -Encoding utf8
    foreach ($artifact in $artifacts) { Write-Output (Join-Path $output $artifact.file) }
} finally { Pop-Location }
