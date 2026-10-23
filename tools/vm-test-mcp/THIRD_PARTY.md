# VM Test MCP component notices

The service and native helper source in this repository use the root MIT license.
The installed service package includes that text, Go's license, the exact vendored
module/version list and dependency LICENSE/COPYING/NOTICE files under
`share/licenses/vm-test-mcp`. `go.sum` and `flake.lock` record the resolved inputs.

The native helper compiles the C API glue from Oracle's official
[VirtualBox SDK 7.2.20](https://download.virtualbox.org/virtualbox/7.2.20/VirtualBoxSDK-7.2.20-175154.zip).
These SDK bindings retain their LGPL 2.1 notices. The package includes the
unmodified binding sources, headers, `COPYING.LIB`, and this repository's helper
source so the helper can be rebuilt with replacement bindings. Its compiler
invocation is in `nix/vm-test-mcp.nix`. This derivation is independent of nixpkgs'
VirtualBox host package. Host VirtualBox libraries are installed by the administrator
and are loaded at runtime. The Extension Pack is not redistributed.

The router Live ISO contains NixOS from the repository's locked nixpkgs. Its `system` link and squashfs record the exact installed closure. This includes the Linux
kernel, systemd, dnsmasq, nftables and VirtualBox Guest Additions userspace tools,
with their upstream licenses (including GPL and LGPL components). The
`nixpkgs-source` link in the image output preserves the package definitions, source
URLs/hashes and license metadata needed to reproduce this closure. The output
also includes this notice and the build's `system` link. Preserve the notices and
source/rebuild information when distributing cached service or image outputs;
the repository's MIT license does not relicense those components.

The service package also links xorriso from the locked nixpkgs (GPL 2 or later).
Its source tree, including upstream notices, is linked at
`share/licenses/vm-test-mcp/xorriso-source`. The locked Nix expression records the
reproducible package definition.
