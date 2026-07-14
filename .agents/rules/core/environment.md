---
id: core.environment
kind: core
triggers:
  - "environment"
  - "devcontainer"
  - "host tool"
  - "global install"
  - "system dependency"
summary: Respect local environment limits, shared devcontainer configuration, and available capabilities.
companions:
  skills:
    - id: environment-troubleshooting
      when: a local environment or capability blocks the task
---

# Environment Rules

- Assume the default development environment may be a VS Code devcontainer.
- Read tracked `.vscode/**` or `.devcontainer/**` configuration when relevant and preserve deliberately shared project configuration.
- Keep new or untracked machine-local editor/container files ignored unless the user explicitly requests sharing them.
- Never modify system-level package managers or global toolchains.
- Do not run `apt`, `brew`, `sudo apt`, global npm installs, global cargo installs, or Go installs into a global bin.
- Do not execute curl/wget-to-shell installers.
- Prefer the project's declared toolchain, project-local lockfiles, and checked-in scripts. Do not introduce Nix solely to resolve an environment diagnostic.
- Do not assume KVM, host display, systemd, Docker daemon, GPU, USB, Android emulator, privileged container settings, or additional kernel namespace support.
- Use the project's ignored temp directory for runtime artifacts; use `tmp/` only when it exists or is confirmed ignored.
- Use `.work/` for ignored upstream source checkouts in pure patch projects.
- Let project-owned browser helpers select browser executables; agents should not probe browser paths themselves.
