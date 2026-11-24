#!/usr/bin/env python3
import json
import os
import socket
import sys


def call_daemon(sock_path, msg, timeout=0.6):
    if not sock_path or not os.path.exists(sock_path):
        return None
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(timeout)
    try:
        s.connect(sock_path)
        s.sendall((json.dumps(msg) + "\n").encode("utf-8"))
        buf = b""
        while not buf.endswith(b"\n"):
            chunk = s.recv(65536)
            if not chunk:
                break
            buf += chunk
        return json.loads(buf.decode("utf-8")) if buf else None
    except (OSError, ValueError):
        return None
    finally:
        s.close()


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "statusline"
    if mode in ("-h", "--help"):
        sys.stdout.write("Usage: meter_client.py [pre|post|statusline]\n")
        return
    if mode not in ("pre", "post", "statusline"):
        sys.stderr.write(f"meter_client.py: unsupported mode '{mode}'\n")
        sys.exit(2)

    try:
        payload = json.load(sys.stdin)
    except (OSError, ValueError):
        payload = {}

    sock_path = os.environ.get("AGY_METER_SOCK")
    hud_cache = os.environ.get("AGY_HUD_CACHE")

    if mode in ("pre", "post"):
        cid = (
            payload.get("conversationId") or payload.get("conversation_id") or "default"
        )
        call_daemon(
            sock_path,
            {"event": mode, "cid": cid, "inv": payload.get("invocationNum", 0)},
            timeout=0.8,
        )
        print("{}")
        return

    resp = call_daemon(
        sock_path, {"event": "statusline", "payload": payload}, timeout=0.4
    )
    if resp and resp.get("line"):
        print(resp["line"])
    elif hud_cache and os.path.exists(hud_cache):
        try:
            with open(hud_cache) as f:
                print(f.read().strip())
        except OSError:
            print("● READY │ TTFT -- │ TPS -- │ Out (0.0k) │ Ctx 0%")
    else:
        st = (payload.get("agent_state") or "idle").upper()
        cw = payload.get("context_window") or {}
        tot_k = (cw.get("total_output_tokens") or 0) / 1000.0
        pct = cw.get("used_percentage") or 0
        print(f"● {st} │ TTFT -- │ TPS -- │ Out ({tot_k:.1f}k) │ Ctx {pct:.0f}%")


if __name__ == "__main__":
    main()
