$ErrorActionPreference = "Stop"

$repo = "ericbryant24/serve"
$bin = "serve"
$installDir = "$HOME\.local\bin"

# Detect arch
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

# Get latest release version
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$version = $release.tag_name
$versionNum = $version.TrimStart("v")

$url = "https://github.com/$repo/releases/download/$version/${bin}_${versionNum}_windows_${arch}.zip"

Write-Host "Downloading $bin $version (windows/$arch)..."

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) "serve-install"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

$zipPath = Join-Path $tmp "$bin.zip"
Invoke-WebRequest -Uri $url -OutFile $zipPath
Expand-Archive -Path $zipPath -DestinationPath $tmp -Force

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
Copy-Item -Path "$tmp\$bin.exe" -Destination "$installDir\$bin.exe" -Force

Remove-Item $tmp -Recurse -Force

Write-Host "Installed to $installDir\$bin.exe"

if ($env:PATH -notlike "*$installDir*") {
    Write-Host "Note: add $installDir to your PATH to use $bin from anywhere"
}
