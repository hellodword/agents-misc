# Environment

- Preserve deliberately tracked `.devcontainer/**` and `.vscode/**` configuration when it affects the task.
- Discover project tools through the project's declared environment and ordinary environment capabilities through `PATH`.
- Treat a browser executable used by durable E2E as a project tool. Preserve explicit project configuration. When the project uses Nix or the current task is establishing it, add a missing browser to the project's development shell and supply its exact executable path from locked nixpkgs. Other projects use their established browser provisioning; do not introduce Nix merely to run E2E. Do not discover ambient host browsers through `PATH` or silently download or switch browsers.
- Do not hardcode machine-specific absolute paths when tool discovery or project configuration can provide them.
- Do not assume KVM, a graphical display, a Docker daemon, GPU access, mobile simulators, signing credentials, network access, or privileged mounts.
- Treat a missing environment capability as a diagnosable blocker, not as permission to mutate the host.
- Do not use global installs, a host package manager, or system-level configuration to repair a project toolchain.
- When blocked, load the environment troubleshooting skill, capture the narrow failure, inspect only relevant tracked environment configuration, and retry the narrow command.
- Keep temporary diagnostics in a confirmed ignored `tmp/agent/<task-id>/` path and clean them up. Use a system temporary directory if the repository has no confirmed ignored path.
