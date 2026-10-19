#!/usr/bin/env python3
"""Execute a flake's NixOS QEMU test on a prepared SSH/Nix/KVM executor."""

from __future__ import annotations

import argparse
import contextlib
import ipaddress
import json
import os
from pathlib import Path, PurePosixPath
import re
import selectors
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid
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
for tool in nix nix-daemon nix-store python3; do
  printf '%s\t%s\n' "$tool" "$(find_tool "$tool")"
done
if [ -c /dev/kvm ]; then printf 'kvm\tyes\n'; else printf 'kvm\tno\n'; fi
if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
  printf 'kvm-user-rw\tyes\n'
else
  printf 'kvm-user-rw\tno\n'
fi
"""
GRACE_SECONDS = 30
KILL_SECONDS = 5
RUNTIME = None


class Failure(Exception):
    pass


class TimedOut(Failure):
    pass


def note(message: str) -> None:
    data = f"nix-vm-test: {message}\n".encode()
    if RUNTIME:
        RUNTIME.emit(data, sys.stderr)
    else:
        sys.stderr.write(data.decode())
        sys.stderr.flush()


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
    parser.add_argument(
        "--timeout",
        type=positive,
        help="Seconds for the whole invocation; default: unlimited",
    )
    parser.add_argument(
        "--build-timeout",
        type=positive,
        help="Seconds per remote Nix builder; default: executor policy",
    )
    parser.add_argument(
        "--keep-failed",
        action="store_true",
        help="Retain failed Nix build directories on the executor",
    )
    parser.add_argument(
        "--log-dir",
        type=Path,
        help="Parent for a new run directory; default: system temporary directory",
    )
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


def proc_identity(pid: int) -> str:
    """Linux process birth identity, including the boot across executor restarts."""
    fields = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
    return (
        Path("/proc/sys/kernel/random/boot_id").read_text().strip() + ":" + fields[19]
    )


def group_members(pgid: int) -> list[int]:
    if Path("/proc/self/stat").exists():
        members = []
        for path in Path("/proc").glob("[0-9]*/stat"):
            try:
                fields = path.read_text().rsplit(")", 1)[1].split()
                if int(fields[2]) == pgid and fields[0] != "Z":
                    members.append(int(path.parent.name))
            except (OSError, ValueError, IndexError):
                continue
        return members
    try:
        os.killpg(pgid, 0)
        return [pgid]
    except ProcessLookupError:
        return []


def stop_group(
    process: subprocess.Popen, grace=GRACE_SECONDS, kill_wait=KILL_SECONDS
) -> list[int]:
    """Bounded cleanup of the session created for this command, including children."""
    for sig, seconds in ((signal.SIGTERM, grace), (signal.SIGKILL, kill_wait)):
        process.poll()
        if not group_members(process.pid):
            return []
        with contextlib.suppress(ProcessLookupError):
            os.killpg(process.pid, sig)
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            process.poll()
            if not group_members(process.pid):
                return []
            time.sleep(0.05)
    return group_members(process.pid)


class Runtime:
    def __init__(self, directory: Path, timeout=None):
        self.directory = directory
        self.log = (directory / "run.log").open("ab", buffering=0)
        self.deadline = time.monotonic() + timeout if timeout else None
        self.cleanup_complete = True
        self.state = None

    def emit(self, data: bytes, stream=None):
        self.log.write(data)
        if stream is not None:
            try:
                if hasattr(stream, "buffer"):
                    stream.buffer.write(data)
                else:
                    stream.write(data.decode(errors="replace"))
                stream.flush()
            except BrokenPipeError:
                # Keep evidence and finish cleanup if the SSH channel disappears.
                pass

    def remaining(self):
        if self.deadline is None:
            return None
        seconds = self.deadline - time.monotonic()
        if seconds <= 0:
            raise TimedOut("invocation timed out")
        return seconds

    def save_state(self, **updates):
        if self.state is not None:
            self.state.update(updates)
            path = self.directory / "state.json"
            temporary = path.with_suffix(".new")
            temporary.write_text(json.dumps(self.state, indent=2) + "\n")
            temporary.replace(path)

    def run(
        self,
        argv,
        *,
        capture=False,
        quiet=False,
        env=None,
        timeout=None,
        interactive=False,
        recovery=False,
    ):
        end = time.monotonic() + timeout if timeout else None
        if not recovery and self.deadline is not None:
            self.remaining()
            end = min(end, self.deadline) if end else self.deadline
        output = bytearray()
        # Interactive commands keep their controlling terminal. Direct drivers
        # write driver.xml/log themselves; the outer SSH channel is still logged.
        process = subprocess.Popen(
            argv,
            env=env,
            start_new_session=not interactive,
            process_group=0 if interactive else None,
            stdout=None if interactive else subprocess.PIPE,
            stderr=None if interactive else subprocess.PIPE,
        )
        previous_ttou = None
        try:
            if interactive:
                previous_ttou = signal.signal(signal.SIGTTOU, signal.SIG_IGN)
                os.tcsetpgrp(sys.stdin.fileno(), process.pid)
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(process.pid, signal.SIGCONT)
            self.save_state(child_pid=process.pid, child_pgid=process.pid)
            with selectors.DefaultSelector() as selector:
                if not interactive:
                    selector.register(process.stdout, selectors.EVENT_READ, "stdout")
                    selector.register(process.stderr, selectors.EVENT_READ, "stderr")
                while selector.get_map() or process.poll() is None:
                    if end and time.monotonic() >= end:
                        raise TimedOut("command timed out")
                    events = selector.select(0.1) if selector.get_map() else []
                    if not events and process.poll() is not None:
                        break
                    if not selector.get_map():
                        time.sleep(0.05)
                    for key, _ in events:
                        data = os.read(key.fileobj.fileno(), 65536)
                        if not data:
                            selector.unregister(key.fileobj)
                            continue
                        if key.data == "stdout" and capture:
                            output.extend(data)
                        if key.data == "stderr" or not quiet:
                            stream = (
                                sys.stderr
                                if key.data == "stderr"
                                else (None if capture else sys.stdout)
                            )
                            self.emit(data, stream)
            if process.returncode:
                if process.returncode == 124:
                    raise TimedOut("remote invocation timed out")
                raise Failure(
                    f"{Path(argv[0]).name} exited with status {process.returncode}"
                )
            return output.decode(errors="replace") if capture else ""
        finally:
            handlers = {
                sig: signal.signal(sig, signal.SIG_IGN)
                for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)
            }
            try:
                remaining = stop_group(process)
            finally:
                for sig, handler in handlers.items():
                    signal.signal(sig, handler)
            if previous_ttou is not None:
                with contextlib.suppress(OSError):
                    os.tcsetpgrp(sys.stdin.fileno(), os.getpgrp())
                signal.signal(signal.SIGTTOU, previous_ttou)
            if remaining:
                self.cleanup_complete = False
                note(
                    f"cleanup incomplete: command {process.pid}, remaining PIDs {remaining}"
                )
            self.save_state(
                child_pid=None if not remaining else process.pid,
                cleanup_complete=self.cleanup_complete,
            )
            for pipe in (process.stdout, process.stderr):
                if pipe:
                    pipe.close()


def command(argv, **kwargs) -> str:
    return RUNTIME.run(argv, **kwargs)


def remote_job(job: dict) -> int:
    global RUNTIME
    os.umask(0o077)
    work = Path.home() / job["id"]
    work.mkdir()
    os.chdir(work)
    RUNTIME = Runtime(work, job["timeout"])
    RUNTIME.state = {
        "pid": os.getpid(),
        "identity": proc_identity(os.getpid()),
        "finished": False,
    }
    RUNTIME.save_state(
        phase="build",
        cleanup_scope="Nix client process group; sandbox workers are owned by the daemon",
    )
    note(f"remote work directory: {work}")
    for sig in (signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, interrupted)
    status = 0
    try:
        build = [
            job["nix"],
            "build",
            "--store",
            "daemon",
            "--builders",
            "",
            "--no-link",
            "--print-out-paths",
            "-L",
            "--keep-failed" if job["keep_failed"] else "--no-keep-failed",
        ]
        if job["build_timeout"]:
            build += ["--timeout", str(job["build_timeout"])]
        if job["mode"] == "rebuild":
            build += ["--rebuild"]
        paths = command([*build, job["drv"] + "^*"], capture=True).splitlines()
        if job["mode"] in {"build", "rebuild"}:
            for path in paths:
                print(path, flush=True)
        else:
            if len(paths) != 1 or not PurePosixPath(paths[0]).is_absolute():
                raise Failure("expected exactly one absolute driver path")
            driver = paths[0]
            command(
                [
                    job["nix_store"],
                    "--store",
                    "daemon",
                    "--realise",
                    driver,
                    "--add-root",
                    str(work / "driver"),
                    "--indirect",
                ],
                capture=True,
            )
            (work / "tmp").mkdir()
            (work / "log").mkdir()
            RUNTIME.save_state(
                phase="driver",
                cleanup_scope="driver process group, including QEMU children",
            )
            args = [driver + "/bin/nixos-test-driver", "-o", str(work / "log")]
            if job["mode"] == "debug":
                args.append("--interactive")
            command(
                args,
                env=dict(
                    os.environ,
                    TMPDIR=str(work / "tmp"),
                    XDG_RUNTIME_DIR=str(work / "tmp"),
                    LOGFILE=str(work / "driver.xml"),
                ),
                interactive=job["mode"] == "debug",
            )
    except TimedOut as error:
        note(str(error))
        status = 124
    except KeyboardInterrupt:
        note("remote job interrupted")
        status = 130
    except (Failure, OSError, ValueError) as error:
        note(str(error))
        status = 1
    finally:
        if RUNTIME.cleanup_complete:
            (work / "driver").unlink(missing_ok=True)
        else:
            status = status or 1
        RUNTIME.save_state(
            finished=True, exit_status=status, cleanup_complete=RUNTIME.cleanup_complete
        )
        note(f"remote result: {work / 'state.json'}; text log: {work / 'run.log'}")
        RUNTIME.log.close()
        RUNTIME = None
    return status


def cancel_remote(job_id: str) -> int:
    path = Path.home() / job_id / "state.json"
    state = json.loads(path.read_text())
    if not state["finished"]:
        try:
            if proc_identity(state["pid"]) != state["identity"]:
                raise Failure(
                    "remote supervisor identity changed; inspect the retained state"
                )
            os.kill(state["pid"], signal.SIGTERM)
        except ProcessLookupError:
            pass
        end = time.monotonic() + GRACE_SECONDS + KILL_SECONDS + 3
        while time.monotonic() < end:
            state = json.loads(path.read_text())
            if state["finished"]:
                break
            time.sleep(0.1)
    print(json.dumps(state), flush=True)
    return 0 if state["finished"] and state["cleanup_complete"] else 1


class Connection:
    def __init__(self, args: argparse.Namespace, directory: Path):
        self.args = args
        self.control = directory / "ssh.sock"
        self.master = None
        self.job = None
        self.python = None
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

    def configure(self):
        config = command(
            ["ssh", *self.options, "-T", "-G", self.args.host],
            capture=True,
            quiet=True,
            timeout=10,
        )
        host = next(
            line.split(None, 1)[1]
            for line in config.splitlines()
            if line.startswith("hostname ")
        )
        # Resolve the effective HostName, including SSH aliases and internal DNS.
        resolver = "import json,socket,sys; print(json.dumps(sorted({a[4][0] for a in socket.getaddrinfo(sys.argv[1],None)})))"
        try:
            addresses = json.loads(
                command(
                    [sys.executable, "-c", resolver, host],
                    capture=True,
                    quiet=True,
                    timeout=10,
                )
            )
        except Failure as error:
            if isinstance(error, TimedOut):
                raise
            addresses = []
        networks = [
            ipaddress.ip_network(net)
            for net in (
                "10.0.0.0/8",
                "172.16.0.0/12",
                "192.168.0.0/16",
                "127.0.0.0/8",
                "169.254.0.0/16",
                "fc00::/7",
                "fe80::/10",
                "::1/128",
            )
        ]
        private = bool(addresses) and all(
            any(ipaddress.ip_address(ip) in net for net in networks) for ip in addresses
        )
        if private:
            # Command-line options are first-value-wins: override known-hosts
            # configuration for private targets, including replaced snapshots.
            self.options = [
                "-o",
                "StrictHostKeyChecking=no",
                "-o",
                "UserKnownHostsFile=/dev/null",
                "-o",
                "GlobalKnownHostsFile=/dev/null",
                *self.options,
            ]
            note(f"private SSH target {host}: accepting its host key")
        else:
            self.options = ["-o", "StrictHostKeyChecking=yes", *self.options]
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
        self.configure()
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
        self.master = subprocess.Popen(
            [
                *prefix,
                "ssh",
                *self.options,
                *auth,
                "-M",
                "-N",
                "-o",
                "ControlPersist=no",
                args.host,
            ],
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=RUNTIME.log,
            stderr=RUNTIME.log,
            start_new_session=True,
        )
        end = time.monotonic() + 30
        while not self.control.exists():
            RUNTIME.remaining()
            if self.master.poll() is not None:
                raise Failure(
                    f"SSH master exited {self.master.returncode}; see {RUNTIME.directory / 'run.log'}"
                )
            if time.monotonic() >= end:
                raise TimedOut("SSH master startup timed out")
            time.sleep(0.1)
        command(
            ["ssh", *self.slave, "-O", "check", args.host], env=self.env, timeout=10
        )

    def close(self) -> None:
        try:
            if self.control.exists():
                command(
                    ["ssh", *self.slave, "-O", "exit", self.args.host],
                    env=self.env,
                    timeout=5,
                    recovery=True,
                )
        finally:
            if self.master:
                remaining = stop_group(self.master, grace=2)
                if remaining:
                    raise Failure(f"SSH master cleanup incomplete: {remaining}")

    def remote(
        self, argv: list[str], *, capture: bool = True, tty: bool = False, **kwargs
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
            **kwargs,
        )

    def job_command(self, action, value):
        # Send the same standard-library implementation; no executor install.
        return [
            self.python,
            "-c",
            Path(__file__).read_text(),
            action,
            json.dumps(value),
        ]

    def cancel(self):
        if not self.job:
            return
        note(f"checking remote cleanup for ~/{self.job}")
        state = json.loads(
            self.remote(
                self.job_command("--cancel-job", self.job),
                timeout=GRACE_SECONDS + KILL_SECONDS + 5,
                recovery=True,
            )
        )
        note(f"remote status: {json.dumps(state)}")


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


def visible_settings(connection: Connection, nix: str) -> dict:
    try:
        settings = json.loads(
            connection.remote([nix, "config", "show", "--json"], quiet=True)
        )
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
    ) as exc:
        if isinstance(exc, TimedOut):
            raise
        note(f"SSH-user Nix configuration unavailable: {exc}; continuing")
        return {}
    note("SSH-user Nix configuration (diagnostic only): " + json.dumps(values))
    return values


def preflight(connection: Connection) -> tuple[str, dict, dict]:
    output = connection.remote(["sh", "-c", PROBE])
    rows = [line.split("\t") for line in output.splitlines()]
    keys = {
        "os",
        "machine",
        "nix",
        "nix-daemon",
        "nix-store",
        "python3",
        "kvm",
        "kvm-user-rw",
    }
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
    for tool in ("nix", "nix-daemon", "python3"):
        if not probe[tool]:
            errors.append(f"remote {tool} was not found")
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
    connection.python = probe["python3"]
    connection.remote(
        [
            connection.python,
            "-c",
            "import sys; assert sys.version_info >= (3, 11), 'Python 3.11 or newer is required'",
        ],
        capture=True,
    )
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
                    "containers = builtins.attrNames (t.config.containers or {}); "
                    + (
                        "drv = t.driver.drvPath; }"
                        if args.mode in {"run", "debug"}
                        else "drv = t.drvPath; }"
                    ),
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
    command(
        [
            "nix",
            "copy",
            "--derivation",
            "--substitute-on-destination",
            "--from",
            "auto",
            "--to",
            store,
            metadata["drv"],
        ],
        env=connection.env,
    )
    connection.job = "nix-vm-test." + uuid.uuid4().hex[:12]
    job = {
        "id": connection.job,
        "nix": probe["nix"],
        "nix_store": probe["nix-store"],
        "mode": args.mode,
        "drv": metadata["drv"],
        "timeout": RUNTIME.remaining(),
        "build_timeout": args.build_timeout,
        "keep_failed": args.keep_failed,
    }
    (RUNTIME.directory / "remote.json").write_text(
        json.dumps({"host": args.host, **job}, indent=2) + "\n"
    )
    note(f"remote job: ~/{connection.job}; build placement: selected executor's daemon")
    if args.mode in {"run", "debug"}:
        note(
            "executing the remote driver as the SSH user, outside the Nix build sandbox"
        )
    elif args.mode == "rebuild":
        note("rebuild compares outputs; a mismatch is distinct from a failed assertion")
    else:
        note("build may reuse a cached result")
    connection.remote(
        connection.job_command("--remote-job", job),
        capture=False,
        tty=args.mode == "debug",
    )


def main(argv: list[str] | None = None) -> int:
    global RUNTIME
    args = arguments(argv)
    required = ["ssh", "nix"] + ([] if args.identity_file else ["sshpass"])
    missing = [name for name in required if shutil.which(name) is None]
    if missing:
        note(
            "missing client tools in the current project environment: "
            + ", ".join(missing)
        )
        return 1
    os.umask(0o077)
    if args.log_dir:
        args.log_dir.mkdir(parents=True, exist_ok=True)
    logs = Path(tempfile.mkdtemp(prefix="nvt-logs-", dir=args.log_dir))
    RUNTIME = Runtime(logs, args.timeout)
    note(f"local text log: {logs / 'run.log'}")
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
    except TimedOut as exc:
        note(str(exc))
        status = 124
    except (Failure, OSError, ValueError) as exc:
        note(str(exc))
        status = 1
    finally:
        try:
            connection.cancel()
        except (Failure, OSError, ValueError, KeyboardInterrupt) as exc:
            note(
                f"remote cleanup unconfirmed: {exc}; inspect ~/{connection.job} and {logs / 'remote.json'}"
            )
            status = status or 1
        try:
            connection.close()
            shutil.rmtree(directory)
        except (Failure, OSError, KeyboardInterrupt) as exc:
            note(f"cleanup failed: {exc}; inspect retained directory {directory}")
            if not status:
                status = 130 if isinstance(exc, KeyboardInterrupt) else 1
        finally:
            signal.signal(signal.SIGTERM, old_term)
            if not RUNTIME.cleanup_complete:
                status = status or 1
            RUNTIME.log.close()
            RUNTIME = None
    return status


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--remote-job":
        sys.exit(remote_job(json.loads(sys.argv[2])))
    if len(sys.argv) > 1 and sys.argv[1] == "--cancel-job":
        sys.exit(cancel_remote(json.loads(sys.argv[2])))
    sys.exit(main())
