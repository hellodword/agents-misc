set positional-arguments

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
