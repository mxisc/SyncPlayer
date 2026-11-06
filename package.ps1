param([string]$Version = '1.0.0')
$ErrorActionPreference = 'Stop'
$repoRoot = $PSScriptRoot
$releaseName = "SyncPlayer-$Version-windows-x64"
$output = Join-Path $repoRoot "dist/$releaseName.zip"
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw 'Expected a numeric release version.' }
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

$files = [ordered]@{
 'build/bin/SyncPlayer.exe' = 'SyncPlayer.exe'
 'runtime/mpv/mpv.exe' = 'runtime/mpv/mpv.exe'
 'runtime/mpv/d3dcompiler_43.dll' = 'runtime/mpv/d3dcompiler_43.dll'
 'runtime/ffmpeg/ffmpeg.exe' = 'runtime/ffmpeg/ffmpeg.exe'
 'runtime/ffmpeg/ffprobe.exe' = 'runtime/ffmpeg/ffprobe.exe'
 'runtime/ffmpeg/LICENSE' = 'runtime/ffmpeg/LICENSE'
 'runtime/ffmpeg/README.txt' = 'runtime/ffmpeg/README.txt'
 'runtime/whisper/whisper-cli.exe' = 'runtime/whisper/whisper-cli.exe'
 'runtime/whisper/whisper.dll' = 'runtime/whisper/whisper.dll'
 'runtime/whisper/ggml.dll' = 'runtime/whisper/ggml.dll'
 'runtime/whisper/ggml-base.dll' = 'runtime/whisper/ggml-base.dll'
 'runtime/whisper/LICENSE' = 'runtime/whisper/LICENSE'
 'runtime/models/ggml-small.bin' = 'runtime/models/ggml-small.bin'
 'runtime/models/LICENSE-whisper.txt' = 'runtime/models/LICENSE-whisper.txt'
 'runtime/licenses/MPV-Copyright.txt' = 'runtime/licenses/MPV-Copyright.txt'
 'runtime/licenses/MPV-LICENSE.GPL' = 'runtime/licenses/MPV-LICENSE.GPL'
 'README.md' = 'README.md'
 'NATIVE-DEPENDENCIES.md' = 'NATIVE-DEPENDENCIES.md'
}

$cpuBackends = @(Get-ChildItem (Join-Path $repoRoot 'runtime/whisper/ggml-cpu-*.dll') -File)
if ($cpuBackends.Count -eq 0) { throw 'Missing Whisper CPU backend: runtime/whisper/ggml-cpu-*.dll' }
foreach ($backend in $cpuBackends) {
 $relative = 'runtime/whisper/' + $backend.Name
 $files[$relative] = $relative
}

foreach ($relative in $files.Keys) {
 if (-not (Test-Path -LiteralPath (Join-Path $repoRoot $relative) -PathType Leaf)) { throw "Missing dependency: $relative" }
}

$crt = @('msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll')
foreach ($name in $crt) {
 if (-not (Test-Path -LiteralPath (Join-Path $env:WINDIR "System32/$name"))) { throw "Missing x64 Visual C++ runtime: $name" }
}

New-Item -ItemType Directory -Force (Join-Path $repoRoot 'dist') | Out-Null
try {
 $stream = [IO.File]::Open($output, [IO.FileMode]::Create)
 $zip = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Create)
 try {
  foreach ($relative in $files.Keys) {
   $entry = "$releaseName/" + $files[$relative]
   [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $repoRoot $relative), $entry, [IO.Compression.CompressionLevel]::Optimal) | Out-Null
  }
  foreach ($name in $crt) {
   $entry = "$releaseName/runtime/whisper/$name"
   [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $env:WINDIR "System32/$name"), $entry, [IO.Compression.CompressionLevel]::Optimal) | Out-Null
  }
 } finally { $zip.Dispose() }
 $hash = (Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash.ToLowerInvariant()
 "$hash  $([IO.Path]::GetFileName($output))" | Set-Content -LiteralPath (Join-Path $repoRoot 'dist/SHA256SUMS.txt') -Encoding ascii
 Write-Output "Portable release: $output"
 Write-Output "Extract the ZIP and run $releaseName/SyncPlayer.exe"
} catch {
 if (Test-Path -LiteralPath $output) { Remove-Item -LiteralPath $output -Force }
 throw
}
