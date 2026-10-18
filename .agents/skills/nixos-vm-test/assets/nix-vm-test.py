#!/usr/bin/env python3
"""Execute a flake's NixOS QEMU test on a prepared SSH/Nix/KVM executor."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
from urllib.parse import quote, urlencode


HOST = re.compile(r"(?:[A-Za-z0-9_][A-Za-z0-9_.-]*@)?[A-Za-z0-9_][A-Za-z0-9_.-]*\Z")
PROBE = r"""
set -eu
printf 'os\t%s\n' "$(uname -s)"
printf 'machine\t%s\n' "$(uname -m)"
find_tool() {
  candidate=$(command -v "$1" || true)
  case "$candidate" in
    /*) if [ -f "$candidate" ] && [ -x "$candidate" ]; then
      printf '%s' "$candidate"
      return
    fi ;;
  esac
  for p in /run/current-system/sw/bin /nix/var/nix/profiles/default/bin; do
    if [ -f "$p/$1" ] && [ -x "$p/$1" ]; then
      printf '%s' "$p/$1"
      return
    fi
  done
}
for tool in nix nix-daemon nix-store; do
  printf '%s\t%s\n' "$tool" "$(find_tool "$tool")"
done
if [ -c /dev/kvm ]; then printf 'kvm\tyes\n'; else printf 'kvm\tno\n'; fi
if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
  printf 'kvm-user-rw\tyes\n'
else
  printf 'kvm-user-rw\tno\n'
fi
"""
RUN_DRIVER = r"""
set -eu
umask 077
nix_store=$1
driver=$2
mode=$3
work=$(mktemp -d "$HOME/nix-vm-test.XXXXXXXX")
printf 'Remote work directory: %s\n' "$work" >&2
child=
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ -n "$child" ]; then
    kill -TERM "$child" 2>/dev/null || true
    wait "$child" 2>/dev/null || true
  fi
  rm -f "$work/driver"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
"$nix_store" --store daemon --realise "$driver" \
  --add-root "$work/driver" --indirect >/dev/null
mkdir "$work/tmp" "$work/log"
cd "$work"
export TMPDIR="$work/tmp" LOGFILE="$work/driver.xml"
set -- -o "$work/log"
if [ "$mode" = debug ]; then set -- "$@" --interactive; fi
exec 3<&0
"$driver/bin/nixos-test-driver" "$@" <&3 &
child=$!
status=0
wait "$child" || status=$?
child=
exit "$status"
"""


class Failure(Exception):
    pass


def note(message: str) -> None:
    print(f"nix-vm-test: {message}", file=sys.stderr, flush=True)


def interrupted(_signum: int, _frame: object) -> None:
    raise KeyboardInterrupt


def positive(value: str) -> int:
    number = int(value)
    if number < 1:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def arguments(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["check", "build", "rebuild", "run", "debug"])
    parser.add_argument(
        "test",
        nargs="?",
        help="complete flake test attribute, e.g. .#checks.x86_64-linux.vm",
    )
    parser.add_argument(
        "--host", required=True, help="[user@]host or SSH alias; use an alias for IPv6"
    )
    parser.add_argument("--port", type=positive)
    parser.add_argument("--ssh-config", type=Path)
    parser.add_argument("--known-hosts", type=Path)
    auth = parser.add_mutually_exclusive_group(required=True)
    auth.add_argument("--password")
    auth.add_argument("--password-file", type=Path)
    auth.add_argument(
        "--identity-file", type=Path, help="private key without a passphrase"
    )
    args = parser.parse_args(argv)
    if not HOST.fullmatch(args.host):
        parser.error("invalid SSH target; use [user@]hostname or an SSH alias")
    if args.port and args.port > 65535:
        parser.error("port must be between 1 and 65535")
    if args.mode != "check" and not args.test:
        parser.error("this mode requires a complete flake test attribute")
    if args.test and (
        args.test.startswith("-")
        or "#" not in args.test
        or not args.test.split("#", 1)[1]
        or "^" in args.test
    ):
        parser.error("use one complete flake test attribute without output selectors")
    for name in ("ssh_config", "known_hosts", "password_file", "identity_file"):
        path = getattr(args, name)
        if path is not None:
            path = path.expanduser().resolve()
            if not path.is_file() or not os.access(path, os.R_OK):
                parser.error(f"{name.replace('_', '-')} must be a readable file")
            setattr(args, name, path)
    if args.mode == "debug" and not (sys.stdin.isatty() and sys.stdout.isatty()):
        parser.error(
            "debug requires an interactive terminal (authentication is still non-interactive)"
        )
    return args


def command(
    argv: list[str],
    *,
    capture: bool = False,
    env: dict | None = None,
    timeout: int | None = None,
) -> str:
    result = subprocess.run(
        argv,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        env=env,
        timeout=timeout,
        check=False,
    )
    if result.returncode:
        # Do not print argv: a caller may have supplied authentication material.
        raise Failure(f"{Path(argv[0]).name} exited with status {result.returncode}")
    return result.stdout if capture else ""


class Connection:
    def __init__(self, args: argparse.Namespace, directory: Path):
        self.args = args
        self.control = directory / "ssh.sock"
        self.options = [
            "-o",
            f"ControlPath={self.control}",
            "-o",
            "IdentityAgent=none",
            "-o",
            "ForwardAgent=no",
            "-o",
            "ForwardX11=no",
            "-o",
            "ClearAllForwardings=yes",
            "-o",
            "RemoteCommand=none",
            "-o",
            "StrictHostKeyChecking=yes",
            "-o",
            "UpdateHostKeys=no",
            "-o",
            "ConnectTimeout=10",
            "-o",
            "ConnectionAttempts=1",
            "-o",
            "ServerAliveInterval=15",
            "-o",
            "ServerAliveCountMax=3",
        ]
        if args.port:
            self.options += ["-p", str(args.port)]
        if args.ssh_config:
            self.options += ["-F", str(args.ssh_config)]
        if args.known_hosts:
            # OpenSSH parses this option as a list of paths.
            path = str(args.known_hosts).replace("\\", "\\\\").replace('"', '\\"')
            self.options += ["-o", f'UserKnownHostsFile="{path}"']
        self.slave = [
            *self.options,
            "-o",
            "ControlMaster=no",
            "-o",
            "BatchMode=yes",
            "-o",
            "ProxyCommand=false",
        ]
        self.env = dict(os.environ, NIX_SSHOPTS=shlex.join(self.slave))
        self.env.pop("SSHPASS", None)

    def open(self) -> None:
        args = self.args
        env = dict(self.env)
        auth = ["-o", "NumberOfPasswordPrompts=1"]
        prefix = []
        if args.identity_file:
            auth += [
                "-o",
                "BatchMode=yes",
                "-o",
                "IdentitiesOnly=yes",
                "-o",
                "PreferredAuthentications=publickey",
                "-i",
                str(args.identity_file),
            ]
        else:
            auth += [
                "-o",
                "BatchMode=no",
                "-o",
                "PubkeyAuthentication=no",
                "-o",
                "PreferredAuthentications=password,keyboard-interactive",
            ]
            if args.password_file:
                prefix = ["sshpass", "-f", str(args.password_file)]
            else:
                env["SSHPASS"] = args.password
                prefix = ["sshpass", "-e"]
        command(
            [
                *prefix,
                "ssh",
                *self.options,
                *auth,
                "-M",
                "-N",
                "-f",
                "-o",
                "ControlPersist=no",
                args.host,
            ],
            env=env,
            timeout=30,
        )
        command(
            ["ssh", *self.slave, "-O", "check", args.host], env=self.env, timeout=10
        )

    def close(self) -> None:
        if not self.control.exists():
            return
        command(
            ["ssh", *self.slave, "-O", "exit", self.args.host],
            env=self.env,
            timeout=5,
        )

    def remote(
        self, argv: list[str], *, capture: bool = True, tty: bool = False
    ) -> str:
        return command(
            [
                "ssh",
                *self.slave,
                "-t" if tty else "-T",
                self.args.host,
                shlex.join(argv),
            ],
            capture=capture,
            env=self.env,
        )


def remote_store(host: str, nix_daemon: str) -> str:
    # SSH passes remote-program through a shell. Nix URLs decode %20, not '+'.
    return f"ssh-ng://{host}?" + urlencode(
        {
            "remote-program": shlex.quote(nix_daemon),
            "remote-store": "daemon",
            "max-connections": "1",
        },
        quote_via=quote,
    )


def error_message(exc: BaseException) -> str:
    # TimeoutExpired includes argv, which can contain authentication material.
    return (
        "command timed out" if isinstance(exc, subprocess.TimeoutExpired) else str(exc)
    )


def visible_settings(connection: Connection, nix: str) -> dict:
    if not nix:
        note("SSH-user Nix configuration unavailable: nix was not found")
        return {}
    try:
        settings = json.loads(connection.remote([nix, "config", "show", "--json"]))
        fields = (
            "system",
            "extra-platforms",
            "sandbox",
            "sandbox-fallback",
            "system-features",
            "builders",
            "max-jobs",
            "cores",
        )
        values = {key: settings[key]["value"] for key in fields if key in settings}
    except (
        Failure,
        OSError,
        ValueError,
        TypeError,
        KeyError,
        subprocess.TimeoutExpired,
    ) as exc:
        note(
            f"SSH-user Nix configuration unavailable: {error_message(exc)}; continuing"
        )
        return {}
    note("SSH-user Nix configuration (diagnostic only): " + json.dumps(values))
    return values


def preflight(connection: Connection) -> tuple[str, dict, dict]:
    output = connection.remote(["sh", "-c", PROBE])
    rows = [line.split("\t") for line in output.splitlines()]
    keys = {"os", "machine", "nix", "nix-daemon", "nix-store", "kvm", "kvm-user-rw"}
    if (
        len(rows) != len(keys)
        or any(len(row) != 2 for row in rows)
        or {row[0] for row in rows} != keys
    ):
        raise Failure("unexpected SSH probe output; check login-shell stdout")
    probe = dict(rows)
    errors = []
    if probe["os"] != "Linux":
        errors.append("executor must run Linux")
    if not probe["nix-daemon"]:
        errors.append("remote nix-daemon was not found")
    if connection.args.mode in {"run", "debug"} and not probe["nix-store"]:
        errors.append("remote nix-store is required for direct driver execution")
    if probe["kvm"] != "yes":
        errors.append("remote /dev/kvm is missing")
    if probe["kvm-user-rw"] != "yes":
        note(
            "SSH user cannot read/write /dev/kvm; run/debug are unavailable (build-user access is separate)"
        )
        if connection.args.mode in {"run", "debug"}:
            errors.append("SSH user needs KVM access for direct driver execution")
    if errors:
        raise Failure("; ".join(errors))
    store = remote_store(connection.args.host, probe["nix-daemon"])
    command(
        ["nix", "store", "info", "--store", store, "--json"],
        capture=True,
        env=connection.env,
    )
    note(
        f"executor {connection.args.host}: {probe['machine']}, daemon reachable through the selected SSH store"
    )
    note(
        "daemon policy needs administrator confirmation; a fresh test verifies VM execution, not sandbox policy"
    )
    return store, probe, visible_settings(connection, probe["nix"])


def execute(args: argparse.Namespace, connection: Connection) -> None:
    store, probe, settings = preflight(connection)
    if args.test:
        metadata = json.loads(
            command(
                [
                    "nix",
                    "eval",
                    "--store",
                    "auto",
                    "--no-update-lock-file",
                    "--json",
                    "--apply",
                    "t: { system = t.system; forceAccel = t.config.qemu.forceAccel or false; "
                    "containers = builtins.attrNames (t.config.containers or {}); }",
                    args.test,
                ],
                capture=True,
                env=connection.env,
            )
        )
        if not isinstance(metadata, dict):
            raise Failure("unexpected test metadata")
        if metadata.get("forceAccel") is not True:
            raise Failure(
                "test must expose config.qemu.forceAccel=true; inspect its runNixOSTest definition"
            )
        if metadata.get("containers") != []:
            raise Failure(
                "systemd-nspawn containers are outside this QEMU test workflow"
            )
        system = metadata.get("system")
        note(f"test execution system: {system}; executor uname: {probe['machine']}")
        if settings.get("system") and system != settings["system"]:
            note(
                f"test system {system!r} differs from SSH-user config {settings['system']!r}; the daemon will determine build compatibility"
            )
    if args.mode == "check":
        print(
            "Connection/device checks passed; daemon policy and VM execution not verified."
        )
        return
    build = [
        "nix",
        "build",
        "--eval-store",
        "auto",
        "--store",
        store,
        "--no-update-lock-file",
        "--no-link",
        "--print-out-paths",
        "-L",
    ]
    if args.mode in {"build", "rebuild"}:
        note(
            "build may reuse a cached result"
            if args.mode == "build"
            else "rebuild compares outputs; a mismatch is distinct from a failed test assertion"
        )
        command(
            [*build, *(["--rebuild"] if args.mode == "rebuild" else []), args.test],
            env=connection.env,
        )
        return
    paths = command(
        [*build, f"{args.test}.driver"], capture=True, env=connection.env
    ).splitlines()
    if len(paths) != 1 or not PurePosixPath(paths[0]).is_absolute():
        raise Failure("expected exactly one absolute driver path")
    driver = paths[0]
    note("executing the remote driver as the SSH user, outside the Nix build sandbox")
    connection.remote(
        ["sh", "-c", RUN_DRIVER, "nix-vm-test", probe["nix-store"], driver, args.mode],
        capture=False,
        tty=args.mode == "debug",
    )


def main(argv: list[str] | None = None) -> int:
    args = arguments(argv)
    required = ["ssh", "nix"] + ([] if args.identity_file else ["sshpass"])
    missing = [name for name in required if shutil.which(name) is None]
    if missing:
        note(
            "missing client tools in the current project environment: "
            + ", ".join(missing)
        )
        return 1
    # A short system-temporary path avoids Unix socket path-length limits.
    directory = Path(tempfile.mkdtemp(prefix="nvt-", dir="/tmp"))
    connection = Connection(args, directory)
    old_term = signal.signal(signal.SIGTERM, interrupted)
    status = 0
    try:
        connection.open()
        execute(args, connection)
    except KeyboardInterrupt:
        note(
            "interrupted; inspect reported remote work directories if remote cleanup could not finish"
        )
        status = 130
    except (Failure, OSError, ValueError, subprocess.TimeoutExpired) as exc:
        note(error_message(exc))
        status = 1
    finally:
        try:
            connection.close()
            shutil.rmtree(directory)
        except (Failure, OSError, subprocess.TimeoutExpired, KeyboardInterrupt) as exc:
            note(
                f"cleanup failed: {error_message(exc)}; inspect retained directory {directory}"
            )
            if not status:
                status = 130 if isinstance(exc, KeyboardInterrupt) else 1
        finally:
            signal.signal(signal.SIGTERM, old_term)
    return status


if __name__ == "__main__":
    sys.exit(main())
