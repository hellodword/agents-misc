# Project-owned desktop automation

Use pywinauto's Win32/UIA backends for exposed controls and Airtest for image matching. Start exploration with ordinary scripts and control inspection. Transfer the working flow into the project's existing framework and command, together with image templates, dependency declarations, assertions and reports. Keep these assets runnable from a Windows console without MCP.

The project owns test behavior. MCP owns disposable clones and power, transfer,
bounded Go command supervision, native input and evidence. Keep controls/vision
interpreter paths in the tested project's environment context and use `command_start`
to probe actual versions and imports. Missing packages can be prepared in the
node's workspace using project dependency tooling.

## Copyable example

Copy the entire `assets/desktop-example` directory from this skill into the tested project's test assets. Its `run.py` accepts an application path, controls and vision interpreters, a new output directory, timeout, input interval, flow and backend. `scenario.py` owns selectors and assertions: adapt it to the application before pointing `--app` at another program. The supplied `fixture.py` is a disposable Win32 application with an Edit, an Apply button, a modal dialog and a painted PNG target.

From a Windows PowerShell console, with prepared interpreters:

```powershell
$controls = 'C:\vm-tools\controls\Scripts\python.exe'
$vision = 'C:\vm-tools\vision\Scripts\python.exe'
& $controls .\run.py --app .\fixture.py --controls-python $controls --vision-python $vision --output C:\results\controls-01 --flow controls --backend win32 --timeout 60
& $controls .\run.py --app .\fixture.py --controls-python $controls --vision-python $vision --output C:\results\vision-01 --flow vision --backend uia --timeout 60
& $controls .\run.py --app .\fixture.py --controls-python $controls --vision-python $vision --output C:\results\mixed-01 --flow mixed --timeout 60 --input-interval-seconds 1
```

Each run requires a new output directory to prevent stale files from satisfying assertions. The scenario enters Chinese text, reads it back, checks that a modal disables its parent, activates Apply by control or image location, and verifies an application-written JSON result. Mixed mode also drags the window and captures a fresh screenshot before matching. Window discovery follows the launched process tree, including Windows venv launcher children. Image matching runs in the vision interpreter as a local child process: one JSON stdin request names the screenshot/template, and one JSON stdout response contains `found` and `point`. Diagnostics use stderr. The controls driver owns all input. See the upstream [pywinauto keyboard API](https://pywinauto.readthedocs.io/en/latest/code/pywinauto.keyboard.html) and [Airtest Template API](https://airtest.readthedocs.io/en/latest/all_module/airtest.core.cv.html).

The outer runner creates its worker suspended, assigns it to a kill-on-close Windows Job Object, then resumes it. Its process deadline terminates blocked UIA calls and the owned application/matcher descendants even without MCP. The failure screenshot helper joins the same job before running, including any venv launcher descendants. Failure capture and termination have bounded cleanup grace (five seconds each). `report.json` records exit code, reason, elapsed time and observed `cleanup_confirmed`; `worker.log`, `assertions.json`, application output and PNGs retain evidence. Exit codes are 0 for passed assertions plus cleanup, 1 for failure and 2 for invalid arguments. Stdin is unused by the public runner; stdout contains the final JSON report. UAC-launched elevated applications are a separate preparation boundary, not assumed descendants of an unelevated Job Object.

For a deliberate blocked-provider case, run with `--backend uia --timeout 10 --app-arg=--block-uia --app-arg=--result --app-arg=C:\results\blocked-application.json`. The fixture blocks `WM_GETOBJECT`; expect runner exit 1, `reason="timeout"`, a failure screenshot and confirmed cleanup. Run a normal case next to verify recovery. This is a failure assertion, not a passing application case.

The input helper serializes physical actions and defaults to one second from one complete action's end to the next start. A typing sequence, click or drag is one action; `0` disables the extra spacing. Condition waits establish readiness; interval sleeps do not. The outer process deadline includes helper waits. Adapt assertions to application state, file content or service results, rather than successful click delivery.

## Dependencies and template provenance

The supplied `.in` files declare the two environments; `.txt` files are tool-generated Windows amd64 Python 3.11.17 locks with hashes. Regenerate each independently with uv (use an available build interpreter with `--python` when cross-resolving):

```powershell
uv pip compile controls.in --python-version 3.11.17 --python-platform x86_64-pc-windows-msvc --generate-hashes -o controls.txt
uv pip compile vision.in --python-version 3.11.17 --python-platform x86_64-pc-windows-msvc --generate-hashes -o vision.txt
uv pip sync --python C:\vm-tools\controls\Scripts\python.exe --require-hashes controls.txt
uv pip check --python C:\vm-tools\controls\Scripts\python.exe
uv pip sync --python C:\vm-tools\vision\Scripts\python.exe --require-hashes vision.txt
uv pip check --python C:\vm-tools\vision\Scripts\python.exe
```

Use the project's own lock/dependency commands when it already has them. `target.png` is synthetic, contains no user data and is an intentional tracked test input. Regenerate it by running `make_template.py` in the locked controls environment; inspect the fixture and matching assertion after any change. Actual application templates belong alongside project tests and should be captured at observed resolution/DPI.

Airtest's Windows dependency pins pywinauto 0.6.3, so it stays isolated from controls' 0.6.9. The old package computes some metadata from its build OS; the vision input explicitly declares its Windows `comtypes`/`pypiwin32` dependencies so cross-resolution is followed by a successful Windows `uv pip check`.

## Elevation, display and firewall cases

Run ordinary application input and UAC input in coordinated phases. Pause the guest driver before requesting elevation; inspect the native VM screenshot, confirm or cancel with VM keyboard/mouse, then resume only after the ordinary desktop returns. Reacquire the window and inspect actual screenshot dimensions and DPI. Avoid concurrent guest physical input during the native phase. Guest libraries do not operate the secure desktop. A guest-only console run needs the equivalent human/executor UAC step; run administrator preparation separately from ordinary tests.

For network feature tests, prepare firewall rules scoped to the executable, protocol and ports. Verify communication while allowed, while blocked and after restoring access; confirm that the server remains alive throughout. Correlate client, application and NIC-capture evidence using request identifiers, and keep policy-transition periods outside the stable observation windows. For inbound tests, use `endpoint_open` for the guest service and connect from an allowed development-container source. Close the endpoint and remove task-specific guest firewall rules when finished.

For prompt-behavior tests, retain the real firewall prompt, exercise the intended accept/cancel choice, then inspect generated rules and actual communication. Disabling the firewall is not an equivalent assertion.

When accepting a target, cover Qt Widgets, Qt Quick, Win32/WPF and a custom-painted application; include control states, modal windows, Chinese input, matching, movement and dragging. Observe 1280×1024, 1920×1080 and 3840×2160 at 100%/150% scaling where supported. Record unsupported or unavailable combinations separately. Exercise UAC accept/cancel, administrator windows and recovery after a blocked UIA call. Move the same assets to another directory and run the same console command to compare reports and tree cleanup. Finish with at least 45 sequential guest commands, observing Guest Control session/process counts and final clone deletion with task `cleanup.state: complete`.
