# Preparing a Windows test baseline

This is a manual guide for the VM owner. Installation media, OS setup and baseline creation are prepared outside runtime test automation.

## Host and guest

Use the host's ordinary VirtualBox installation and a dedicated registered test VM. Run the MCP controller as the VM owner with access to the graphical session used by the selected startup profile. The controller needs Python 3.11+, uv and its declared MCP SDK; it uses the installed `VBoxManage`.

Install Windows from administrator-provided, appropriately licensed media. Install matching Guest Additions, Python 3.11+ at a known absolute path, and the common project tools worth retaining in the baseline. Create a test user with a password and a writable task workspace. Register these in the host-owned service configuration.

Use the same test account for the console desktop and Guest Control commands. In that account, run `python -c "import sys; print(sys.executable); print(sys.version)"` and register the reported executable. A per-user installation can live under `C:\Users\WINDOWS_TEST_USER\AppData\Local\Programs\Python\Python314\python.exe`; use the actual installed path. A workspace under that user's profile gives uploads, test programs and reports a common writable location.

Use a disposable test disk and keep host data outside the guest's shared-folder namespace. Service-mediated uploads provide task inputs. Review attached media and network access for the intended test environment.

## Automatic login and an available desktop

For unattended Windows 10/11 testing, prepare automatic console login with [Microsoft Sysinternals Autologon](https://learn.microsoft.com/en-us/sysinternals/downloads/autologon):

1. Sign in as the test account and run Autologon as administrator.
2. Enter the test username, the guest computer name as the domain for a local account (`hostname` shows it), and the account password. Windows Hello PINs are separate from this password.
3. Select **Enable**, then restart Windows and verify that it reaches the test account's desktop without input. Autologon saves the configuration without validating the supplied credentials. **Disable** in the same tool reverses the setting.

Configure the test desktop to remain available:

| Setting                   | Windows configuration                                                                                                                                           |
| ------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Require sign-in again     | Settings > Accounts > Sign-in options: select **Never** where available.                                                                                        |
| Dynamic lock              | Turn off automatic locking in Sign-in options.                                                                                                                  |
| Screen saver              | Settings > Personalization > Lock screen > Screen saver: select **None** and clear **On resume, display logon screen**.                                         |
| Screen and sleep timeouts | Windows 10: Settings > System > Power & sleep. Windows 11: Settings > System > Power / Power & battery. Set the test VM's screen and sleep timers to **Never**. |

See Microsoft's [sign-in settings](https://support.microsoft.com/en-gb/accounts-billing/security/sign-in-options-in-windows), [screen saver settings](https://support.microsoft.com/en-us/windows/configure-a-screen-saver-in-windows-a9dc2a0c-dc8e-9161-d270-aaccc252082a) and [power settings](https://support.microsoft.com/en-gb/windows/experience/power-battery/power-settings-in-windows-11).

For an unattended test baseline, an administrator PowerShell window can configure the current power plan and disable hibernation:

```powershell
powercfg /change monitor-timeout-ac 0
powercfg /change monitor-timeout-dc 0
powercfg /change standby-timeout-ac 0
powercfg /change standby-timeout-dc 0
powercfg /hibernate off
powercfg /query
powercfg /availablesleepstates
```

The AC/DC settings cover plugged-in and battery operation. A zero display/sleep timeout means **Never**; `/hibernate off` disables hibernation. Read back the current plan and available sleep states before saving the snapshot. Changing only the screen timeout leaves the sleep timer active. See Microsoft's [powercfg reference](https://learn.microsoft.com/en-us/windows-hardware/design/device-experiences/powercfg-command-line-options).

Automatic login applies at startup. A desktop locked later still needs unlocking. MCP credentials authenticate Guest Control commands; the service has no console login/unlock operation. Its session probe checks a nonzero session on `WinSta0`, which can also exist while the screen is locked. Validate the prepared baseline with a visible application, an input assertion and a screenshot. See [Microsoft's UI test considerations](https://learn.microsoft.com/en-us/azure/devops/pipelines/test/ui-testing-considerations?view=azure-devops).

## Defender settings for unsigned development builds

For a dedicated development baseline whose owner chooses to disable real-time scanning and behavior monitoring, apply the settings inside the Windows guest during preparation. Record the chosen settings with the baseline; ordinary test execution uses that prepared environment.

Open Windows Security > Virus & threat protection > Manage settings and turn **Tamper Protection** off. This permits changes to the protected antivirus settings. In an administrator PowerShell window, substitute the registered guest workspace and run:

```powershell
$testWorkspace = 'C:\Users\WINDOWS_TEST_USER\vm-tests'
Set-MpPreference -DisableRealtimeMonitoring $true
Set-MpPreference -DisableBehaviorMonitoring $true
Add-MpPreference -ExclusionPath $testWorkspace
```

The workspace exclusion covers its files in real-time and scheduled scans. Include another directory when the project's installed test artifacts actually live there. See [tamper protection](https://learn.microsoft.com/en-us/defender-endpoint/tamper-protection-windows-configure), [behavior monitoring](https://learn.microsoft.com/en-us/defender-endpoint/behavior-monitor) and [path exclusions](https://learn.microsoft.com/powershell/module/defender/add-mppreference).

On Pro, Enterprise and Education editions, run `gpedit.msc` and open Computer Configuration > Administrative Templates > Windows Components > Microsoft Defender Antivirus > Real-time Protection:

| Policy                        | Development baseline value |
| ----------------------------- | -------------------------- |
| Turn off real-time protection | **Enabled**                |
| Turn on behavior monitoring   | **Disabled**               |

Local protection switches can turn back on automatically. Save the policy and exclusions, then restart and inspect the effective state from a new administrator PowerShell window. Reading exclusions requires elevation; a Guest Control test session can have an unelevated token even for an administrator account. Home editions have no built-in Local Group Policy Editor; treat local protection switches as temporary and verify the retained workspace exclusions. See [Microsoft's real-time protection configuration](https://learn.microsoft.com/en-us/defender-endpoint/configure-real-time-protection-microsoft-defender-antivirus).

```powershell
Get-MpComputerStatus |
  Select-Object IsTamperProtected, RealTimeProtectionEnabled, BehaviorMonitorEnabled
(Get-MpPreference).ExclusionPath
```

For this baseline profile, real-time protection and behavior monitoring should report `False`, and the exclusions should contain the registered workspace. These settings leave Defender installed. Legacy `DisableAntiSpyware` registry recipes are not a dependable cross-version substitute; see [Microsoft's applicability notes](https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/security-malware-windows-defender-disableantispyware).

## Graphics and media

Choose the VirtualBox graphics controller, memory and 3D configuration for the installed guest driver. Preserve the startup environment already verified with the host's graphics stack. If that environment includes `ANV_DEBUG=video-decode,video-encode`, record it in the VM startup profile and verify it after launching through the service.

Run separate application checks for rendering, decoding and encoding. For browsers, retain renderer/decoder diagnostics and a visible test result. For FFmpeg, record the selected hardware device/codec and a successful representative operation. A codec advertised by a program or a successful playback alone does not prove hardware execution. Record unsupported combinations as capability results.

For physical camera input, the host owner prepares the matching Extension Pack and device access described in [VirtualBox webcam passthrough](https://docs.oracle.com/en/virtualization/virtualbox/7.2/user/AdvancedTopics.html#webcam-passthrough). Configure a USB controller and the guest application's camera permission. Keep microphone and speaker input/output settings in the baseline. Runtime tests select an enumerated camera through MCP and obtain explicit human approval before attaching or detaching it.

Consult [VirtualBox Guest Additions](https://docs.oracle.com/en/virtualization/virtualbox/7.2/user/guestadditions.html) for the installed version's graphics support and [Guest Control](https://docs.oracle.com/en/virtualization/virtualbox/7.2/user/vboxmanage.html) for execution/session requirements.

## Baseline acceptance

1. Restart after preparing login, desktop and Defender settings. Confirm automatic login, effective settings, Python and guest file transfer. Normal MCP shutdown requests the guest OS through Guest Additions and verifies that the VM powers off.
2. Run a script that prints output, creates a report and exits with a known nonzero code. Confirm the code and report reach the Agent.
3. Exercise a command with descendants and a timeout; confirm cleanup before another task starts.
4. Run one representative browser/desktop test and the requested hardware-accelerated media operation. Save diagnostics and screenshots.
5. Shut down normally and save a powered-off snapshot containing the prepared settings and tools. Set the service's `vms.<target>.baseline` to its name/UUID and reload the controller configuration.
6. Start a fresh task and verify baseline restoration, automatic login, project upload, test execution and final shutdown.

Each new task restores this baseline. Preserve common tools in the snapshot; install project-specific dependencies within tasks as needed. Repeat acceptance after relevant Windows, Guest Additions, graphics or host changes.
