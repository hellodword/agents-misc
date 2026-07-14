```json
{
  "image": "ghcr.io/hellodword/devcontainers-rust",
  "mounts": [
    "source=${localEnv:HOME}/repos/agents-misc/skills,target=/etc/codex/skills,type=bind,readonly",
    "source=${localEnv:HOME}/repos/agents-misc/tools/codex-hooks/codex_hook_forwarder.py,target=/etc/codex/codex_hook_forwarder.py,type=bind,readonly",
    {
      "source": "${localEnv:HOME}/.codex",
      "target": "/home/vscode/.codex",
      "type": "bind"
    }
  ]
}
```
