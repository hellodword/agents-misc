# Nix on GitHub-Hosted Ubuntu

Use these recipes only in a project-owned GitHub Actions workflow after matching the stated condition. They are not general Nix defaults. Never run them on a developer machine or self-hosted runner.

## 1. Heavy Nix disk preparation

Use this only when all of the following are true:

- the job runs on GitHub-hosted Ubuntu;
- `runner.environment == 'github-hosted'` and `runner.os == 'Linux'`;
- `GITHUB_ACTIONS=true`;
- `/nix` does not exist yet;
- measured or well-supported build estimates show that the standard runner disk is insufficient.

This is destructive to a disposable runner. Keep the explicit guard and exact SDK removal list; do not add wildcards or use it elsewhere.

```yaml
- name: Prepare heavy Nix disk
  if: ${{ runner.os == 'Linux' && runner.environment == 'github-hosted' }}
  shell: bash
  env:
    ROOT_SAFE_HAVEN_MB: "40000"
    ROOT_FALLBACK_SAFE_HAVEN_MB: "12288"
    ROOT_MIN_NIX_VOLUME_MB: "20480"
    MNT_SAFE_HAVEN_MB: "1024"
  run: |
    set -euo pipefail

    [[ "${GITHUB_ACTIONS:-}" == "true" ]] || {
      echo "GitHub Actions is required" >&2
      exit 1
    }
    [[ "${{ runner.environment }}" == "github-hosted" ]] || {
      echo "A GitHub-hosted runner is required" >&2
      exit 1
    }
    [[ "$(lsb_release -is 2>/dev/null || true)" == "Ubuntu" ]] || {
      echo "Ubuntu is required" >&2
      exit 1
    }
    [[ ! -e /nix ]] || {
      echo "Run disk preparation before installing Nix" >&2
      exit 1
    }

    for value in \
      "$ROOT_SAFE_HAVEN_MB" \
      "$ROOT_FALLBACK_SAFE_HAVEN_MB" \
      "$ROOT_MIN_NIX_VOLUME_MB" \
      "$MNT_SAFE_HAVEN_MB"; do
      [[ "$value" =~ ^[0-9]+$ ]] || {
        echo "Disk values must be integer MiB values" >&2
        exit 1
      }
    done

    diagnose_disk() {
      local label="$1"
      local target
      local -a targets=(/ /mnt /tmp /var/cache/nix /nix /nix/build /nix/tmp /var/lib/nix-storage /var/lib/docker)
      echo "::group::disk diagnostics: ${label}"
      date -u +"%Y-%m-%dT%H:%M:%SZ"
      df -hT "${targets[@]}" 2>/dev/null || true
      df -ih "${targets[@]}" 2>/dev/null || true
      for target in "${targets[@]}"; do
        if [[ -e "$target" ]]; then
          findmnt -T "$target" -o TARGET,SOURCE,FSTYPE,SIZE,AVAIL,USED,USE%,OPTIONS 2>/dev/null || true
        fi
      done
      sudo btrfs filesystem usage -T /nix 2>/dev/null || true
      sudo btrfs filesystem df /nix 2>/dev/null || true
      sudo btrfs filesystem show /nix 2>/dev/null || true
      lsblk -f 2>/dev/null || true
      sudo losetup -a 2>/dev/null || true
      sudo du -xh --max-depth=1 /var/cache/nix /mnt /var/lib/nix-storage /nix /var/lib/docker 2>/dev/null \
        | sort -h | tail -n 50 || true
      if command -v docker >/dev/null 2>&1; then
        docker info --format 'DockerRootDir={{.DockerRootDir}} Driver={{.Driver}}' 2>/dev/null || true
        docker system df -v 2>/dev/null || true
      fi
      echo "::endgroup::"
    }

    sudo rm -rf \
      /usr/share/dotnet \
      /usr/local/lib/android \
      /opt/ghc \
      /opt/hostedtoolcache \
      /opt/az \
      /opt/microsoft \
      /opt/google \
      /usr/local/.ghcup \
      /usr/share/swift \
      || true
    docker system prune -af || true
    sudo -n true

    if ! command -v mkfs.btrfs >/dev/null 2>&1; then
      sudo apt-get update
      sudo apt-get install -y btrfs-progs
    fi

    sudo mkdir -p /var/cache/nix /var/lib/nix-storage
    sudo chmod 1777 /var/cache/nix
    diagnose_disk before-carving

    source_label=/
    source_path=/
    image_dir=/var/lib/nix-storage
    reserve_mb="$ROOT_SAFE_HAVEN_MB"

    if [[ -d /mnt && "$(findmnt -T /mnt -no TARGET 2>/dev/null || true)" == "/mnt" ]]; then
      free_mnt="$(df -m --output=avail /mnt | tail -n 1 | tr -d ' ')"
      if (( free_mnt >= MNT_SAFE_HAVEN_MB + 1024 )); then
        source_label=/mnt
        source_path=/mnt
        image_dir=/mnt
        reserve_mb="$MNT_SAFE_HAVEN_MB"
      fi
    fi

    free_source="$(df -m --output=avail "$source_path" | tail -n 1 | tr -d ' ')"
    if [[ "$source_label" == "/" ]]; then
      normal_size=$((free_source - ROOT_SAFE_HAVEN_MB))
      if (( free_source < ROOT_SAFE_HAVEN_MB + 1024 || normal_size < ROOT_MIN_NIX_VOLUME_MB )); then
        reserve_mb="$ROOT_FALLBACK_SAFE_HAVEN_MB"
      fi
    fi
    (( free_source >= reserve_mb + 1024 )) || {
      echo "Insufficient disk for a guarded Nix volume" >&2
      exit 1
    }

    initial_loop="$(sudo losetup --find)"
    initial_img="${image_dir}/nix-disk${initial_loop##*/loop}.img"
    initial_size=$((free_source - reserve_mb))
    sudo fallocate -l "${initial_size}M" "$initial_img"
    sudo losetup "$initial_loop" "$initial_img"
    sudo mkfs.btrfs -L nix -d raid0 -m raid0 --nodiscard "$initial_loop"
    sudo btrfs device scan
    sudo mkdir -p /nix
    sudo mount "$initial_loop" /nix \
      -o noatime,nobarrier,nodiscard,compress=zstd:1,space_cache=v2,commit=120
    sudo mkdir -p /nix/build /nix/tmp
    sudo chmod 1777 /nix/tmp

    free_root="$(df -m --output=avail / | tail -n 1 | tr -d ' ')"
    if (( free_root > ROOT_SAFE_HAVEN_MB + 2048 )); then
      root_loop="$(sudo losetup --find)"
      root_img="/var/lib/nix-storage/root-disk${root_loop##*/loop}.img"
      root_size=$((free_root - ROOT_SAFE_HAVEN_MB))
      sudo fallocate -l "${root_size}M" "$root_img"
      sudo losetup "$root_loop" "$root_img"
      sudo btrfs device add --nodiscard "$root_loop" /nix
      sudo btrfs balance start -dusage=50 /nix
    fi

    export TMPDIR=/nix/tmp
    diagnose_disk after-carving
```

Retain disk, inode, mount, Btrfs, loop-device, directory-size, and Docker diagnostics before and after the build when investigating exhaustion. Do not turn this recipe into a default step.

## 2. `dockerTools.pullImage` container-store workaround

Use this only after the corresponding container-storage permission failure is observed on a GitHub-hosted Ubuntu runner. Do not include it in ordinary Nix CI.

```yaml
- name: Repair container storage permissions
  if: ${{ runner.os == 'Linux' && runner.environment == 'github-hosted' }}
  shell: bash
  run: |
    set -euo pipefail
    [[ "${GITHUB_ACTIONS:-}" == "true" ]] || exit 1
    sudo chmod 755 /run/containers
    sudo mkdir -p "/run/containers/$(id -u runner)"
    sudo chown runner: "/run/containers/$(id -u runner)"
```

## 3. Install and configure Nix

This recipe is an explicit exception for a one-use GitHub-hosted Ubuntu runner. Never copy its `apt` or installer steps to local or self-hosted environments. Pass cache names and public keys as project inputs; do not embed another project's defaults.

```yaml
- name: Install and configure Nix
  if: ${{ runner.os == 'Linux' && runner.environment == 'github-hosted' }}
  shell: bash
  env:
    GITHUB_ACCESS_TOKEN: ${{ github.token }}
    NIX_EXTRA_SUBSTITUTERS: ${{ inputs.nix_extra_substituters }}
    NIX_EXTRA_TRUSTED_PUBLIC_KEYS: ${{ inputs.nix_extra_trusted_public_keys }}
  run: |
    set -euo pipefail
    [[ "${GITHUB_ACTIONS:-}" == "true" ]] || exit 1
    [[ "${{ runner.environment }}" == "github-hosted" ]] || exit 1
    [[ "$(lsb_release -is 2>/dev/null || true)" == "Ubuntu" ]] || exit 1

    sudo apt-get update
    sudo apt-get install -y curl ca-certificates xz-utils
    curl -fsSL https://nixos.org/nix/install | sh -s -- --daemon

    echo "/nix/var/nix/profiles/default/bin" >> "$GITHUB_PATH"
    echo "NIX_REMOTE=daemon" >> "$GITHUB_ENV"
    echo "TMPDIR=/nix/tmp" >> "$GITHUB_ENV"
    export NIX_REMOTE=daemon
    export TMPDIR=/nix/tmp
    sudo mkdir -p /nix/build /nix/tmp
    sudo chmod 1777 /nix/tmp
    . /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh

    normalize_list() {
      local value="${1:-}"
      value="${value//$'\n'/ }"
      value="${value//$'\t'/ }"
      printf '%s\n' "$value" | awk '{$1=$1; print}'
    }

    current_user="$(id -un)"
    experimental="$(normalize_list "${NIX_EXPERIMENTAL_FEATURES:-nix-command flakes}")"
    jobs="$(normalize_list "${NIX_MAX_JOBS:-auto}")"
    trusted_users="$(normalize_list "${NIX_TRUSTED_USERS:-root ${current_user}}")"
    access_tokens="$(normalize_list "${NIX_ACCESS_TOKENS:-}")"
    [[ -n "$access_tokens" || -z "${GITHUB_ACCESS_TOKEN:-}" ]] \
      || access_tokens="github.com=${GITHUB_ACCESS_TOKEN}"
    substituters="$(normalize_list "${NIX_EXTRA_SUBSTITUTERS:-}")"
    public_keys="$(normalize_list "${NIX_EXTRA_TRUSTED_PUBLIC_KEYS:-}")"

    sudo mkdir -p /etc/nix
    sudo touch /etc/nix/nix.conf
    sudo sed -i \
      -e '/^[[:space:]]*experimental-features[[:space:]]*=/d' \
      -e '/^[[:space:]]*build-dir[[:space:]]*=/d' \
      -e '/^[[:space:]]*max-jobs[[:space:]]*=/d' \
      -e '/^[[:space:]]*trusted-users[[:space:]]*=/d' \
      -e '/^[[:space:]]*access-tokens[[:space:]]*=/d' \
      -e '/^[[:space:]]*extra-substituters[[:space:]]*=/d' \
      -e '/^[[:space:]]*extra-trusted-public-keys[[:space:]]*=/d' \
      /etc/nix/nix.conf
    {
      echo "experimental-features = ${experimental}"
      echo "build-dir = /nix/build"
      echo "max-jobs = ${jobs}"
      echo "trusted-users = ${trusted_users}"
      [[ -z "$access_tokens" ]] || echo "access-tokens = ${access_tokens}"
      [[ -z "$substituters" ]] || echo "extra-substituters = ${substituters}"
      [[ -z "$public_keys" ]] || echo "extra-trusted-public-keys = ${public_keys}"
    } | sudo tee -a /etc/nix/nix.conf >/dev/null
    sudo systemctl restart nix-daemon.service
    nix --version
    nix config show experimental-features
    nix config show build-dir
    nix config show substituters
    nix config show trusted-public-keys
    nix config show trusted-users
```

## 4. Inherit reviewed input-flake caches

Use this only for explicitly reviewed inputs and cache endpoints. Never inherit URLs or public keys from an unreviewed pull request or ref.

Define the collector in project flake code. `inputNames` is an allowlist; each value is a list of URL substrings allowed for that input.

```nix
collectInputNixConfig =
  {
    inputs,
    inputNames,
  }:
  let
    selectedInputs = lib.filterAttrs (
      name: value:
      builtins.hasAttr name inputNames
      && value ? outPath
      && builtins.pathExists (value.outPath + "/flake.nix")
    ) (removeAttrs inputs [ "self" ]);

    configs = lib.mapAttrsToList (
      name: input:
      let
        flake = import (input.outPath + "/flake.nix");
      in
      {
        inherit name;
        config = flake.nixConfig or { };
      }
    ) selectedInputs;

    asList = value: if builtins.isList value then value else [ value ];
    allowed = name: value:
      lib.any (substring: lib.hasInfix substring value) (asList inputNames.${name});
    readList = name: attribute: config:
      lib.filter (allowed name) (asList (config.${attribute} or [ ]));
    collect = attributes:
      lib.unique (
        lib.flatten (
          map (
            { name, config }:
            lib.flatten (map (attribute: readList name attribute config) attributes)
          ) configs
        )
      );
    substituters = collect [
      "substituters"
      "extra-substituters"
      "trusted-substituters"
      "extra-trusted-substituters"
    ];
    trustedPublicKeys = collect [
      "trusted-public-keys"
      "extra-trusted-public-keys"
    ];
  in
  {
    inherit substituters trustedPublicKeys;
    settings =
      (lib.optionalAttrs (substituters != [ ]) {
        extra-substituters = substituters;
      })
      // (lib.optionalAttrs (trustedPublicKeys != [ ]) {
        extra-trusted-public-keys = trustedPublicKeys;
      });
  };
```

Export the reviewed result as `lib.<system>.inheritedNixConfig`. If generated images must contain the cache configuration, inject it after module defaults:

```nix
{
  config = lib.optionalAttrs (inheritedNixConfig.settings != { }) {
    nix.settings = lib.mapAttrs (_: value: lib.mkAfter value) inheritedNixConfig.settings;
  };
}
```

In CI, evaluate only the reviewed checkout and do not update its lockfile. Read the two lists, merge them with current `extra-*` entries from `/etc/nix/nix.conf`, preserve first occurrence, and restart the daemon:

```bash
set -euo pipefail
eval_args=(
  --extra-experimental-features nix-command
  --extra-experimental-features flakes
  --no-write-lock-file
)

nix eval --json "${eval_args[@]}" .#lib.x86_64-linux.inheritedNixConfig >/dev/null

flake_list() {
  nix eval --raw "${eval_args[@]}" \
    ".#lib.x86_64-linux.inheritedNixConfig.$1" \
    --apply 'builtins.concatStringsSep " "'
}
conf_values() {
  awk -v key="$1" '
    $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
      sub(/^[^=]*=/, "")
      print
    }
  ' /etc/nix/nix.conf
}
unique_words() {
  printf '%s\n' "$*" | awk '
    {
      for (i = 1; i <= NF; i++) if (!seen[$i]++) values[++count] = $i
    }
    END {
      for (i = 1; i <= count; i++) printf "%s%s", (i == 1 ? "" : " "), values[i]
      if (count > 0) printf "\n"
    }
  '
}

merged_substituters="$(unique_words \
  "$(conf_values extra-substituters)" \
  "$(flake_list substituters)")"
merged_keys="$(unique_words \
  "$(conf_values extra-trusted-public-keys)" \
  "$(flake_list trustedPublicKeys)")"

sudo sed -i \
  -e '/^[[:space:]]*extra-substituters[[:space:]]*=/d' \
  -e '/^[[:space:]]*extra-trusted-public-keys[[:space:]]*=/d' \
  /etc/nix/nix.conf
{
  [[ -z "$merged_substituters" ]] || echo "extra-substituters = $merged_substituters"
  [[ -z "$merged_keys" ]] || echo "extra-trusted-public-keys = $merged_keys"
} | sudo tee -a /etc/nix/nix.conf >/dev/null
sudo systemctl restart nix-daemon.service
```
