"""Disposable Win32 form with a painted image target and an application result."""

import argparse
import ctypes
import json
import time
from pathlib import Path


def main():
    import win32api
    import win32con as c
    import win32gui as g
    from PIL import Image, ImageWin

    ctypes.windll.shcore.SetProcessDpiAwareness(2)

    parser = argparse.ArgumentParser()
    parser.add_argument("--result", type=Path, required=True)
    parser.add_argument(
        "--block-uia",
        action="store_true",
        help="Acceptance case: block WM_GETOBJECT until the outer runner terminates this process",
    )
    args = parser.parse_args()
    edit = None
    target = ImageWin.Dib(Image.open(Path(__file__).with_name("target.png")))

    def apply():
        args.result.write_text(
            json.dumps(
                {"text": g.GetWindowText(edit), "applied": True}, ensure_ascii=False
            ),
            encoding="utf-8",
        )

    def window(hwnd, msg, wp, lp):
        if msg == c.WM_GETOBJECT and args.block_uia:
            time.sleep(300)
        elif msg == c.WM_DESTROY:
            g.PostQuitMessage(0)
        elif msg == c.WM_COMMAND:
            if win32api.LOWORD(wp) == 102:
                apply()
            elif win32api.LOWORD(wp) == 103:
                g.MessageBox(hwnd, "Modal ready", "Example modal", c.MB_OK)
        elif msg == c.WM_PAINT:
            dc, paint = g.BeginPaint(hwnd)
            target.draw(dc, (280, 100, 376, 148))
            g.EndPaint(hwnd, paint)
        elif msg == c.WM_LBUTTONUP:
            x, y = win32api.LOWORD(lp), win32api.HIWORD(lp)
            if 280 <= x < 376 and 100 <= y < 148:
                apply()
        return g.DefWindowProc(hwnd, msg, wp, lp)

    wc = g.WNDCLASS()
    wc.lpfnWndProc, wc.lpszClassName = window, "AutomationExample"
    wc.hInstance = win32api.GetModuleHandle(None)
    wc.hbrBackground = c.COLOR_WINDOW + 1
    g.RegisterClass(wc)
    hwnd = g.CreateWindow(
        wc.lpszClassName,
        "Automation Example",
        c.WS_OVERLAPPEDWINDOW,
        100,
        100,
        480,
        260,
        0,
        0,
        wc.hInstance,
        None,
    )
    edit = g.CreateWindowEx(
        c.WS_EX_CLIENTEDGE,
        "EDIT",
        "",
        c.WS_CHILD | c.WS_VISIBLE | c.WS_TABSTOP | c.ES_AUTOHSCROLL,
        20,
        20,
        400,
        30,
        hwnd,
        101,
        wc.hInstance,
        None,
    )
    for ident, label, x in ((102, "Apply", 20), (103, "Modal", 140)):
        g.CreateWindow(
            "BUTTON",
            label,
            c.WS_CHILD | c.WS_VISIBLE | c.WS_TABSTOP,
            x,
            100,
            100,
            40,
            hwnd,
            ident,
            wc.hInstance,
            None,
        )
    g.ShowWindow(hwnd, c.SW_SHOW)
    g.UpdateWindow(hwnd)
    g.PumpMessages()


if __name__ == "__main__":
    main()
