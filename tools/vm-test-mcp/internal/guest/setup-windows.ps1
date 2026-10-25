param([Parameter(Mandatory=$true)][string]$Binary, [Parameter(Mandatory=$true)][string]$Root)
$ErrorActionPreference = 'Stop'
# Invoked explicitly inside the source guest by an elevated administrator.
$Install = 'C:\ProgramData\vm-test'
$User = [Security.Principal.WindowsIdentity]::GetCurrent().Name
$Credential = Get-Credential -UserName $User -Message 'Administrator account used by Guest Control (existing local account)'
if (!$Credential) { throw 'An administrator credential is required' }
$Account = $Credential.UserName.Split('\')[-1]
if ($Account -ne $env:USERNAME) { throw 'Use the current local administrator account' }
Get-LocalUser -Name $Account | Out-Null
if (!(Get-Service VBoxService -ErrorAction SilentlyContinue)) { throw 'Install VirtualBox Guest Additions first' }
$Existing = Get-Service VMTestGuest -ErrorAction SilentlyContinue
if ($Existing) { Stop-Service VMTestGuest -Force }
New-Item -ItemType Directory -Force $Install, $Root, "$Root\inbox" | Out-Null
if ([IO.Path]::GetFullPath($Binary) -ne "$Install\vm-test-guest.exe") { Copy-Item $Binary "$Install\vm-test-guest.exe" -Force }
Copy-Item "$Install\vm-test-guest.exe" "$Install\vm-test-service.exe" -Force
# Guest Control may receive a filtered token; this explicitly selected account
# can submit commands and refresh the command binary inside its own guest.
& icacls.exe $Install /grant "${User}:(OI)(CI)F" | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Cannot grant guest command access' }
& icacls.exe $Root /grant "${User}:(OI)(CI)F" | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Cannot grant guest work access' }
$Command = '"' + $Install + '\vm-test-service.exe" service --root "' + $Root + '" --control-user "' + $Account + '"'
if ($Existing) {
    & sc.exe config VMTestGuest binPath= $Command start= auto | Out-Null
} else {
    New-Service -Name VMTestGuest -BinaryPathName $Command -StartupType Automatic -DisplayName 'VM Test Guest Supervisor' | Out-Null
}
if ($LASTEXITCODE -and $LASTEXITCODE -ne 0) { throw 'Service installation failed' }
# Preserve the administrator's autologon decision. No login is enabled here.
$Winlogon = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
$Login = Get-ItemProperty $Winlogon
$Automatic = $Login.AutoAdminLogon -eq '1'
function Xml([string]$Value) { [Security.SecurityElement]::Escape($Value) }
$Password = Xml ($Credential.GetNetworkCredential().Password)
$Name = Xml $Account
$FirstBoot = Xml ('"' + $Install + '\vm-test-guest.exe" firstboot --root "' + $Root + '"')
$AutoLogon = ''
if ($Automatic) {
    if ($Login.DefaultUserName -and $Login.DefaultUserName -ne $Account) { throw 'Autologon account differs from the prepared administrator' }
    $AutoLogon = "<AutoLogon><Enabled>true</Enabled><LogonCount>999999</LogonCount><Username>$Name</Username><Password><Value>$Password</Value><PlainText>true</PlainText></Password></AutoLogon>"
}
# Carry the administrator's language and keyboard choices into each clone.
# Unconfigured international settings otherwise leave OOBE waiting for input.
$InputLocale = Xml ((Get-WinUserLanguageList | ForEach-Object { $_.InputMethodTips }) -join ';')
if (!$InputLocale) { throw 'Windows input locales are unavailable' }
$SystemLocale = Xml ((Get-WinSystemLocale).Name)
$UserLocale = Xml ((Get-Culture).Name)
$DisplayLanguage = Get-WinUILanguageOverride
if (!$DisplayLanguage) { $DisplayLanguage = Get-UICulture }
$UILanguage = Xml $DisplayLanguage.Name
$Unattend = @"
<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
 <settings pass="generalize"><component name="Microsoft-Windows-PnpSysprep" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS"><PersistAllDeviceInstalls>true</PersistAllDeviceInstalls></component></settings>
 <settings pass="specialize">
  <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS"><ComputerName>*</ComputerName></component>
  <component name="Microsoft-Windows-Deployment" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS"><RunSynchronous><RunSynchronousCommand wcm:action="add"><Order>1</Order><Path>$FirstBoot</Path></RunSynchronousCommand></RunSynchronous></component>
 </settings>
 <settings pass="oobeSystem">
 <component name="Microsoft-Windows-International-Core" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS"><InputLocale>$InputLocale</InputLocale><SystemLocale>$SystemLocale</SystemLocale><UILanguage>$UILanguage</UILanguage><UserLocale>$UserLocale</UserLocale></component>
 <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
  <OOBE><HideEULAPage>true</HideEULAPage><HideOEMRegistrationScreen>true</HideOEMRegistrationScreen><HideOnlineAccountScreens>true</HideOnlineAccountScreens><HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE><ProtectYourPC>3</ProtectYourPC></OOBE>
  <UserAccounts><LocalAccounts><LocalAccount wcm:action="add"><Name>$Name</Name><Group>Administrators</Group><Password><Value>$Password</Value><PlainText>true</PlainText></Password></LocalAccount></LocalAccounts></UserAccounts>
  $AutoLogon
 </component></settings>
</unattend>
"@
Remove-Item "$Root\prepared" -ErrorAction SilentlyContinue
$Answer = "$Install\unattend.xml"
[IO.File]::WriteAllText($Answer, $Unattend, [Text.UTF8Encoding]::new($false))
# Sysprep generalizes the source once; each linked clone specializes separately.
$Process = Start-Process "$env:WINDIR\System32\Sysprep\Sysprep.exe" -ArgumentList @('/generalize','/oobe','/shutdown',"/unattend:$Answer") -Wait -PassThru
if ($Process.ExitCode -ne 0) { throw 'Sysprep failed; inspect Windows Panther and Sysprep logs before creating a snapshot' }
