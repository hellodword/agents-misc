"""Copy into the tested project and replace selectors/assertions for its app."""

import json
import ctypes
import subprocess
import time
from pathlib import Path

from support import Inputs, locate, wait_for


def run(options):
    from PIL import ImageGrab
    import psutil
    from pywinauto import Application, Desktop, keyboard, mouse

    output = Path(options["output"])
    application_result = output / "application.json"
    app = Path(options["app"])
    argv = (
        [options["controls_python"], str(app)]
        if app.suffix.lower() == ".py"
        else [str(app)]
    )
    argv += options["app_arg"] or ["--result", str(application_result)]
    with (output / "application.log").open("w", encoding="utf-8") as log:
        process = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT)

    def owned_window():
        if process.poll() is not None:
            raise RuntimeError(
                f"Application exited {process.returncode}; see application.log"
            )
        owned = {process.pid} | {
            p.pid for p in psutil.Process(process.pid).children(recursive=True)
        }
        matches = [
            w
            for w in Desktop(backend=options["backend"]).windows(
                title="Automation Example"
            )
            if w.process_id() in owned
        ]
        return matches[0] if matches else None

    # Windows venv launchers can delegate to another process. Bind only to a
    # window owned by this launch's process tree, never a same-title external app.
    handle = wait_for(owned_window, timeout=15).handle
    automation = Application(backend=options["backend"]).connect(handle=handle)
    window = automation.window(title="Automation Example")
    window.wait("visible enabled ready", timeout=15)
    inputs = Inputs(options["input_interval_seconds"])
    inputs.perform(window.set_focus)
    edit = (
        window.child_window(control_id=101)
        if options["backend"] == "win32"
        else window.child_window(auto_id="101", control_type="Edit")
    )
    edit.wait("visible enabled", timeout=10)
    inputs.perform(edit.click_input)
    inputs.perform(
        keyboard.send_keys, "中文输入 test", with_spaces=True, vk_packet=True
    )
    wait_for(
        lambda: (
            "中文输入"
            in (edit.get_value() if options["backend"] == "uia" else edit.window_text())
        )
    )

    button_selector = (
        {"control_type": "Button"}
        if options["backend"] == "uia"
        else {"class_name": "Button"}
    )
    inputs.perform(window.child_window(title="Modal", **button_selector).click_input)
    # The fixture's native MessageBox is nested under its owner in the UIA
    # tree. Bind its HWND through Win32, including in the UIA main-window case.
    native = Application(backend="win32").connect(handle=handle)
    modal = native.window(title="Example modal")
    modal.wait("visible enabled", timeout=10)
    assert not native.window(handle=handle).is_enabled(), (
        "Parent HWND must be disabled while the native modal is open"
    )
    confirm = modal.child_window(class_name="Button")
    inputs.perform(confirm.click_input)
    window.wait("enabled", timeout=10)

    if options["flow"] == "controls":
        button = window.child_window(title="Apply", **button_selector)
        inputs.perform(button.click_input)
    else:
        # Vision runs in its own interpreter. Only this driver sends input.
        if options["flow"] == "mixed":
            rect = native.window(handle=handle).rectangle()

            def drag():
                user = ctypes.windll.user32
                dpi = user.GetDpiForWindow(handle)
                border = user.GetSystemMetricsForDpi(
                    33, dpi
                ) + user.GetSystemMetricsForDpi(92, dpi)
                caption = user.GetSystemMetricsForDpi(4, dpi)
                start = (rect.left + 100, rect.top + border + caption // 2)
                mouse.press(coords=start)
                try:
                    for step in range(1, 7):
                        mouse.move(
                            coords=(
                                start[0] + step * 10,
                                start[1] + round(step * 40 / 6),
                            )
                        )
                        time.sleep(0.05)
                finally:
                    mouse.release(coords=(start[0] + 60, start[1] + 40))

            inputs.perform(drag)
            wait_for(lambda: native.window(handle=handle).rectangle().left != rect.left)
        screenshot = output / "before.png"
        ImageGrab.grab().save(screenshot)
        point = locate(
            options["vision_python"], screenshot, Path(__file__).with_name("target.png")
        )
        inputs.perform(mouse.click, coords=point)
    wait_for(application_result.exists)
    actual = json.loads(application_result.read_text(encoding="utf-8"))
    assert actual == {"text": "中文输入 test", "applied": True}, actual
    ImageGrab.grab().save(output / "success.png")
    return {
        "assertions": [
            "Chinese text readback",
            "modal disables parent",
            "application result",
        ],
        "application_pid": process.pid,
        "screen_size": list(ImageGrab.grab().size),
        "flow": options["flow"],
        "backend": options["backend"],
        "window_dpi": ctypes.windll.user32.GetDpiForWindow(window.handle),
    }
