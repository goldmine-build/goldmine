$DebugPreference = "Continue"
$ErrorActionPreference = "Stop"
$WarningPreference = "Continue"

$username = "chrome-bot"
$userDir = "C:\Users\$username"
$password = "CHROME_BOT_PASSWORD"
$logFile = "C:\gce_startup.log"

Function log($msg) {
  Write-Debug $msg
  Add-Content $logFile "$msg`n"
  try {
    # Write to GCE serial port output (console), if available.
    $port= new-Object System.IO.Ports.SerialPort COM1,9600,None,8,one
    $port.open()
    $port.WriteLine($msg)
    $port.close()
  } catch {}
}

Function banner($title) {
  $bannerWidth = 80
  $padChar = "*"
  $titleLine = " $title "
  $len = $titleLine.length
  $padding = 0
  $extra = $bannerWidth - $len
  if ($extra -ge 4) {
    $padding = $extra / 2
  }
  $titleLine = $titleLine.PadLeft($padding + $len, $padChar)
  $titleLine = $titleLine.PadRight($bannerWidth, $padChar)
  log ""
  log "".PadRight($bannerWidth, $padChar)
  log $titleLine
  log "".PadRight($bannerWidth, $padChar)
  log ""
}

Function setRegistryVar($path, $name, $value) {
  Set-ItemProperty -Path $path -Name $name -Value $value
  $res = (Get-ItemProperty -Path $path -Name $name)
  log "Set $name = $res"
}

Function setupAutoLogon() {
  $winLogon = "Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows` NT\CurrentVersion\WinLogon"
  setRegistryVar "$winLogon" DefaultUserName $username
  setRegistryVar "$winLogon" DefaultPassword $password
  setRegistryVar "$winLogon" AutoAdminLogon 1
}

Function addToRegistryPath($dir) {
  # Don't add empty strings.
  If (!$dir) { Return }

  # Retrieve the current PATH from the registry.
  $envRegPath = ("Registry::HKEY_LOCAL_MACHINE\System\CurrentControlSet\" +
                 "Control\Session Manager\Environment")
  $oldPath = (Get-ItemProperty -Path $envRegPath -Name PATH).Path

  # Don't add duplicates to PATH.
  $found = $FALSE
  $oldPath.Split(";") | ForEach { If ($_ -eq $dir) { $found = $TRUE } }

  if ($found) {
    return
  }

  # Override PATH
  $newPath=$oldPath+";"+$dir
  Set-ItemProperty -Path $envRegPath -Name PATH -Value $newPath
  $actualPath = (Get-ItemProperty -Path $envRegPath -Name PATH).Path
  log $actualPath
  $ENV:PATH = $actualPath
}

try
{

banner "Add to registry PATH"
addToRegistryPath "$userDir\depot_tools\python276_bin\Scripts"
addToRegistryPath "$userDir\depot_tools\python276_bin"
addToRegistryPath "$userDir\depot_tools"
addToRegistryPath "C:\Program` Files` (x86)\CMake\bin"

banner "Set up Auto-Logon"
setupAutoLogon

banner "Startup script complete"

}
catch
{

log "Caught an exception: $($_.Exception.GetType().FullName)"
log "$($_.Exception.Message)"

}
