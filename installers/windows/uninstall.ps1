# AnySave uninstaller for Windows. Run via "Uninstall AnySave.bat".
# Removes what the installer added. Your downloads (Downloads\AnySave) are NOT touched.
#  - %LOCALAPPDATA%\AnySave            (helper, yt-dlp, ffmpeg, speech engine and models, settings, logs)
#  - %USERPROFILE%\AnySave\extension   (the extension folder Chrome loads)
#  - the browser registrations, the anysave:// link handler, Start Menu and "Send to" shortcuts
#  - the AnySave folder in your PATH (the `anysave` command)
# Options: -Yes  don't ask first.
param([switch]$Yes)
$ErrorActionPreference = 'Continue'

$App      = Join-Path $env:LOCALAPPDATA 'AnySave'
$ExtRoot  = Join-Path $env:USERPROFILE 'AnySave'
$ExtDest  = Join-Path $ExtRoot 'extension'
$HostName = 'com.anysave.host'
$removed  = New-Object System.Collections.Generic.List[string]

Write-Host '=== Uninstall AnySave ==='
Write-Host 'This removes the AnySave app, its helper, the speech models and its settings.'
Write-Host 'Your downloaded files in Downloads\AnySave are kept.'
if (-not $Yes) {
  $answer = Read-Host 'Continue? Type y and press Enter'
  if ($answer -notmatch '^(y|yes)$') { Write-Host 'Nothing was removed.'; exit 0 }
}

# 1. Stop AnySave if it is running.
Get-Process -Name 'AnySave', 'anysave-host', 'whisper-cli' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

function Remove-IfThere($Path, $Label) {
  if ($Path -and (Test-Path -LiteralPath $Path)) {
    Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue
    if (-not (Test-Path -LiteralPath $Path)) { $removed.Add($Label) } else { Write-Host "Couldn't remove $Path (is a file still open?)" -ForegroundColor Yellow }
  }
}

# 2. Helper, tools, models, settings, logs.
Remove-IfThere $App $App
# 3. Extension folder (and %USERPROFILE%\AnySave if nothing else is in it).
Remove-IfThere $ExtDest $ExtDest
if ((Test-Path -LiteralPath $ExtRoot) -and -not (Get-ChildItem -LiteralPath $ExtRoot -Force)) { Remove-IfThere $ExtRoot $ExtRoot }

# 4. Browser registrations and the anysave:// link handler.
foreach ($k in @(
  "HKCU:\Software\Google\Chrome\NativeMessagingHosts\$HostName",
  "HKCU:\Software\Microsoft\Edge\NativeMessagingHosts\$HostName",
  "HKCU:\Software\BraveSoftware\Brave-Browser\NativeMessagingHosts\$HostName",
  "HKCU:\Software\Chromium\NativeMessagingHosts\$HostName",
  'HKCU:\Software\Classes\anysave')) {
  Remove-IfThere $k $k
}

# 5. Shortcuts.
foreach ($dir in @([Environment]::GetFolderPath('Programs'), [Environment]::GetFolderPath('SendTo'))) {
  if ($dir) { Remove-IfThere (Join-Path $dir 'AnySave.lnk') (Join-Path $dir 'AnySave.lnk') }
}

# 6. PATH entry.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -and (($userPath -split ';') -contains $App)) {
  $new = (($userPath -split ';') | Where-Object { $_ -and $_ -ne $App }) -join ';'
  [Environment]::SetEnvironmentVariable('Path', $new, 'User')
  $removed.Add("$App from your PATH")
}

Write-Host ''
if ($removed.Count -eq 0) { Write-Host "AnySave wasn't installed (nothing to remove)." }
else { Write-Host 'Removed:'; foreach ($r in $removed) { Write-Host "  - $r" } }
Write-Host ''
Write-Host 'Last step, in Chrome: open chrome://extensions and click Remove on AnySave.'
Write-Host 'Your downloads are still in Downloads\AnySave.'
