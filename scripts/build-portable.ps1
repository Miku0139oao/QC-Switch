[CmdletBinding()]
param(
    [string]$Flutter = 'flutter',
    [switch]$SkipChecks,
    [switch]$SkipFlutterBuild
)
$ErrorActionPreference = 'Stop'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location $repoRoot
try {
    function Invoke-Checked([string]$Tool, [string[]]$Arguments) {
        & $Tool @Arguments
        if ($LASTEXITCODE -ne 0) { throw "$Tool exited with $LASTEXITCODE" }
    }
    $versionMatch = [regex]::Match((Get-Content desktop/pubspec.yaml -Raw), '(?m)^version:\s*([^+\s]+)')
    if (-not $versionMatch.Success) { throw 'Missing application version' }
    $version = $versionMatch.Groups[1].Value
    Invoke-Checked go @('mod', 'download')
    Push-Location desktop
    try { Invoke-Checked $Flutter @('pub', 'get', '--enforce-lockfile') } finally { Pop-Location }
    if (-not $SkipChecks) {
        Invoke-Checked go @('test', './...')
        Invoke-Checked go @('vet', './...')
        Push-Location desktop
        try {
            Invoke-Checked $Flutter @('analyze')
            Invoke-Checked $Flutter @('test')
        } finally { Pop-Location }
    }
    if (-not $SkipFlutterBuild) {
        Push-Location desktop
        try { Invoke-Checked $Flutter @('build', 'windows', '--release') } finally { Pop-Location }
    }
    $releaseDir = Join-Path $repoRoot 'desktop/build/windows/x64/runner/Release'
    if (-not (Test-Path (Join-Path $releaseDir 'qc_switch.exe'))) { throw 'Flutter Windows release is missing' }
    $stageName = "QC-Switch-$version-windows-x64-portable"
    $stageDir = [IO.Path]::GetFullPath((Join-Path $repoRoot "build/portable/$stageName"))
    $allowedRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot 'build/portable')) + [IO.Path]::DirectorySeparatorChar
    if (-not $stageDir.StartsWith($allowedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Invalid staging directory' }
    if (Test-Path -LiteralPath $stageDir) { Remove-Item -LiteralPath $stageDir -Recurse -Force }
    New-Item -ItemType Directory -Path $stageDir -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $releaseDir 'qc_switch.exe') -Destination (Join-Path $stageDir 'QC Switch.exe')
    Get-ChildItem -LiteralPath $releaseDir -Filter '*.dll' | Copy-Item -Destination $stageDir
    Copy-Item -LiteralPath (Join-Path $releaseDir 'data') -Destination $stageDir -Recurse
    Invoke-Checked go @('build', '-trimpath', '-ldflags=-s -w', '-o', (Join-Path $stageDir 'qc-switch-core.exe'), './cmd/qc-switch-core')

    # App-local VC runtime: portable use does not run an installer.
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    if (-not (Test-Path $vswhere)) { throw 'vswhere.exe is required to locate the licensed VC redistributables' }
    $vsRoot = (& $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath | Select-Object -First 1)
    if (-not $vsRoot) { throw 'Visual Studio C++ toolchain not found' }
    $redist = Get-ChildItem (Join-Path $vsRoot 'VC/Redist/MSVC') -Directory |
        Where-Object Name -Match '^\d+\.' | Sort-Object { [version]$_.Name } -Descending | Select-Object -First 1
    $crtDir = Get-ChildItem (Join-Path $redist.FullName 'x64') -Directory -Filter 'Microsoft.VC*.CRT' | Select-Object -First 1
    if (-not $crtDir) { throw 'VC x64 redistributable DLLs not found' }
    Get-ChildItem $crtDir.FullName -Filter '*.dll' | Copy-Item -Destination $stageDir

    $licenseDir = Join-Path $stageDir 'licenses'
    New-Item -ItemType Directory $licenseDir -Force | Out-Null
    function Copy-Notices([string]$Source, [string]$Name) {
        $target = Join-Path $licenseDir $Name
        New-Item -ItemType Directory $target -Force | Out-Null
        Get-ChildItem -LiteralPath $Source -File | Where-Object Name -Match '^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS|AUTHORS)(\..*)?$' |
            Copy-Item -Destination $target
        if (-not (Get-ChildItem $target -File)) { throw "Missing dependency license: $Name ($Source)" }
    }
    $goRoot = (& go env GOROOT).Trim()
    Copy-Notices $goRoot 'go-runtime'
    $modules = & go list -m -f '{{.Path}}|{{.Version}}|{{.Dir}}' all
    if ($LASTEXITCODE -ne 0) { throw 'Cannot enumerate Go dependencies' }
    $modules | Set-Content (Join-Path $licenseDir 'go-modules.txt') -Encoding utf8
    foreach ($line in $modules) {
        $parts = $line.Split('|')
        if ($parts[1]) { Copy-Notices $parts[2] ('go-' + ($parts[0] -replace '[/\\]', '_') + '-' + $parts[1]) }
    }
    $configPath = Join-Path $repoRoot 'desktop/.dart_tool/package_config.json'
    $packages = (Get-Content $configPath -Raw | ConvertFrom-Json).packages
    $flutterPackage = $packages | Where-Object name -EQ 'flutter'
    $flutterRoot = [IO.Path]::GetFullPath((Join-Path ([uri]::new([uri]::new($configPath), $flutterPackage.rootUri).LocalPath) '../..'))
    foreach ($package in $packages) {
        if ($package.name -eq 'qc_switch') { continue }
        $uri = [uri]::new([uri]::new($configPath), $package.rootUri)
        $packageRoot = $uri.LocalPath
        if ($packageRoot.StartsWith((Join-Path $flutterRoot 'packages'), [StringComparison]::OrdinalIgnoreCase) -and
            -not (Get-ChildItem -LiteralPath $packageRoot -File | Where-Object Name -Match '^(LICENSE|LICENCE|COPYING)(\..*)?$')) {
            # SDK packages share the Flutter repository's BSD license.
            $packageRoot = $flutterRoot
        }
        Copy-Notices $packageRoot ('dart-' + $package.name)
    }
    Copy-Notices $flutterRoot 'flutter-sdk'
    Copy-Notices (Join-Path $flutterRoot 'bin/cache/dart-sdk') 'dart-sdk'
    $msNotices = Get-ChildItem (Join-Path $vsRoot 'Licenses') -Recurse -File | Where-Object Name -Match '^(Redist\.txt|.*EULA.*\.(rtf|txt))$'
    $msTarget = Join-Path $licenseDir 'microsoft-visual-cpp'
    New-Item -ItemType Directory $msTarget -Force | Out-Null
    foreach ($notice in $msNotices) {
        Copy-Item -LiteralPath $notice.FullName -Destination (Join-Path $msTarget ($notice.Directory.Name + '-' + $notice.Name))
    }
    if (-not (Get-ChildItem $msTarget -File)) { throw 'Microsoft redistributable notices missing' }
    foreach ($file in @('README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md')) {
        Copy-Item -LiteralPath (Join-Path $repoRoot $file) -Destination $stageDir
    }
    Copy-Item -LiteralPath (Join-Path $repoRoot 'docs') -Destination $stageDir -Recurse
    @{
        version = $version
        architecture = 'windows-x64'
        go = (& go version)
        flutter = ((& $Flutter --version --machine | ConvertFrom-Json).frameworkVersion)
        gitCommit = (& git rev-parse HEAD 2>$null)
    } | ConvertTo-Json | Set-Content (Join-Path $stageDir 'build-info.json') -Encoding utf8
    New-Item -ItemType Directory (Join-Path $repoRoot 'dist') -Force | Out-Null
    $zipPath = Join-Path $repoRoot "dist/$stageName.zip"
    Compress-Archive -Path $stageDir -DestinationPath $zipPath -CompressionLevel Optimal -Force
    $hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $stageName.zip" | Set-Content "$zipPath.sha256" -Encoding ascii
    Write-Host "Portable ZIP: $zipPath"
    Write-Host "SHA256: $hash"
} finally { Pop-Location }
