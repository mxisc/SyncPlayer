param([string]$Version = '1.0.0')
$ErrorActionPreference = 'Stop'
$repoRoot = $PSScriptRoot
$payload = Join-Path $repoRoot 'cmd/portable/payload.zip'
$icon = Join-Path $repoRoot 'cmd/portable/resources_windows_amd64.syso'
$output = Join-Path $repoRoot "dist/SyncPlayer-$Version-windows-x64.exe"
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw 'Expected a numeric release version.' }
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$files = @(
 'build/bin/SyncPlayer.exe',
 'runtime/mpv/mpv.exe', 'runtime/mpv/d3dcompiler_43.dll',
 'runtime/ffmpeg/ffmpeg.exe', 'runtime/ffmpeg/ffprobe.exe',
 'runtime/ffmpeg/LICENSE', 'runtime/ffmpeg/README.txt',
 'runtime/whisper/whisper-cli.exe', 'runtime/whisper/whisper.dll',
 'runtime/whisper/ggml.dll', 'runtime/whisper/ggml-base.dll',
 'runtime/whisper/LICENSE',
 'runtime/models/ggml-small.bin', 'runtime/models/LICENSE-whisper.txt',
 'runtime/licenses/MPV-Copyright.txt', 'runtime/licenses/MPV-LICENSE.GPL',
 'NATIVE-DEPENDENCIES.md'
)
$files += @(Get-ChildItem (Join-Path $repoRoot 'runtime/whisper/ggml-cpu-*.dll') | ForEach-Object { 'runtime/whisper/' + $_.Name })
foreach ($relative in $files) {
 if (-not (Test-Path -LiteralPath (Join-Path $repoRoot $relative) -PathType Leaf)) { throw "Missing dependency: $relative" }
}
$crt = @('msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll')
foreach ($name in $crt) {
 if (-not (Test-Path -LiteralPath (Join-Path $env:WINDIR "System32/$name"))) { throw "Missing x64 Visual C++ runtime: $name" }
}
New-Item -ItemType Directory -Force (Join-Path $repoRoot 'dist') | Out-Null
Push-Location $repoRoot
try {
 $stream = [IO.File]::Open($payload, [IO.FileMode]::Create)
 $zip = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Create)
 try {
  foreach ($relative in $files) {
   [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $repoRoot $relative), $relative, [IO.Compression.CompressionLevel]::Optimal) | Out-Null
  }
  foreach ($name in $crt) {
   [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $env:WINDIR "System32/$name"), "runtime/whisper/$name", [IO.Compression.CompressionLevel]::Optimal) | Out-Null
  }
 } finally { $zip.Dispose() }
 $v = $Version.Split('.')
 go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0 -64 -icon assets/branding/syncplayer-icon.ico -ver-major $v[0] -ver-minor $v[1] -ver-patch $v[2] -product-ver-major $v[0] -product-ver-minor $v[1] -product-ver-patch $v[2] -file-version $Version -product-version $Version -o $icon cmd/portable/versioninfo.json
 if ($LASTEXITCODE -ne 0) { throw 'Version resource generation failed' }
 go build -trimpath -tags portable -ldflags "-H windowsgui -s -w -X main.version=$Version" -o $output ./cmd/portable
 if ($LASTEXITCODE -ne 0) { throw 'Single-file build failed' }
 $hash = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
 "$hash  $([IO.Path]::GetFileName($output))" | Set-Content -LiteralPath (Join-Path $repoRoot 'dist/SHA256SUMS.txt') -Encoding ascii
 Write-Output "Release: $output"
} finally {
 Pop-Location
}
