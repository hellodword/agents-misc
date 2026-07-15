# Environment

- Preserve deliberately tracked `.devcontainer/**` and `.vscode/**` configuration when it affects the task.
- Discover project tools through the project's declared environment and ordinary environment capabilities through `PATH`.
- Do not hardcode machine-specific absolute paths when tool discovery or project configuration can provide them.
- Do not assume KVM, a graphical display, a Docker daemon, GPU access, mobile simulators, signing credentials, network access, or privileged mounts.
- Treat a missing environment capability as a diagnosable blocker, not as permission to mutate the host.
- Do not use global installs, a host package manager, or system-level configuration to repair a project toolchain.
- When blocked, load the environment troubleshooting skill, capture the narrow failure, inspect only relevant tracked environment configuration, and retry the narrow command.
- Keep temporary diagnostics in a confirmed ignored `tmp/agent/<task-id>/` path and clean them up. Use a system temporary directory if the repository has no confirmed ignored path.
