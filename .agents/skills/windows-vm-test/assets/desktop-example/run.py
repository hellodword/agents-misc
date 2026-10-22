"""Windows entrypoint: bounded worker, screenshot and owned-process cleanup."""

import argparse
import contextlib
import json
import math
import os
import subprocess
import sys
import time
import traceback
from pathlib import Path


def duration(value):
    result = float(value)
    if not math.isfinite(result) or result < 0:
        raise argparse.ArgumentTypeError("Must be finite and nonnegative")
    return result


def capture(path):
    from PIL import ImageGrab

    ImageGrab.grab().save(path)


def worker(options):
    output = Path(options["output"])
    with (
        (output / "worker.log").open("w", encoding="utf-8") as log,
        contextlib.redirect_stdout(log),
        contextlib.redirect_stderr(log),
    ):
        try:
            from scenario import run

            result = run(options)
            (output / "assertions.json").write_text(
                json.dumps(result, ensure_ascii=False), encoding="utf-8"
            )
            return 0
        except Exception:
            traceback.print_exc()
            capture(output / "failure.png")
            return 1


def supervise(options):
    import win32api
    import win32con
    import win32event
    import win32job
    import win32process

    output = Path(options["output"])
    # Every run owns a new output directory, so stale application files cannot
    # satisfy assertions or replace another run's evidence.
    output.mkdir(parents=True, exist_ok=False)
    request = output / "request.json"
    request.write_text(json.dumps(options), encoding="utf-8")
    report = {
        "passed": False,
        "cleanup_confirmed": False,
        "exit_code": None,
        "reason": None,
    }
    job = win32job.CreateJobObject(None, "")
    process = None

    def launch(argv):
        child, thread, pid, _ = win32process.CreateProcess(
            None,
            subprocess.list2cmdline(argv),
            None,
            None,
            False,
            win32con.CREATE_SUSPENDED | win32con.CREATE_NO_WINDOW,
            None,
            str(Path(__file__).parent.resolve()),
            win32process.STARTUPINFO(),
        )
        try:
            win32job.AssignProcessToJobObject(job, child)
            win32process.ResumeThread(thread)
        except BaseException:
            win32api.TerminateProcess(child, 1)
            child.Close()
            raise
        finally:
            thread.Close()
        return child, pid

    started = time.monotonic()
    try:
        limits = win32job.QueryInformationJobObject(
            job, win32job.JobObjectExtendedLimitInformation
        )
        limits["BasicLimitInformation"]["LimitFlags"] = (
            win32job.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        )
        win32job.SetInformationJobObject(
            job, win32job.JobObjectExtendedLimitInformation, limits
        )
        argv = [
            options["controls_python"],
            str(Path(__file__).resolve()),
            "--worker",
            str(request),
        ]
        process, pid = launch(argv)
        report["worker_pid"] = pid
        remaining = max(0, options["timeout"] - (time.monotonic() - started))
        state = win32event.WaitForSingleObject(
            process, min(int(remaining * 1000), 0xFFFFFFFE)
        )
        if state == win32con.WAIT_TIMEOUT:
            report["reason"] = "timeout"
        else:
            report["exit_code"] = win32process.GetExitCodeProcess(process)
            report["passed"] = report["exit_code"] == 0
            report["reason"] = (
                "completed" if report["passed"] else "assertion_or_worker_error"
            )
        if not report["passed"]:
            capture_process = None
            try:
                # The screenshot helper also belongs to the job: a timed-out
                # venv launcher must not leave its real Python child running.
                capture_process, _ = launch(
                    [
                        options["controls_python"],
                        str(Path(__file__).resolve()),
                        "--capture",
                        str(output / "failure.png"),
                    ]
                )
                if (
                    win32event.WaitForSingleObject(capture_process, 5000)
                    != win32con.WAIT_OBJECT_0
                ):
                    raise TimeoutError("Failure screenshot exceeded five seconds")
                if win32process.GetExitCodeProcess(capture_process) != 0:
                    raise RuntimeError("Failure screenshot process failed")
            except Exception as error:
                report["screenshot_error"] = str(error)
            finally:
                if capture_process is not None:
                    capture_process.Close()
    except Exception as error:
        report["reason"], report["error"] = "supervisor_error", str(error)
    finally:
        try:
            # Also covers an assignment failure before the suspended worker ran.
            if (
                process is not None
                and win32event.WaitForSingleObject(process, 0) != win32con.WAIT_OBJECT_0
            ):
                win32api.TerminateProcess(process, 1)
            win32job.TerminateJobObject(job, 1)
            deadline = time.monotonic() + 5
            while True:
                info = win32job.QueryInformationJobObject(
                    job, win32job.JobObjectBasicAccountingInformation
                )
                if info["ActiveProcesses"] == 0:
                    report["cleanup_confirmed"] = (
                        process is None
                        or win32event.WaitForSingleObject(process, 1000)
                        == win32con.WAIT_OBJECT_0
                    )
                    break
                if time.monotonic() >= deadline:
                    break
                time.sleep(0.05)
        except Exception as error:
            report["cleanup_error"] = str(error)
        finally:
            for handle in (process, job):
                if handle is not None:
                    handle.Close()
        report["elapsed_seconds"] = time.monotonic() - started
        report["passed"] = report["passed"] and report["cleanup_confirmed"]
        (output / "report.json").write_text(
            json.dumps(report, indent=2), encoding="utf-8"
        )
    print(json.dumps(report))
    return 0 if report["passed"] else 1


def main():
    if len(sys.argv) == 3 and sys.argv[1] == "--worker":
        return worker(json.loads(Path(sys.argv[2]).read_text(encoding="utf-8")))
    if len(sys.argv) == 3 and sys.argv[1] == "--capture":
        capture(sys.argv[2])
        return 0
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app", type=Path, required=True)
    parser.add_argument("--app-arg", action="append", default=[])
    parser.add_argument("--controls-python", type=Path, default=Path(sys.executable))
    parser.add_argument("--vision-python", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout", type=duration, default=60)
    parser.add_argument("--input-interval-seconds", type=duration, default=1.0)
    parser.add_argument(
        "--flow", choices=["controls", "vision", "mixed"], default="mixed"
    )
    parser.add_argument("--backend", choices=["win32", "uia"], default="win32")
    args = parser.parse_args()
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    options = vars(args)
    for name in ("app", "controls_python", "vision_python", "output"):
        options[name] = str(options[name].resolve())
    if os.name != "nt":
        parser.error("Run this entrypoint on Windows")
    # The parent needs the controls environment's win32 Job Object bindings too.
    if Path(sys.executable).resolve() != Path(options["controls_python"]):
        parser.error("Launch run.py with --controls-python's interpreter")
    try:
        return supervise(options)
    except Exception as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
