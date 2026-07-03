---
id: core.environment
kind: core
triggers:
  - 'environment'
  - 'devcontainer'
  - 'host tool'
  - 'global install'
  - 'system dependency'
summary: Respect local environment limits, devcontainer boundaries, and available capabilities.
load_with: []
---

# Environment Rules

- Assume the default development environment may be a VS Code devcontainer.
- Do not read or write `.vscode/**` or `.devcontainer/**` by default.
- If the user explicitly asks for editor/container diagnostics, inspect only the narrow files needed for the diagnosis.
- Never modify system-level package managers or global toolchains.
- Do not run `apt`, `brew`, `sudo apt`, global npm installs, global cargo installs, or Go installs into a global bin.
- Do not execute curl/wget-to-shell installers.
- Prefer tools declared in `flake.nix`, project-local lockfiles, or checked-in scripts.
- Do not assume KVM, host display, systemd, Docker daemon, GPU, USB, Android emulator, privileged container settings, or additional kernel namespace support.
- Use `tmp/` for runtime artifacts.
- Use `.work/` for ignored upstream source checkouts in pure patch projects.
- Do not assume conventional Linux paths such as `/usr/bin/chromium`; use PATH discovery.
