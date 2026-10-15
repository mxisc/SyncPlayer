param([string]$SevenZip = 'C:\Program Files\7-Zip\7z.exe')
$ErrorActionPreference = 'Stop'
if (!(Test-Path -LiteralPath $SevenZip)) { throw '7-Zip is required to extract the mpv runtime.' }
$target = Join-Path $PSScriptRoot 'mpv'
New-Item -ItemType Directory -Force -Path $target | Out-Null
$url = 'https://github.com/shinchiro/mpv-winbuild-cmake/releases/download/20261010/mpv-x86_64-20261010-git-b2c255c13e.7z'
$archive = Join-Path $target 'package.7z'
Invoke-WebRequest -Uri $url -OutFile $archive
if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne '70A568C046A10F0B365319C4EC5382D8FB37F509615944B7E9932B66E5CCFB25') { throw 'mpv archive checksum mismatch.' }
& $SevenZip x $archive "-o$target" -y
if ($LASTEXITCODE -ne 0) { throw 'mpv extraction failed.' }
