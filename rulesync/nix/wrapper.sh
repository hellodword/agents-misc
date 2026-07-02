set -euo pipefail

BWRAP="@bwrap@"
JQ="@jq@"
NODE="@node@"
RULESYNC_CLOSURE="@rulesyncClosure@"
RULESYNC_INIT_TEMPLATE="@rulesyncInitTemplate@"
RULESYNC_POLICY="@rulesyncPolicy@"
RULESYNC_SCOPE="@rulesyncScope@"
RULESYNC_UNWRAPPED="@rulesyncUnwrapped@"

die() {
  printf 'rulesync-jail: %s\n' "$*" >&2
  exit 2
}

load_policy_array() {
  local key="$1"
  # shellcheck disable=SC2016
  "$JQ" -r --arg key "$key" '.[$key][]?' "$RULESYNC_POLICY"
}

reject_home_path() {
  local p="$1"
  local label="$2"
  local home

  [[ -n "${HOME:-}" ]] || return 0

  home="$(realpath -m -- "$HOME")"
  case "$p" in
    "$home")
      die "refusing to use $label as home directory: $p"
      ;;
  esac
}

project_root_from_cwd() {
  local base cwd

  base="$(realpath -m -- "${RULESYNC_PROJECT_ROOT:-$PWD}")"
  cwd="$(realpath -m -- "$PWD")"

  reject_home_path "$cwd" "current directory"
  reject_home_path "$base" "project root"

  printf '%s\n' "$base"
}

first_command() {
  local arg

  for arg in "$@"; do
    case "$arg" in
      --)
        break
        ;;
      --json|-j|--help|--version|-h|-v|-V)
        continue
        ;;
      -*)
        continue
        ;;
      *)
        printf '%s\n' "$arg"
        return 0
        ;;
    esac
  done

  printf '\n'
}

reject_forbidden_options() {
  local arg

  for arg in "$@"; do
    case "$arg" in
      --)
        break
        ;;
      --global|--global=*|-g|--input-root|--input-root=*)
        die "option is intentionally disabled in this jail: $arg"
        ;;
    esac
  done
}

reject_symlink() {
  local p="$1"
  if [[ -L "$p" ]]; then
    die "refusing symlink path: $p"
  fi
}

check_existing_dir() {
  local p="$1"

  if [[ -e "$p" ]]; then
    reject_symlink "$p"
    [[ -d "$p" ]] || die "not a directory: $p"
  fi
}

check_existing_regular_file() {
  local p="$1"

  if [[ -e "$p" ]]; then
    reject_symlink "$p"
    [[ -f "$p" ]] || die "not a regular file: $p"
  fi
}

ensure_dir_rw() {
  local p="$1"

  reject_symlink "$p"
  mkdir -p -- "$p"
  reject_symlink "$p"
  [[ -d "$p" ]] || die "not a directory: $p"
}

ensure_file_rw() {
  local p="$1"
  local default_content="${2-}"
  local d

  d="$(dirname -- "$p")"
  reject_symlink "$d"
  mkdir -p -- "$d"
  reject_symlink "$d"

  reject_symlink "$p"
  if [[ ! -e "$p" ]]; then
    printf '%s' "$default_content" > "$p"
  fi

  reject_symlink "$p"
  [[ -f "$p" ]] || die "not a regular file: $p"
}

bind_dir_if_exists() {
  local mode="$1"
  local p="$2"

  if [[ -e "$p" ]]; then
    reject_symlink "$p"
    [[ -d "$p" ]] || die "not a directory: $p"

    case "$mode" in
      rw)
        bwrap_args+=(--bind "$p" "$p")
        ;;
      ro)
        bwrap_args+=(--ro-bind "$p" "$p")
        ;;
      *)
        die "internal error: unknown directory bind mode: $mode"
        ;;
    esac
  fi
}

bind_file_if_exists() {
  local mode="$1"
  local p="$2"

  if [[ -e "$p" ]]; then
    reject_symlink "$p"
    [[ -f "$p" ]] || die "not a regular file: $p"

    case "$mode" in
      rw)
        bwrap_args+=(--bind "$p" "$p")
        ;;
      ro)
        bwrap_args+=(--ro-bind "$p" "$p")
        ;;
      *)
        die "internal error: unknown file bind mode: $mode"
        ;;
    esac
  fi
}

append_unique() {
  local -n target_array="$1"
  local value="$2"
  local existing

  for existing in "${target_array[@]}"; do
    [[ "$existing" != "$value" ]] || return 0
  done

  target_array+=("$value")
}

array_contains() {
  local needle="$1"
  shift
  local value

  for value in "$@"; do
    [[ "$value" != "$needle" ]] || return 0
  done

  return 1
}

reject_never_writable_path() {
  local rel="$1"
  local denied

  for denied in "${rulesync_never_writable_files[@]}" "${rulesync_never_writable_dirs[@]}"; do
    case "$rel" in
      "$denied"|"$denied"/*)
        die "refusing never-writable init path: $rel"
        ;;
    esac
  done
}

validate_project_relative_path() {
  local rel="$1"
  local label="$2"

  [[ -n "$rel" ]] || die "$label cannot be empty"

  case "$rel" in
    /*|..|../*|*/..|*/../*|*//*|*\\*)
      die "$label must stay inside project root and be normalized: $rel"
      ;;
  esac
}

project_relative_path() {
  local raw="$1"
  local label="$2"
  local abs rel

  [[ -n "$raw" ]] || die "$label cannot be empty"

  case "$raw" in
    /*)
      abs="$(realpath -m -- "$raw")"
      ;;
    *)
      abs="$(realpath -m -- "$project/$raw")"
      ;;
  esac

  case "$abs" in
    "$project")
      rel="."
      ;;
    "$project"/*)
      rel="${abs#"$project"/}"
      ;;
    *)
      die "$label outside project root: $raw"
      ;;
  esac

  validate_project_relative_path "$rel" "$label"
  printf '%s\n' "$rel"
}

append_parent_mount_dirs() {
  local rel="$1"
  local dir

  [[ "$rel" != "." ]] || return 0

  dir="$(dirname -- "$rel")"
  while [[ "$dir" != "." && "$dir" != "/" ]]; do
    append_unique extra_source_mount_dirs "$dir"
    dir="$(dirname -- "$dir")"
  done
}

append_extra_source_file() {
  local rel="$1"

  if array_contains "$rel" "${rulesync_source_files[@]}" \
    || array_contains "$rel" "${extra_source_files[@]}"; then
    return 0
  fi

  extra_source_files+=("$rel")
}

parse_config_path_from_args() {
  local arg

  while (($# > 0)); do
    arg="$1"
    shift

    case "$arg" in
      --)
        break
        ;;
      --config)
        (($# > 0)) || die "missing value for --config"
        printf '%s\n' "$1"
        return 0
        ;;
      --config=*)
        printf '%s\n' "${arg#--config=}"
        return 0
        ;;
      -c)
        (($# > 0)) || die "missing value for -c"
        printf '%s\n' "$1"
        return 0
        ;;
      -c?*)
        printf '%s\n' "${arg#-c}"
        return 0
        ;;
    esac
  done

  printf '\n'
}

append_custom_config_inputs() {
  local raw_config config_rel config_dir local_rel

  raw_config="$(parse_config_path_from_args "$@")"
  [[ -n "$raw_config" ]] || return 0

  config_rel="$(project_relative_path "$raw_config" "config path")"
  [[ "$config_rel" != "." ]] || die "config path must be a file: $raw_config"

  check_existing_regular_file "$project/$config_rel"
  [[ -f "$project/$config_rel" ]] || die "missing config file: $project/$config_rel"

  append_extra_source_file "$config_rel"
  append_parent_mount_dirs "$config_rel"

  config_dir="$(dirname -- "$config_rel")"
  if [[ "$config_dir" == "." ]]; then
    local_rel="rulesync.local.jsonc"
  else
    local_rel="$config_dir/rulesync.local.jsonc"
  fi

  check_existing_regular_file "$project/$local_rel"
  append_extra_source_file "$local_rel"
  append_parent_mount_dirs "$local_rel"
}

read_scope_array() {
  local key="$1"
  # shellcheck disable=SC2016
  "$JQ" -r --arg key "$key" '.[$key][]?' <<< "$scope_json"
}

bind_source_inputs() {
  local rel

  for rel in "${rulesync_source_dirs[@]}"; do
    bind_dir_if_exists ro "$project/$rel"
  done

  for rel in "${rulesync_source_files[@]}"; do
    bind_file_if_exists ro "$project/$rel"
  done

  for rel in "${extra_source_files[@]}"; do
    bind_file_if_exists ro "$project/$rel"
  done
}

bind_scope_probe_inputs() {
  local rel

  bind_source_inputs

  for rel in "${rulesync_scope_probe_files[@]}"; do
    bind_file_if_exists ro "$project/$rel"
  done
}

append_store_closure() {
  local store_path

  while IFS= read -r store_path; do
    bwrap_args+=(--ro-bind "$store_path" "$store_path")
  done < "$RULESYNC_CLOSURE/store-paths"
}

base_bwrap_args() {
  bwrap_args=(
    --die-with-parent
    --new-session

    --unshare-all
    --cap-drop ALL

    --clearenv
    --setenv HOME /tmp/home
    --setenv XDG_CACHE_HOME /tmp/home/.cache
    --setenv XDG_CONFIG_HOME /tmp/home/.config
    --setenv XDG_DATA_HOME /tmp/home/.local/share
    --setenv TMPDIR /tmp
    --setenv PWD "$project"
    --setenv PATH /no-such-path

    --dev /dev
    --tmpfs /tmp
    --dir /tmp/home

    --perms 0555
    --dir "$project"

    --ro-bind-try /etc/passwd /etc/passwd
    --ro-bind-try /etc/group /etc/group
  )
}

run_scope_probe() {
  local rel

  base_bwrap_args

  for rel in "${rulesync_source_mount_dirs[@]}" "${extra_source_mount_dirs[@]}"; do
    bwrap_args+=(--perms 0555 --dir "$project/$rel")
  done

  append_store_closure
  bind_scope_probe_inputs

  bwrap_args+=(--chdir "$project")
  bwrap_args+=("$NODE" "$RULESYNC_SCOPE" "$@")

  "$BWRAP" "${bwrap_args[@]}"
}

protect_existing_never_writable() {
  local rel

  for rel in "${rulesync_never_writable_dirs[@]}"; do
    bind_dir_if_exists ro "$project/$rel"
  done

  for rel in "${rulesync_never_writable_files[@]}"; do
    bind_file_if_exists ro "$project/$rel"
  done
}

apply_init_template() {
  local silent=0
  local rel src dest dir

  for arg in "$@"; do
    [[ "$arg" != "--silent" && "$arg" != "-s" ]] || silent=1
  done

  while IFS= read -r rel; do
    validate_project_relative_path "$rel" "init directory"
    reject_never_writable_path "$rel"
    check_existing_dir "$project/$rel"
    ensure_dir_rw "$project/$rel"
  done < <(cd "$RULESYNC_INIT_TEMPLATE" && find . -type d -printf '%P\n' | sed '/^$/d' | sort)

  while IFS= read -r rel; do
    validate_project_relative_path "$rel" "init file"
    reject_never_writable_path "$rel"
    src="$RULESYNC_INIT_TEMPLATE/$rel"
    dest="$project/$rel"
    dir="$(dirname -- "$dest")"

    reject_symlink "$dir"
    mkdir -p -- "$dir"
    reject_symlink "$dir"
    check_existing_regular_file "$dest"

    if [[ ! -e "$dest" ]]; then
      cp -- "$src" "$dest"
      chmod u+rw -- "$dest"
    fi
  done < <(cd "$RULESYNC_INIT_TEMPLATE" && find . -type f -printf '%P\n' | sort)

  if [[ "$silent" != "1" ]]; then
    printf 'rulesync init complete: %s\n' "$project"
  fi
}

load_policy() {
  mapfile -t rulesync_source_dirs < <(load_policy_array sourceDirs)
  mapfile -t rulesync_source_files < <(load_policy_array sourceFiles)
  mapfile -t rulesync_source_mount_dirs < <(load_policy_array sourceMountDirs)
  mapfile -t rulesync_scope_probe_files < <(load_policy_array scopeProbeFiles)
  mapfile -t rulesync_never_writable_dirs < <(load_policy_array neverWritableDirs)
  mapfile -t rulesync_never_writable_files < <(load_policy_array neverWritableFiles)
}

prepare_outputs() {
  local rel

  if [[ "$preview" == "true" ]]; then
    return 0
  fi

  for rel in "${active_writable_dirs[@]}"; do
    ensure_dir_rw "$project/$rel"
  done

  for rel in "${active_empty_writable_files[@]}"; do
    ensure_file_rw "$project/$rel" ""
  done

  for rel in "${active_json_writable_files[@]}"; do
    ensure_file_rw "$project/$rel" $'{}\n'
  done

  for rel in "${active_toml_writable_files[@]}"; do
    ensure_file_rw "$project/$rel" ""
  done

  for rel in "${active_yaml_writable_files[@]}"; do
    ensure_file_rw "$project/$rel" $'{}\n'
  done
}

bind_scope_reads() {
  local rel

  for rel in "${active_read_dirs[@]}"; do
    bind_dir_if_exists ro "$project/$rel"
  done

  for rel in "${active_read_files[@]}"; do
    bind_file_if_exists ro "$project/$rel"
  done
}

add_preview_data_file() {
  local rel="$1"
  local default_content="${2-}"
  local tmp fd

  tmp="$(mktemp)"
  printf '%s' "$default_content" > "$tmp"
  exec {fd}<"$tmp"
  rm -f -- "$tmp"
  preview_data_fds+=("$fd")

  bwrap_args+=(--perms 0644 --bind-data "$fd" "$project/$rel")
}

bind_preview_file() {
  local rel="$1"
  local default_content="${2-}"

  if [[ -e "$project/$rel" ]]; then
    bind_file_if_exists ro "$project/$rel"
  else
    add_preview_data_file "$rel" "$default_content"
  fi
}

bind_preview_outputs() {
  local rel

  for rel in "${active_writable_dirs[@]}"; do
    if [[ -e "$project/$rel" ]]; then
      bind_dir_if_exists ro "$project/$rel"
    else
      bwrap_args+=(--perms 0555 --dir "$project/$rel")
    fi
  done

  for rel in "${active_empty_writable_files[@]}"; do
    bind_preview_file "$rel" ""
  done

  for rel in "${active_json_writable_files[@]}"; do
    bind_preview_file "$rel" $'{}\n'
  done

  for rel in "${active_toml_writable_files[@]}"; do
    bind_preview_file "$rel" ""
  done

  for rel in "${active_yaml_writable_files[@]}"; do
    bind_preview_file "$rel" $'{}\n'
  done
}

bind_rw_outputs() {
  local rel

  for rel in "${active_writable_dirs[@]}"; do
    bind_dir_if_exists rw "$project/$rel"
  done

  for rel in "${active_writable_files[@]}"; do
    bind_file_if_exists rw "$project/$rel"
  done
}

cmd="$(first_command "$@")"

case "$cmd" in
  ""|help|init|generate|gitignore|import|convert)
    ;;
  fetch|install|update|mcp)
    die "network-capable rulesync command is not allowed in this jail: $cmd"
    ;;
  *)
    die "unsupported rulesync command in this strict wrapper: $cmd"
    ;;
esac

reject_forbidden_options "$@"

project="$(project_root_from_cwd)"
[[ -d "$project" ]] || die "project root does not exist: $project"
reject_symlink "$project"

load_policy

extra_source_files=()
extra_source_mount_dirs=()
append_custom_config_inputs "$@"

for rel in "${rulesync_source_dirs[@]}"; do
  check_existing_dir "$project/$rel"
done

for rel in "${rulesync_source_files[@]}"; do
  check_existing_regular_file "$project/$rel"
done

case "$cmd" in
  init)
    apply_init_template "$@"
    exit 0
    ;;
  ""|help)
    scope_json='{"preview":false,"emptyFiles":[],"jsonFiles":[],"tomlFiles":[],"yamlFiles":[],"dirs":[],"readFiles":[],"readDirs":[],"mountDirs":[]}'
    ;;
  *)
    scope_json="$(run_scope_probe "$@")"
    ;;
esac

mapfile -t active_empty_writable_files < <(read_scope_array emptyFiles)
mapfile -t active_json_writable_files < <(read_scope_array jsonFiles)
mapfile -t active_toml_writable_files < <(read_scope_array tomlFiles)
mapfile -t active_yaml_writable_files < <(read_scope_array yamlFiles)
mapfile -t active_writable_dirs < <(read_scope_array dirs)
mapfile -t active_read_files < <(read_scope_array readFiles)
mapfile -t active_read_dirs < <(read_scope_array readDirs)
mapfile -t active_mount_dirs < <(read_scope_array mountDirs)
preview="$("$JQ" -r '.preview // false' <<< "$scope_json")"

active_writable_files=(
  "${active_empty_writable_files[@]}"
  "${active_json_writable_files[@]}"
  "${active_toml_writable_files[@]}"
  "${active_yaml_writable_files[@]}"
)

for rel in "${rulesync_source_mount_dirs[@]}" "${extra_source_mount_dirs[@]}"; do
  append_unique active_mount_dirs "$rel"
done

prepare_outputs

base_bwrap_args

for rel in "${active_mount_dirs[@]}"; do
  bwrap_args+=(--perms 0555 --dir "$project/$rel")
done

append_store_closure
bind_source_inputs
bind_scope_reads
preview_data_fds=()
if [[ "$preview" == "true" ]]; then
  bind_preview_outputs
else
  bind_rw_outputs
fi
protect_existing_never_writable

bwrap_args+=(--chdir "$project")
bwrap_args+=("$RULESYNC_UNWRAPPED/bin/rulesync" "$@")

exec "$BWRAP" "${bwrap_args[@]}"
