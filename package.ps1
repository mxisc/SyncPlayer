$ErrorActionPreference = 'Stop'
$repoRoot = $PSScriptRoot
# Keep the executable's existing build/bin -> ../../runtime layout.
$releaseRoot = Join-Path $repoRoot ('dist/SyncPlayer-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
$required = @('build/bin/SyncPlayer.exe','runtime/mpv/mpv.exe','runtime/ffmpeg/ffmpeg.exe','runtime/ffmpeg/ffprobe.exe','runtime/whisper/whisper-cli.exe','runtime/whisper/whisper.dll','runtime/whisper/ggml.dll','runtime/whisper/ggml-base.dll','runtime/models/ggml-small.bin')
foreach($relative in $required){if(-not(Test-Path -LiteralPath (Join-Path $repoRoot $relative) -PathType Leaf)){throw "Missing release dependency: $relative"}}
New-Item -ItemType Directory -Path (Join-Path $releaseRoot 'build/bin'),(Join-Path $releaseRoot 'runtime/models') -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $repoRoot 'build/bin/SyncPlayer.exe') -Destination (Join-Path $releaseRoot 'build/bin')
foreach($component in @('mpv','ffmpeg','whisper')){Copy-Item -LiteralPath (Join-Path $repoRoot "runtime/$component") -Destination (Join-Path $releaseRoot 'runtime') -Recurse}
Copy-Item -LiteralPath (Join-Path $repoRoot 'runtime/models/ggml-small.bin'),(Join-Path $repoRoot 'runtime/models/LICENSE-whisper.txt') -Destination (Join-Path $releaseRoot 'runtime/models')
Copy-Item -LiteralPath (Join-Path $repoRoot 'NATIVE-DEPENDENCIES.md') -Destination $releaseRoot
Write-Output "Portable release: $releaseRoot (run build/bin/SyncPlayer.exe; preserve runtime folder)"
