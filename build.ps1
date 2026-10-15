$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    foreach ($dependency in @('runtime/ffmpeg/ffmpeg.exe','runtime/ffmpeg/ffprobe.exe','runtime/whisper/whisper-cli.exe','runtime/models/ggml-small.bin')) {
        if (-not (Test-Path -LiteralPath $dependency -PathType Leaf)) { throw "Missing bundled dependency: $dependency" }
    }
    npm ci --prefix frontend
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    npm run build --prefix frontend
    if ($LASTEXITCODE -ne 0) { throw 'frontend build failed' }
    go test ./internal/...
    if ($LASTEXITCODE -ne 0) { throw 'tests failed' }
    & (Join-Path $PSScriptRoot 'update-icon.ps1')
    go build -tags desktop,production -ldflags '-H windowsgui' -o build/bin/SyncPlayer.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally { Pop-Location }

