# AnySave installer for Windows. Run via "Install AnySave.bat".
#  - Helper app + yt-dlp + ffmpeg -> %LOCALAPPDATA%\AnySave
#  - Chrome extension folder      -> %USERPROFILE%\AnySave\extension
#  - Registers the helper with Chrome, Edge, Brave and Chromium (current user only)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # makes Invoke-WebRequest much faster
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Src     = $PSScriptRoot
$App     = Join-Path $env:LOCALAPPDATA 'AnySave'
$Bin     = Join-Path $App 'bin'
$ExtDest = Join-Path $env:USERPROFILE 'AnySave\extension'
$HostName = 'com.vidgrab.host'
$ExtId   = 'jgkodjdjabccfmfbfmiinmppmcpgomai'   # fixed by the "key" in extension\manifest.json

# Download with retries: GitHub sometimes answers 502/504 for a moment.
function Get-File($Url, $OutFile) {
  for ($i = 1; $i -le 5; $i++) {
    try { Invoke-WebRequest $Url -OutFile $OutFile -UseBasicParsing; return }
    catch {
      if ($i -eq 5) { throw }
      Write-Host "  Download failed ($($_.Exception.Message)), retrying..."
      Start-Sleep -Seconds 3
    }
  }
}

# Stops the install if a download isn't the file its publisher lists.
function Test-Checksum($File, $Expected) {
  $got = (Get-FileHash $File -Algorithm SHA256).Hash
  if (-not $Expected -or $got -ne $Expected.Trim().ToUpper()) {
    Remove-Item $File -Force -ErrorAction SilentlyContinue
    Write-Host "$(Split-Path $File -Leaf) didn't match its published checksum. Nothing was installed from it." -ForegroundColor Red
    Write-Host 'Check your internet connection and run the installer again.'
    exit 1
  }
}
# The expected hash for $Name from a "hash  name" checksum list.
function Get-Sum($Url, $Name) {
  $tmpSum = Join-Path $env:TEMP ('anysave-sums-' + [guid]::NewGuid())
  Get-File $Url $tmpSum
  $line = Get-Content $tmpSum | Where-Object { ($_ -split '\s+')[-1] -eq $Name } | Select-Object -First 1
  Remove-Item $tmpSum -Force
  if ($line) { ($line -split '\s+')[0] } else { $null }
}

Write-Host '=== Installing AnySave ===' -ForegroundColor Cyan
if (-not (Test-Path (Join-Path $Src 'helper\vidgrab-host.exe')) -or -not (Test-Path (Join-Path $Src 'extension'))) {
  Write-Host "This installer must be run from the extracted AnySave-windows folder" -ForegroundColor Red
  Write-Host "(it needs the 'helper' and 'extension' folders next to it). If you opened it"
  Write-Host "from inside the zip, right-click the zip, choose Extract All, and try again."
  exit 1
}
# A running helper or yt-dlp locks its file, so copying over it would fail.
if (Get-Process -Name 'vidgrab-host', 'yt-dlp', 'ffmpeg', 'deno' -ErrorAction SilentlyContinue |
    Where-Object { $_.Path -and $_.Path.StartsWith($App, [StringComparison]::OrdinalIgnoreCase) }) {
  Write-Host 'AnySave is busy (a download or conversion is running).' -ForegroundColor Red
  Write-Host 'Let it finish or cancel it, close Chrome, Edge and Brave, then run the installer again.'
  exit 1
}
New-Item -ItemType Directory -Force -Path $Bin | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path $ExtDest) | Out-Null

# 1. Helper app
Copy-Item (Join-Path $Src 'helper\vidgrab-host.exe') (Join-Path $App 'vidgrab-host.exe') -Force

# 2. Extension (kept in a permanent place so the download folder can be deleted)
if (Test-Path $ExtDest) { Remove-Item $ExtDest -Recurse -Force }
Copy-Item (Join-Path $Src 'extension') $ExtDest -Recurse

# Upgrading from VidGrab: Chrome still loads %USERPROFILE%\VidGrab\extension, so refresh that copy
# too (nothing breaks), and reuse the old ffmpeg instead of downloading it again.
$OldApp = Join-Path $env:LOCALAPPDATA 'VidGrab'
$OldExt = Join-Path $env:USERPROFILE 'VidGrab\extension'
$Upgraded = $false
if (Test-Path $OldExt) {
  Remove-Item $OldExt -Recurse -Force
  Copy-Item (Join-Path $Src 'extension') $OldExt -Recurse
  Get-ChildItem $OldExt -Recurse -File | Unblock-File
  $Upgraded = $true
}
$OldFf = Join-Path $OldApp 'bin\ffmpeg.exe'
if ((Test-Path $OldFf) -and -not (Test-Path (Join-Path $Bin 'ffmpeg.exe'))) { Copy-Item $OldFf (Join-Path $Bin 'ffmpeg.exe') }

# 3. yt-dlp
Write-Host 'Downloading yt-dlp...'
$ytTmp = Join-Path $env:TEMP ('anysave-yt-dlp-' + [guid]::NewGuid() + '.exe')
Get-File 'https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe' $ytTmp
Test-Checksum $ytTmp (Get-Sum 'https://github.com/yt-dlp/yt-dlp/releases/latest/download/SHA2-256SUMS' 'yt-dlp.exe')
Move-Item $ytTmp (Join-Path $Bin 'yt-dlp.exe') -Force

# 4. ffmpeg (needed to join YouTube video + audio and to make MP3s)
if (-not (Test-Path (Join-Path $Bin 'ffmpeg.exe'))) {
  Write-Host 'Downloading ffmpeg (about 100 MB)...'
  $tmp = Join-Path $env:TEMP ('vidgrab-' + [guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    $zip = Join-Path $tmp 'ffmpeg.zip'
    Get-File 'https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip' $zip
    Test-Checksum $zip (Get-Sum 'https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest/checksums.sha256' 'ffmpeg-master-latest-win64-gpl.zip')
    Expand-Archive $zip -DestinationPath $tmp
    $ff = Get-ChildItem $tmp -Recurse -Filter 'ffmpeg.exe' | Select-Object -First 1
    Copy-Item $ff.FullName (Join-Path $Bin 'ffmpeg.exe') -Force
  } finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}

# 5. Deno: the JavaScript runtime yt-dlp needs to unlock YouTube's video formats.
if (-not (Test-Path (Join-Path $Bin 'deno.exe'))) {
  Write-Host 'Downloading Deno (needed for YouTube, about 45 MB)...'
  $tmp = Join-Path $env:TEMP ('anysave-' + [guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    $denoName = 'deno-x86_64-pc-windows-msvc.zip'   # runs on Windows on Arm too
    $denoUrl = "https://github.com/denoland/deno/releases/latest/download/$denoName"
    $zip = Join-Path $tmp $denoName
    Get-File $denoUrl $zip
    # Deno's Windows checksum file is PowerShell's Get-FileHash output: take the 64-hex hash in it.
    $sumFile = Join-Path $tmp 'deno.sha256sum'
    Get-File "$denoUrl.sha256sum" $sumFile
    $m = [regex]::Match((Get-Content $sumFile -Raw), '[0-9A-Fa-f]{64}')
    Test-Checksum $zip $(if ($m.Success) { $m.Value } else { $null })
    Expand-Archive $zip -DestinationPath $tmp
    Copy-Item (Join-Path $tmp 'deno.exe') (Join-Path $Bin 'deno.exe') -Force
  } finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}

# Clear the "downloaded from the internet" mark from everything we copied.
Get-ChildItem $App, $ExtDest -Recurse -File | Unblock-File

# 6. Native messaging manifest + registry keys
$manifestPath = Join-Path $App "$HostName.json"
$json = @{
  name            = $HostName
  description     = 'AnySave helper'
  path            = (Join-Path $App 'vidgrab-host.exe')
  type            = 'stdio'
  allowed_origins = @("chrome-extension://$ExtId/")
} | ConvertTo-Json
# UTF-8 without BOM (Chrome rejects a BOM; non-English user names need UTF-8)
[IO.File]::WriteAllText($manifestPath, $json, (New-Object Text.UTF8Encoding $false))

$browsers = @{
  'Chrome'   = 'HKCU:\Software\Google\Chrome\NativeMessagingHosts'
  'Edge'     = 'HKCU:\Software\Microsoft\Edge\NativeMessagingHosts'
  'Brave'    = 'HKCU:\Software\BraveSoftware\Brave-Browser\NativeMessagingHosts'
  'Chromium' = 'HKCU:\Software\Chromium\NativeMessagingHosts'
}
foreach ($b in $browsers.GetEnumerator()) {
  $key = Join-Path $b.Value $HostName
  New-Item -Path $key -Force | Out-Null
  Set-ItemProperty -Path $key -Name '(default)' -Value $manifestPath
  Write-Host "Registered with $($b.Key)"
}

Write-Host ''
& (Join-Path $App 'vidgrab-host.exe') --check
Write-Host ''
Write-Host '=== Done! Last step, in Chrome: ===' -ForegroundColor Green
Write-Host '  1. Go to chrome://extensions and turn on Developer mode (top right)'
Write-Host "  2. Click 'Load unpacked' and choose the folder:  $ExtDest"
Write-Host '     (an Explorer window showing it is opening now)'
Write-Host '  3. Close Chrome completely and open it again'
Write-Host ''
Write-Host 'Already installed? Just click the reload button on AnySave in chrome://extensions.'
Write-Host 'To update AnySave later, run this installer again.'
# The old helper folder isn't used any more (the browser registration now points here).
if (Test-Path $OldApp) { Remove-Item $OldApp -Recurse -Force -ErrorAction SilentlyContinue }
if ($Upgraded) {
  Write-Host ''
  Write-Host 'Updated: just click the reload button on the extension in chrome://extensions.' -ForegroundColor Green
  Write-Host "(Optional: remove it and 'Load unpacked' from $ExtDest, then delete $env:USERPROFILE\VidGrab.)"
}
if ($env:ANYSAVE_NO_OPEN -ne '1') {
  Start-Process explorer.exe (Split-Path $ExtDest)
}
