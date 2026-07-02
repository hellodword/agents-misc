set positional-arguments

check:
  nix flake check

build-tools:
  nix build --no-link .#codexcfg .#codexSchemaRegistry .#codexConfigData .#codexConfigSite .#rulesync

codex-fetch ref:
  nix develop path:$PWD#dev --command python3 codex/scripts/fetch-upstream.py --ref {{ref}}

codex-apply-check ref:
  nix develop path:$PWD#dev --command python3 codex/scripts/apply-patches.py --ref {{ref}} --check

codex-apply ref:
  nix develop path:$PWD#dev --command python3 codex/scripts/apply-patches.py --ref {{ref}}

codex-refresh ref:
  nix develop path:$PWD#dev --command python3 codex/scripts/refresh-patches.py --ref {{ref}}

codex-test ref:
  nix develop path:$PWD#dev --command python3 codex/scripts/test.py --ref {{ref}}

codex-build ref:
  nix develop path:$PWD#dev --command python3 codex/scripts/build.py --ref {{ref}}

codexcfg-current:
  nix run .#codexcfg -- current

codexcfg-check-registry:
  nix run .#codexcfg -- check-registry --schemas codex/schemas

codexcfg-diff from to:
  nix run .#codexcfg -- diff --schemas codex/schemas --from {{from}} --to {{to}}

codexcfg-diff-defaults from to:
  nix run .#codexcfg -- diff-defaults --schemas codex/schemas --from {{from}} --to {{to}}

codexcfg-gen-toml version mode="reference":
  nix run .#codexcfg -- gen-toml --schemas codex/schemas --version {{version}} --mode {{mode}}

rulesync-build:
  nix build --no-link .#rulesync

rulesync-version:
  nix run .#rulesync -- --version

rulesync-init:
  nix run .#rulesync -- init

rulesync-generate:
  nix run .#rulesync -- generate

rulesync-generate-check:
  nix run .#rulesync -- generate --check

rulesync-gitignore:
  nix run .#rulesync -- gitignore

rulesync-import target="codexcli" features="rules":
  nix run .#rulesync -- import -t {{target}} -f {{features}}

rulesync-convert from="codexcli" to="opencode" features="rules":
  nix run .#rulesync -- convert --from {{from}} --to {{to}} -f {{features}}
