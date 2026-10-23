# Preparing VirtualBox Windows/Linux baselines

This manual guide is for an explicitly authorized host/baseline preparation task.
Runtime tests use disposable linked clones of an administrator's powered-off
snapshot. The service installation guide owns exact build/install commands,
shared YAML, process accounts and local cleanup recovery.

## Required preparation

For Windows, install and activate the OS, disable Windows Update through group
policy, and install Guest Additions. Use a password-bearing local administrator
for Guest Control. Run the service package's Windows guest `prepare` command from
an elevated terminal. It installs the command service/supervisor, prepares Sysprep
specialization and shuts down for a snapshot. It preserves the administrator's
autologon decision. Capture the powered-off snapshot before booting the source again.

For Linux, install the OS and Guest Additions, configure root credentials, and
verify systemd plus cgroup v2. Run the packaged Linux guest `prepare` command as
root. It installs the supervisor and first-boot identity unit, clears machine-id
and SSH host keys, and shuts down. Capture a powered-off snapshot. Commands use
system-level transient units; user-manager setup and linger are unnecessary.

Both source VMs use VDI disks. Remove host shares, raw disks, PCI passthrough,
generic USB filters and host serial/parallel devices. Register the source VM
name/UUID and snapshot UUID. Each lab linked-clones that snapshot directly; there
is no full-copy preparation cache. The source is never a command target.

The controller probes the prepared command service in its clones; its version and
hash are evidence, while live capabilities determine readiness.
Windows children enter Job Objects before execution; Linux commands enter system
cgroups. Without a desktop login, Windows CLI commands can run through the prepared
LocalSystem command service. With a logged-in administrator, it can run commands
on that administrator's desktop. Verify the actual identity/elevation and application
behavior when these are part of the test.

## Optional baseline conveniences

Recommend automatic Windows login and disabling sleep, screensavers and automatic
lock for repeated desktop work. They are not requirements: native screenshots and
input remain available at login, and the agent can inspect the screen and log in
or unlock using the configured test account. MCP does not embed a vision model.
Do not force a particular scaling, language or keyboard layout.

Recommend Python in Windows/Linux PATH and Docker Engine with Compose on Linux,
with Docker enabled at boot. These are optional. Probe executable versions and
imports through ordinary commands; install missing tools/libraries inside the lab
using project tooling, guest commands and transfers, optionally from offline
packages. Keep interpreter and application paths in project test context, not the
service configuration. Add durable baseline dependencies only when explicitly
selected by the administrator.

For the optional controls/vision example, use the skill's environment script and
hash-locked dependency files. They need the declared Python version, independent
venvs, actual imports and desktop acceptance. Project automation remains runnable
without MCP; it is not part of baseline identity or command supervision.

## Host, router and desktop settings

Run the controller under the VirtualBox owner's graphical session for GUI nodes.
The service package supplies the official-SDK native input helper and xorriso.
The latter creates per-lab credential seeds. Configure an administrator-built
router Live ISO path. MCP creates a diskless 1 vCPU / 256 MiB router and keeps both
the system ISO and seed attached until cleanup; no router import or snapshot is
needed. The boot ISO remains administrator-owned. Verify actual operation at
256 MiB before relying on it unattended.

Clones default to `GUI/AutoresizeGuest=false`. Use `node_display(auto_resize=…)`
for the per-node setting and `lab_gui_set(max_guest_resolution="any"|"auto")` for
the global resolution policy (default any). The service restores the recorded
original global value at cleanup and reports conflicts with external edits.
These settings do not require administrator `setextradata` commands.

Keep graphics controller, VRAM, paravirtualization and paging choices in the
baseline. Declare approved camera/audio/3D capabilities and any required host
startup environment in the template. Requests still default media off. Describe
rendering/decoding/encoding from the actual measured application behavior; device
availability alone does not prove acceleration.

## Acceptance and updating

Check two clones from one snapshot for independent identities and disk writes,
CLI execution without login, optional GUI login/unlock, native input/screenshots,
concurrent commands, cancellation/descendant cleanup, normal shutdown and final
VM deletion. Verify Windows/Linux Compose traffic, TCP/UDP ingress, protected
addresses, egress changes, router restart/recovery and the 256 MiB router target.
Record evidence and cleanup separately from application assertions.

If a source occupies 30 GiB on disk and two clone deltas grow to 2 and 3 GiB,
total disk use is approximately 35 GiB plus router ISO, metadata and retained
evidence. Deleting the lab releases its deltas. A preinstalled baseline tool can
reduce repeated installs; an offline installer only saves downloading, and still
writes installed files into each clone's delta.

To update a baseline, finish its users, boot/update the source, run preparation
again and create a new powered-off snapshot. Change the registered snapshot
explicitly. Preserve parent media while any snapshot/clone still references it.
