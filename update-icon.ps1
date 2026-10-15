$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.7.0 -64 -skip-versioninfo -icon assets/branding/syncplayer-icon.ico -o resources_windows_amd64.syso
    if ($LASTEXITCODE -ne 0) { throw 'Icon resource generation failed' }
    # Wails loads RT_GROUP_ICON 3; preserve the IDI_APPLICATION group as well.
    $resourcePath = Join-Path $PSScriptRoot 'resources_windows_amd64.syso'
    $bytes = [IO.File]::ReadAllBytes($resourcePath)
    $sections = [BitConverter]::ToUInt16($bytes, 2)
    $sectionStart = 20 + [BitConverter]::ToUInt16($bytes, 16)
    $resourceOffset = -1
    for ($index = 0; $index -lt $sections; $index++) {
        $section = $sectionStart + 40 * $index
        if ([Text.Encoding]::ASCII.GetString($bytes, $section, 8).Trim([char]0) -eq '.rsrc') {
            $resourceOffset = [BitConverter]::ToUInt32($bytes, $section + 20)
            break
        }
    }
    if ($resourceOffset -lt 0) { throw 'Missing resource section' }
    $count = [BitConverter]::ToUInt16($bytes, $resourceOffset + 12) + [BitConverter]::ToUInt16($bytes, $resourceOffset + 14)
    $patched = $false
    for ($index = 0; $index -lt $count; $index++) {
        $entry = $resourceOffset + 16 + 8 * $index
        if ([BitConverter]::ToUInt32($bytes, $entry) -ne 14) { continue }
        $directory = $resourceOffset + ([BitConverter]::ToUInt32($bytes, $entry + 4) -band 0x7fffffff)
        $groups = [BitConverter]::ToUInt16($bytes, $directory + 12) + [BitConverter]::ToUInt16($bytes, $directory + 14)
        for ($group = 0; $group -lt $groups; $group++) {
            $groupEntry = $directory + 16 + 8 * $group
            if ([BitConverter]::ToUInt32($bytes, $groupEntry) -eq 1) {
                [BitConverter]::GetBytes([uint32]3).CopyTo($bytes, $groupEntry)
                $patched = $true
            }
        }
    }
    if (-not $patched) { throw 'Missing application icon group' }
    [IO.File]::WriteAllBytes($resourcePath, $bytes)
} finally { Pop-Location }
