"""Windows test command supervisor, copied into each task's guest directory.

Only Python's standard library is required. Each child starts suspended and is
assigned to a kill-on-close Windows Job Object before its first instruction.
"""

import ctypes
import json
import os
import subprocess
import sys
import threading
import time
import zipfile
from pathlib import Path

FILE_RETRY_SECONDS = 2


def retry_file(operation):
    """Allow short Windows sharing/access conflicts without an unbounded wait."""
    deadline = time.monotonic() + FILE_RETRY_SECONDS
    while True:
        try:
            return operation()
        except PermissionError:
            if time.monotonic() >= deadline:
                raise
            time.sleep(0.05)


def read_json(path):
    return retry_file(lambda: json.loads(path.read_text(encoding="utf-8")))


def write_json(path, value):
    temporary = path.with_suffix(".new")

    def publish():
        temporary.write_text(json.dumps(value), encoding="utf-8")
        temporary.replace(path)

    retry_file(publish)


def read_status(directory):
    # The terminal record is published once, independently of the hot snapshot.
    try:
        return read_json(directory / "result.json")
    except FileNotFoundError:
        return read_json(directory / "status.json")


class InputPipe:
    """Guest-local ordered files feed a pipe without blocking process supervision."""

    def __init__(self, directory):
        self.directory = directory
        reader, self.writer = os.pipe()
        self.reader = os.fdopen(reader, "rb", buffering=0)
        self.state = {"written_sequence": 0, "closed": False, "error": None}
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.feed, daemon=True)

    def start(self):
        self.reader.close()  # The child owns its inherited reader now.
        self.thread.start()

    def feed(self):
        try:
            while not self.stop.is_set():
                sequence = self.state["written_sequence"] + 1
                path = self.directory / f"input-{sequence:08d}.json"
                try:
                    message = read_json(path)
                except FileNotFoundError:
                    self.stop.wait(0.05)
                    continue
                data = message["text"].encode("utf-8")
                while data:
                    data = data[os.write(self.writer, data) :]
                self.state["written_sequence"] = sequence
                if message["eof"]:
                    break
        except (OSError, ValueError, KeyError) as error:
            self.state["error"] = str(error)
        finally:
            os.close(self.writer)
            self.state["closed"] = True

    def close(self):
        self.stop.set()
        self.reader.close()
        if self.thread.ident is not None:
            self.thread.join(timeout=2)
        else:
            os.close(self.writer)
            self.state["closed"] = True


def probe():
    from ctypes import wintypes as w

    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    session = w.DWORD()
    if not kernel.ProcessIdToSessionId(os.getpid(), ctypes.byref(session)):
        raise ctypes.WinError(ctypes.get_last_error())
    user = ctypes.WinDLL("user32", use_last_error=True)
    user.GetProcessWindowStation.restype = w.HANDLE
    user.GetUserObjectInformationW.argtypes = [
        w.HANDLE,
        ctypes.c_int,
        w.LPVOID,
        w.DWORD,
        ctypes.POINTER(w.DWORD),
    ]
    name = ctypes.create_unicode_buffer(256)
    needed = w.DWORD()
    if not user.GetUserObjectInformationW(
        user.GetProcessWindowStation(),
        2,
        name,
        ctypes.sizeof(name),
        ctypes.byref(needed),
    ):
        raise ctypes.WinError(ctypes.get_last_error())
    return {
        "python": sys.version,
        "session_id": session.value,
        "window_station": name.value,
        "interactive": session.value != 0 and name.value.casefold() == "winsta0",
    }


def run(directory):
    import msvcrt
    from ctypes import wintypes as w

    spec = read_json(directory / "command.json")
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)

    class Limits(ctypes.Structure):
        _fields_ = [
            ("PerProcessUserTimeLimit", ctypes.c_int64),
            ("PerJobUserTimeLimit", ctypes.c_int64),
            ("LimitFlags", w.DWORD),
            ("MinimumWorkingSetSize", ctypes.c_size_t),
            ("MaximumWorkingSetSize", ctypes.c_size_t),
            ("ActiveProcessLimit", w.DWORD),
            ("Affinity", ctypes.c_size_t),
            ("PriorityClass", w.DWORD),
            ("SchedulingClass", w.DWORD),
        ]

    class IO(ctypes.Structure):
        _fields_ = [
            (name, ctypes.c_uint64)
            for name in (
                "ReadOperationCount",
                "WriteOperationCount",
                "OtherOperationCount",
                "ReadTransferCount",
                "WriteTransferCount",
                "OtherTransferCount",
            )
        ]

    class Extended(ctypes.Structure):
        _fields_ = [
            ("BasicLimitInformation", Limits),
            ("IoInfo", IO),
            ("ProcessMemoryLimit", ctypes.c_size_t),
            ("JobMemoryLimit", ctypes.c_size_t),
            ("PeakProcessMemoryUsed", ctypes.c_size_t),
            ("PeakJobMemoryUsed", ctypes.c_size_t),
        ]

    class Accounting(ctypes.Structure):
        _fields_ = [
            (name, ctypes.c_int64)
            for name in (
                "TotalUserTime",
                "TotalKernelTime",
                "ThisPeriodTotalUserTime",
                "ThisPeriodTotalKernelTime",
            )
        ] + [
            (name, w.DWORD)
            for name in (
                "TotalPageFaultCount",
                "TotalProcesses",
                "ActiveProcesses",
                "TotalTerminatedProcesses",
            )
        ]

    class Startup(ctypes.Structure):
        _fields_ = (
            [
                ("cb", w.DWORD),
                ("lpReserved", w.LPWSTR),
                ("lpDesktop", w.LPWSTR),
                ("lpTitle", w.LPWSTR),
            ]
            + [
                (name, w.DWORD)
                for name in (
                    "dwX",
                    "dwY",
                    "dwXSize",
                    "dwYSize",
                    "dwXCountChars",
                    "dwYCountChars",
                    "dwFillAttribute",
                    "dwFlags",
                )
            ]
            + [
                ("wShowWindow", w.WORD),
                ("cbReserved2", w.WORD),
                ("lpReserved2", ctypes.POINTER(ctypes.c_byte)),
                ("hStdInput", w.HANDLE),
                ("hStdOutput", w.HANDLE),
                ("hStdError", w.HANDLE),
            ]
        )

    class Process(ctypes.Structure):
        _fields_ = [
            ("hProcess", w.HANDLE),
            ("hThread", w.HANDLE),
            ("dwProcessId", w.DWORD),
            ("dwThreadId", w.DWORD),
        ]

    signatures = {
        "CreateJobObjectW": ([w.LPVOID, w.LPCWSTR], w.HANDLE),
        "SetInformationJobObject": (
            [w.HANDLE, ctypes.c_int, w.LPVOID, w.DWORD],
            w.BOOL,
        ),
        "QueryInformationJobObject": (
            [w.HANDLE, ctypes.c_int, w.LPVOID, w.DWORD, w.LPVOID],
            w.BOOL,
        ),
        "AssignProcessToJobObject": ([w.HANDLE, w.HANDLE], w.BOOL),
        "TerminateJobObject": ([w.HANDLE, w.UINT], w.BOOL),
        "TerminateProcess": ([w.HANDLE, w.UINT], w.BOOL),
        "CreateProcessW": (
            [
                w.LPCWSTR,
                w.LPWSTR,
                w.LPVOID,
                w.LPVOID,
                w.BOOL,
                w.DWORD,
                w.LPVOID,
                w.LPCWSTR,
                ctypes.POINTER(Startup),
                ctypes.POINTER(Process),
            ],
            w.BOOL,
        ),
        "ResumeThread": ([w.HANDLE], w.DWORD),
        "WaitForSingleObject": ([w.HANDLE, w.DWORD], w.DWORD),
        "GetExitCodeProcess": ([w.HANDLE, ctypes.POINTER(w.DWORD)], w.BOOL),
        "CloseHandle": ([w.HANDLE], w.BOOL),
    }
    for name, (arguments, result) in signatures.items():
        function = getattr(kernel, name)
        function.argtypes = arguments
        function.restype = result

    def checked(value):
        if not value:
            raise ctypes.WinError(ctypes.get_last_error())
        return value

    job = None
    child = Process()
    assigned = False
    status = {"state": "running", "cleanup_confirmed": False, "exit_code": None}
    input_pipe = None

    def failed(phase, error):
        previous = status.get("error")
        status.update(
            state="failed",
            error=f"{previous}; {phase}: {error}" if previous else f"{phase}: {error}",
        )

    try:
        job = checked(kernel.CreateJobObjectW(None, None))
        input_pipe = InputPipe(directory) if spec.get("stdin") else None
        write_json(directory / "status.json", status)
        limits = Extended()
        limits.BasicLimitInformation.LimitFlags = (
            0x2000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        )
        checked(
            kernel.SetInformationJobObject(
                job, 9, ctypes.byref(limits), ctypes.sizeof(limits)
            )
        )
        with (
            input_pipe.reader if input_pipe else open(os.devnull, "rb") as stdin,
            (directory / "run.log").open("wb") as output,
        ):
            handles = [
                msvcrt.get_osfhandle(stream.fileno()) for stream in (stdin, output)
            ]
            for handle in handles:
                os.set_handle_inheritable(handle, True)
            startup = Startup()
            startup.cb = ctypes.sizeof(startup)
            startup.dwFlags = 0x100  # STARTF_USESTDHANDLES
            startup.hStdInput, startup.hStdOutput, startup.hStdError = (
                handles[0],
                handles[1],
                handles[1],
            )
            environment = os.environ | spec.get("env", {})
            block = ctypes.create_unicode_buffer(
                "\0".join(
                    f"{key}={value}" for key, value in sorted(environment.items())
                )
                + "\0\0"
            )
            command = ctypes.create_unicode_buffer(
                subprocess.list2cmdline(spec["argv"])
            )
            checked(
                kernel.CreateProcessW(
                    None,
                    command,
                    None,
                    None,
                    True,
                    0x4 | 0x400,
                    block,
                    spec.get("cwd") or str(directory.parent),
                    ctypes.byref(startup),
                    ctypes.byref(child),
                )
            )
            checked(kernel.AssignProcessToJobObject(job, child.hProcess))
            assigned = True
            if kernel.ResumeThread(child.hThread) == 0xFFFFFFFF:
                raise ctypes.WinError(ctypes.get_last_error())
            if input_pipe:
                input_pipe.start()
            started = time.monotonic()
            reason = "exited"
            while True:
                if input_pipe:
                    status["stdin"] = dict(input_pipe.state)
                    write_json(directory / "status.json", status)
                waited = kernel.WaitForSingleObject(child.hProcess, 250)
                if waited == 0:
                    break
                if waited != 0x102:
                    raise ctypes.WinError(ctypes.get_last_error())
                if (directory.parent / "cancel").exists():
                    reason = "cancelled"
                    break
                if time.monotonic() - started > spec["timeout"]:
                    reason = "timeout"
                    break
                if (directory / "run.log").stat().st_size > spec["log_bytes"]:
                    reason = "output_limit"
                    break
            status.update(state="finished", reason=reason)
    except (OSError, ValueError, KeyError, TypeError) as error:
        failed("execution", error)
    finally:
        # File publication must never bypass process-tree cleanup or its readback.
        try:
            if child.hProcess:
                if kernel.WaitForSingleObject(child.hProcess, 0) == 0:
                    code = w.DWORD()
                    checked(
                        kernel.GetExitCodeProcess(child.hProcess, ctypes.byref(code))
                    )
                    status["exit_code"] = code.value
                if assigned:
                    checked(kernel.TerminateJobObject(job, 1))
                    deadline = time.monotonic() + 10
                    while True:
                        accounting = Accounting()
                        checked(
                            kernel.QueryInformationJobObject(
                                job,
                                1,
                                ctypes.byref(accounting),
                                ctypes.sizeof(accounting),
                                None,
                            )
                        )
                        if accounting.ActiveProcesses == 0:
                            status["cleanup_confirmed"] = True
                            break
                        if time.monotonic() >= deadline:
                            break
                        time.sleep(0.1)
                else:
                    checked(kernel.TerminateProcess(child.hProcess, 1))
                    status["cleanup_confirmed"] = (
                        kernel.WaitForSingleObject(child.hProcess, 10000) == 0
                    )
            else:
                status["cleanup_confirmed"] = True
        except OSError as error:
            failed("cleanup", error)
        for handle in (child.hThread, child.hProcess, job):
            if handle:
                kernel.CloseHandle(handle)
        if input_pipe:
            input_pipe.close()
            status["stdin"] = dict(input_pipe.state)
        try:
            with (directory / "run.log").open("r+b") as output:
                output.truncate(min(output.seek(0, 2), spec["log_bytes"]))
        except FileNotFoundError:
            pass
        except OSError as error:
            failed("log", error)
        try:
            write_json(directory / "result.json", status)
        except OSError:
            # Keep result.new for diagnosis if even the independent record fails.
            print(json.dumps(status), file=sys.stderr, flush=True)
            raise


def pack(source, destination):
    source = Path(source)
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        if source.is_file():
            archive.write(source, source.name)
        else:
            for base, dirs, files in os.walk(source, followlinks=False):
                dirs[:] = [
                    name for name in dirs if not (Path(base) / name).is_symlink()
                ]
                for name in files:
                    path = Path(base) / name
                    if (
                        path.is_file()
                        and not path.is_symlink()
                        and path.resolve() != Path(destination).resolve()
                    ):
                        archive.write(path, path.relative_to(source))


if __name__ == "__main__":
    if sys.argv[1] == "probe":
        print(json.dumps(probe()))
    elif sys.argv[1] == "pack":
        pack(sys.argv[2], sys.argv[3])
    elif sys.argv[1] == "publish":
        retry_file(lambda: Path(sys.argv[2]).replace(sys.argv[3]))
    elif sys.argv[1] == "status":
        print(json.dumps(read_status(Path(sys.argv[2]))))
    else:
        run(Path(sys.argv[1]))
