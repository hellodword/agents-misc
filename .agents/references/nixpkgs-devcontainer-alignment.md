# Nixpkgs and Devcontainer Alignment

Use this reference before initializing or updating a repository `nixpkgs` input when the current environment exposes a devcontainer flake input file.

## Goal

Keep project `nixpkgs` aligned with the development container's exact nixpkgs revision when that revision is available, while falling back to `nixos-unstable` for unconstrained new work.

## Procedure

Before initializing or updating the repository `nixpkgs` input, check whether `$DEVCONTAINER_FLAKE_INPUTS` points to a readable JSON file with `.inputs.nixpkgs.rev`.

Shell shape:

```sh
devcontainer_nixpkgs_rev=""
if [ -n "${DEVCONTAINER_FLAKE_INPUTS:-}" ] && [ -r "$DEVCONTAINER_FLAKE_INPUTS" ]; then
  devcontainer_nixpkgs_rev="$(jq -r '.inputs.nixpkgs.rev // empty' "$DEVCONTAINER_FLAKE_INPUTS")"
fi
```

If `devcontainer_nixpkgs_rev` is non-empty, align to it:

```sh
nix flake update nixpkgs --override-input nixpkgs "github:NixOS/nixpkgs/${devcontainer_nixpkgs_rev}"
```

Verify the result:

```sh
jq -r '.nodes.nixpkgs.locked.rev // empty' flake.lock
```

Success means the locked revision matches `devcontainer_nixpkgs_rev`.

If the lockfile does not record the intended revision, stop and report the mismatch. Do not claim that the repository is aligned.

## Fallback

If `$DEVCONTAINER_FLAKE_INPUTS` is unset, unreadable, lacks `.inputs.nixpkgs.rev`, or the extracted value is empty, use:

```text
github:NixOS/nixpkgs/nixos-unstable
```

## Tool availability

Do not install `jq` globally.

If `jq` is unavailable before the dev shell exists, use an already available `python3` fallback only if it is present. Otherwise report an environment blocker.

Python fallback shape:

```sh
python3 - "$DEVCONTAINER_FLAKE_INPUTS" <<'PY'
import json
import sys
path = sys.argv[1]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)
print(data.get("inputs", {}).get("nixpkgs", {}).get("rev", ""))
PY
```

## Reporting

Report:

- whether `$DEVCONTAINER_FLAKE_INPUTS` was present and readable;
- whether `.inputs.nixpkgs.rev` existed;
- selected nixpkgs input;
- whether `flake.lock` records the intended revision;
- any tool availability blockers.
