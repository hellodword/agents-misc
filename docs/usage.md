# Usage

## devcontainer

```json
{
  "build": {
    "dockerfile": "Dockerfile"
  },
  "updateRemoteUserUID": false,
  "mounts": [
    "source=${localEnv:HOME}/repos/agents-misc/.agents,target=${containerWorkspaceFolder}/.agents,type=bind,readonly",
    "source=${localEnv:HOME}/repos/agents-misc/AGENTS.md,target=${containerWorkspaceFolder}/AGENTS.md,type=bind,readonly",
    "source=${localEnv:HOME}/repos/agents-misc/tools/codex-hooks/codex_hook_forwarder.py,target=/etc/codex/codex_hook_forwarder.py,type=bind,readonly",
    {
      "source": "${localEnv:HOME}/.codex",
      "target": "/home/vscode/.codex",
      "type": "bind"
    }
  ],
  "containerEnv": {
    "AI_COMMIT_COAUTHOR": "Codex <noreply@openai.com>"
  }
}
```

```dockerfile
FROM ghcr.io/hellodword/devcontainers-dev:latest

USER root
RUN /usr/bin/devcontainer-set-user-id --uid 1000 --gid 100
ENV XDG_RUNTIME_DIR=/run/user/1000
USER vscode
```

## docker

```bash
docker run --rm -it \
  -v "$(pwd)":"/worpsaces/${PWD##*/}" \
  -v "$HOME/.codex":/home/vscode/.codex \
  -v "$HOME/repos/agents-misc/AGENTS.md":"/worpsaces/${PWD##*/}/AGENTS.md":ro \
  -v "$HOME/repos/agents-misc/.agents":"/worpsaces/${PWD##*/}/.agents":ro \
  -v "$HOME/repos/agents-misc/tools/codex-hooks/codex_hook_forwarder.py":/etc/codex/codex_hook_forwarder.py:ro \
  -w "/worpsaces/${PWD##*/}" \
  ghcr.io/hellodword/devcontainers-dev:latest bash
```
