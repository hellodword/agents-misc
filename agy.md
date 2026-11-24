## 第一部分：安全隔离使用 `tokscale`（按时间区间、按对话统计 Token）

### 1. 安全隔离策略

* **最小目录只读暴露**：不挂载完整的 `~/.gemini`（隔离其中的 `mcp_config.json` 密钥及无头环境落盘的 OAuth 凭据），仅将 `~/.gemini/antigravity-cli/conversations` 子目录以 `:ro` 只读挂载进容器。
* **构建与运行物理隔离**：构建阶段锁定版本、通过 `--ignore-scripts` 禁用 npm 安装脚本并预热离线价格表；运行阶段强制启用 `--runtime=runsc`（gVisor 内核沙箱）、`--network=none`（物理断网）、`--read-only` 与 `--cap-drop=ALL`。
* **内存副本解析**：启动入口将只读挂载的 SQLite `.db` 文件复制至容器内存 `tmpfs` 后再读取，规避 `runsc` 只读挂载层无法创建 SQLite WAL `-shm` 共享内存锁的问题。

### 2. 构建沙箱镜像

新建空目录，保存以下 `Dockerfile` 并执行 `docker build -t tokscale-sandbox .`：

```dockerfile
# syntax=docker/dockerfile:1
FROM node:22-alpine

RUN npm install -g tokscale@4.23.0 --ignore-scripts \
    && npm cache clean --force \
    && mkdir -p /opt/tokscale-config \
    && TOKSCALE_CONFIG_DIR=/opt/tokscale-config tokscale pricing refresh --no-spinner \
    && chmod -R a+rX /opt/tokscale-config

COPY --chmod=755 <<'EOF' /entrypoint.sh
#!/bin/sh
set -eu
mkdir -p /tmp/cfg /tmp/.gemini/antigravity-cli/conversations
cp -r /opt/tokscale-config/. /tmp/cfg/
cp -r /mnt/conversations/. /tmp/.gemini/antigravity-cli/conversations/
export HOME=/tmp
export GEMINI_CLI_HOME=/tmp/.gemini
export TOKSCALE_CONFIG_DIR=/tmp/cfg
exec tokscale --offline --client antigravity-cli "$@"
EOF

ENTRYPOINT ["/entrypoint.sh"]

```

### 3. 宿主机运行函数与查询命令

确保宿主机 Docker 已配置 `runsc` 运行时，将以下函数写入宿主机 `~/.bashrc` 或 `~/.zshrc`：

```bash
tokscale-agy() {
  local tty_flag="-i"
  [ -t 0 ] && [ -t 1 ] && tty_flag="-it"

  docker run --rm $tty_flag \
    --runtime=runsc \
    --network=none \
    --read-only \
    --cap-drop=ALL \
    --security-opt=no-new-privileges \
    --pids-limit=128 \
    --memory=2g \
    --user "$(id -u):$(id -g)" \
    -e TERM="${TERM:-xterm-256color}" \
    --tmpfs /tmp:rw,noexec,nosuid,nodev,size=1g \
    -v "$HOME/.gemini/antigravity-cli/conversations:/mnt/conversations:ro" \
    tokscale-sandbox "$@"
}

```

日常查询方式：

```bash
# 1. 启动交互式 TUI（Tab 切换 Daily / Hourly / Monthly / Sessions，按 b 归并并行子 Agent）
tokscale-agy

# 2. 按时间区间统计并支持导出 JSON
tokscale-agy --since 2026-11-01 --until 2026-11-24
tokscale-agy hourly --since 2026-11-24 --json

# 3. 按单次对话（Session）与模型分组统计
tokscale-agy models --group-by session,model --since 2026-11-20

```

---

## 第二部分：原版 `agy` 实时展示与多维统计 TTFT / TPS 完整方案

### 1. 架构原理

本方案专为无特权 Docker 容器（默认 `runc` 及 `runsc` 沙箱均兼容）内的原版 `agy` 设计，纯 Python 3 标准库实现，不注入进程内存、不配置中间人代理证书：

1. **按需内核套接字字节采样（`NETLINK_SOCK_DIAG`）**：
守护包装器 `agy-mon` 在空闲期完全休眠（`0%` CPU）；仅在 `Hooks` 标记的模型推理窗口内以 `5ms`（`200Hz`）频率通过 Linux 标准无特权 `NETLINK_SOCK_DIAG` 接口读取 `agy` 进程树所属 `:443` 套接字的内核 `tcp_info` 收发字节计数（`runsc` 环境下自动回退读取容器独立网卡 `/proc/net/dev`）。
2. **协议噪声剥离与思考模型校准**：
* 自动过滤 `<90B` 的 HTTP/2 控制帧，并剥离上游网关提前刷出的空 `200 OK` 响应头，精准锁定首枚真实 SSE 数据块到达时刻 $t_{\text{first}}$。
* 结合分片数、流式时长占比及套接字字节密度（`rx_bytes / output_tokens`），自动区分**真实流式解码（`TPS_gen`）**与推理模型**静默思考后的短包批次下发（Batch Flush）**，消除隐藏思考 Token 造成的速率虚高。


3. **固定五槽位常驻底栏与跨端 SQLite 共享**：
`statusLine` 始终同屏锚定 **`运行状态 │ TTFT │ TPS (gen / eff) │ 输出增量 │ 上下文占比`** 五个固定槽位，配合异步后台队列将逐次调用明细落盘至挂载目录 `~/.gemini/antigravity-cli/meter/metrics.db`（采用 `TRUNCATE` 日志模式兼容各类容器文件系统），宿主机与容器内均可随时执行时间加权聚合查询。

---

### 2. 部署步骤（宿主机侧一次性配置，所有挂载 `~/.gemini` 的容器通用）

前提：运行 `agy` 的容器镜像内安装有 `python3`。在宿主机创建目录：

```bash
mkdir -p ~/.gemini/antigravity-cli/meter ~/.gemini/config

```

#### ① 写入守护启动器 `~/.gemini/antigravity-cli/meter/agy-mon`

```python
#!/usr/bin/env python3
import json, os, queue, re, socket, sqlite3, struct, subprocess, sys, threading, time

METER_DIR = os.path.expanduser("~/.gemini/antigravity-cli/meter")
DB_PATH = os.path.join(METER_DIR, "metrics.db")
ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")

AF_NETLINK = getattr(socket, "AF_NETLINK", 16)
NETLINK_SOCK_DIAG = getattr(socket, "NETLINK_SOCK_DIAG", 4)
SOCK_DIAG_BY_FAMILY = 20
NLMSG_ERROR = 2
NLMSG_DONE = 3
NLM_F_REQUEST = 0x01
NLM_F_DUMP = 0x300
INET_DIAG_INFO = 2
TCPF_ESTABLISHED = 1 << 1


def find_real_agy():
    override = os.environ.get("AGY_REAL_BIN")
    if override and os.path.isfile(override):
        return override
    self_real = os.path.realpath(__file__)
    for p in os.environ.get("PATH", "").split(os.pathsep):
        cand = os.path.join(p, "agy")
        if os.path.isfile(cand) and os.access(cand, os.X_OK):
            if os.path.realpath(cand) != self_real:
                return cand
    fallback = os.path.expanduser("~/.local/bin/agy")
    if os.path.isfile(fallback):
        return fallback
    sys.stderr.write("agy-mon: real 'agy' binary not found\n")
    sys.exit(127)


def percentile(vals, q):
    if not vals:
        return None
    s = sorted(vals)
    return s[min(len(s) - 1, max(0, int(round((len(s) - 1) * q))))]


def fmt_dur(ms):
    if ms is None:
        return "--"
    return f"{ms:.0f}ms" if ms < 1000.0 else f"{ms / 1000.0:.2f}s"


def clip_ansi(text, max_cols):
    if max_cols <= 20:
        return text
    vis = len(ANSI_RE.sub("", text))
    if vis <= max_cols:
        return text
    out, count, i = [], 0, 0
    limit = max_cols - 1
    while i < len(text) and count < limit:
        m = ANSI_RE.match(text, i)
        if m:
            out.append(m.group(0))
            i = m.end()
        else:
            out.append(text[i])
            count += 1
            i += 1
    out.append("…\033[0m")
    return "".join(out)


def get_tree_socket_inodes(root_pid):
    inodes, pids, seen = set(), [root_pid], set()
    while pids:
        pid = pids.pop()
        if pid in seen:
            continue
        seen.add(pid)
        try:
            for fd in os.listdir(f"/proc/{pid}/fd"):
                try:
                    lnk = os.readlink(f"/proc/{pid}/fd/{fd}")
                    if lnk.startswith("socket:[") and lnk.endswith("]"):
                        inodes.add(int(lnk[8:-1]))
                except OSError:
                    pass
            for tid in os.listdir(f"/proc/{pid}/task"):
                try:
                    with open(f"/proc/{pid}/task/{tid}/children", "r") as cf:
                        for tok in cf.read().split():
                            pids.append(int(tok))
                except OSError:
                    pass
        except OSError:
            pass
    return inodes


class KernelSocketReader:
    """Zero-privilege TCP byte counter via NETLINK_SOCK_DIAG with /proc/net/dev fallback."""

    def __init__(self):
        self.nl = None
        self.seq = 1
        self.my_uid = os.getuid()
        try:
            self.nl = socket.socket(AF_NETLINK, socket.SOCK_DGRAM, NETLINK_SOCK_DIAG)
            self.nl.bind((0, 0))
            self.nl.settimeout(0.05)
        except OSError:
            self.nl = None

    def read_https_sockets(self, target_inodes):
        if self.nl is not None:
            res, ok = {}, False
            for fam in (socket.AF_INET, socket.AF_INET6):
                try:
                    self.seq += 1
                    req = struct.pack(
                        "<BBBxI48s", fam, socket.IPPROTO_TCP,
                        1 << (INET_DIAG_INFO - 1), TCPF_ESTABLISHED, b"\x00" * 48
                    )
                    hdr = struct.pack("<IHHII", 16 + len(req), SOCK_DIAG_BY_FAMILY, NLM_F_REQUEST | NLM_F_DUMP, self.seq, 0)
                    self.nl.sendto(hdr + req, (0, 0))
                    done = False
                    while not done:
                        buf = self.nl.recv(65536)
                        if not buf:
                            break
                        off = 0
                        while off + 16 <= len(buf):
                            nl_len, nl_type, _, _, _ = struct.unpack_from("<IHHII", buf, off)
                            if nl_len < 16 or off + nl_len > len(buf):
                                done = True
                                break
                            if nl_type in (NLMSG_DONE, NLMSG_ERROR):
                                done = True
                                break
                            if nl_type == SOCK_DIAG_BY_FAMILY and nl_len >= 88:
                                m_off = off + 16
                                _, dport = struct.unpack_from("!HH", buf, m_off + 4)
                                uid, inode = struct.unpack_from("<II", buf, m_off + 64)
                                if dport == 443 and (inode in target_inodes or (not target_inodes and uid == self.my_uid)):
                                    r_off, m_end = m_off + 72, off + nl_len
                                    while r_off + 4 <= m_end:
                                        r_len, r_type = struct.unpack_from("<HH", buf, r_off)
                                        if r_len < 4 or r_off + r_len > m_end:
                                            break
                                        if (r_type & 0x7FFF) == INET_DIAG_INFO and (r_len - 4) >= 136:
                                            t_off = r_off + 4
                                            b_acked, b_rx = struct.unpack_from("<QQ", buf, t_off + 120)
                                            b_tx = struct.unpack_from("<Q", buf, t_off + 200)[0] if (r_len - 4) >= 208 else b_acked
                                            key = inode if inode else (fam, dport, r_off)
                                            res[key] = (b_tx, b_rx)
                                            ok = True
                                        r_off += (r_len + 3) & ~3
                            off += (nl_len + 3) & ~3
                except OSError:
                    pass
            if ok or res:
                return res, "netlink"

        rx_sum = tx_sum = 0
        try:
            with open("/proc/net/dev", "r") as f:
                for line in f.readlines()[2:]:
                    if ":" not in line:
                        continue
                    iface, rest = line.split(":", 1)
                    if iface.strip() == "lo":
                        continue
                    cols = rest.split()
                    if len(cols) >= 9:
                        rx_sum += int(cols[0])
                        tx_sum += int(cols[8])
        except OSError:
            pass
        return {0: (tx_sum, rx_sum)}, "netdev"


def analyze_trace(sock_trace, t_fallback, now, finalized=False):
    bursts = sock_trace["bursts"]
    if not bursts:
        return None
    t_origin = sock_trace["t_req_sent"] or t_fallback
    tot_rx = max(0, sock_trace["prev_rx"] - sock_trace["base_rx"])
    t0, b0 = bursts[0]

    if len(bursts) == 1:
        if not finalized and b0 <= 380 and (t0 - t_origin) < 0.35 and (now - t0) > 0.25:
            return None
        token_bursts = bursts
    else:
        t1, _ = bursts[1]
        gap0_ms = (t1 - t0) * 1000.0
        rest_gaps = [(bursts[i][0] - bursts[i - 1][0]) * 1000.0 for i in range(2, len(bursts))]
        med_itl = percentile(rest_gaps, 0.50) if rest_gaps else 60.0
        if b0 <= 380 and (t0 - t_origin) < 0.35 and gap0_ms > max(250.0, 4.0 * med_itl):
            token_bursts = bursts[1:]
        else:
            token_bursts = bursts

    t_first = token_bursts[0][0]
    t_last = token_bursts[-1][0]
    ttft_ms = max(1.0, (t_first - t_origin) * 1000.0)
    decode_ms = max(0.0, (t_last - t_first) * 1000.0)
    itls_ms = [(token_bursts[i][0] - token_bursts[i - 1][0]) * 1000.0 for i in range(1, len(token_bursts))]
    return {
        "ttft_ms": ttft_ms,
        "decode_ms": decode_ms,
        "itls_ms": itls_ms,
        "chunks": len(token_bursts),
        "rx_bytes": tot_rx,
    }


class MeterDaemon:
    def __init__(self, hud_cache_path):
        self.lock = threading.Lock()
        self.wake_ev = threading.Event()
        self.db_queue = queue.Queue()
        self.agy_pid = None
        self.hud_cache_path = hud_cache_path

        self.active_invs = {}
        self.pending_invs = {}
        self.seen_tot_out = {}
        self.last_hud = {}
        self.global_hud = None
        self.last_valid_gen = {}
        self.global_valid_gen = None

        self._init_db()
        threading.Thread(target=self._db_worker, daemon=True).start()
        threading.Thread(target=self._sampler_loop, daemon=True).start()

    def _connect_db(self):
        os.makedirs(METER_DIR, exist_ok=True)
        conn = sqlite3.connect(DB_PATH, timeout=5.0)
        conn.execute("PRAGMA journal_mode=TRUNCATE;")
        conn.execute("PRAGMA synchronous=NORMAL;")
        conn.execute("PRAGMA busy_timeout=5000;")
        return conn

    def _init_db(self):
        with self._connect_db() as conn:
            conn.execute("""
                CREATE TABLE IF NOT EXISTS invocations (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    ts REAL NOT NULL,
                    conversation_id TEXT NOT NULL,
                    invocation_num INTEGER,
                    model TEXT,
                    input_tokens INTEGER DEFAULT 0,
                    cache_read_tokens INTEGER DEFAULT 0,
                    output_tokens INTEGER NOT NULL,
                    ttft_ms REAL,
                    decode_ms REAL,
                    llm_ms REAL NOT NULL,
                    tps_gen REAL,
                    tps_eff REAL NOT NULL,
                    itl_p50_ms REAL,
                    itl_p95_ms REAL,
                    sse_chunks INTEGER DEFAULT 0,
                    rx_bytes INTEGER DEFAULT 0,
                    engine TEXT DEFAULT 'netlink'
                )
            """)
            cols = {r[1] for r in conn.execute("PRAGMA table_info(invocations)")}
            if "rx_bytes" not in cols:
                conn.execute("ALTER TABLE invocations ADD COLUMN rx_bytes INTEGER DEFAULT 0")
            if "engine" not in cols:
                conn.execute("ALTER TABLE invocations ADD COLUMN engine TEXT DEFAULT 'netlink'")
            conn.execute("CREATE INDEX IF NOT EXISTS idx_inv_ts ON invocations(ts)")
            conn.execute("CREATE INDEX IF NOT EXISTS idx_inv_cid ON invocations(conversation_id)")

            gen_row = conn.execute(
                "SELECT tps_gen FROM invocations WHERE tps_gen IS NOT NULL ORDER BY id DESC LIMIT 1"
            ).fetchone()
            if gen_row:
                self.global_valid_gen = gen_row[0]

            row = conn.execute("""
                SELECT conversation_id, ttft_ms, tps_gen, tps_eff, output_tokens
                FROM invocations ORDER BY id DESC LIMIT 1
            """).fetchone()
            if row:
                hud = {
                    "ttft_ms": row[1],
                    "tps_gen": row[2] if row[2] is not None else self.global_valid_gen,
                    "gen_carried": row[2] is None and self.global_valid_gen is not None,
                    "tps_eff": row[3],
                    "delta_out": row[4],
                }
                self.global_hud = hud
                self.last_hud[row[0]] = hud

    def _db_worker(self):
        while True:
            params = self.db_queue.get()
            if params is None:
                break
            try:
                with self._connect_db() as conn:
                    conn.execute("""
                        INSERT INTO invocations (
                            ts, conversation_id, invocation_num, model,
                            input_tokens, cache_read_tokens, output_tokens,
                            ttft_ms, decode_ms, llm_ms, tps_gen, tps_eff,
                            itl_p50_ms, itl_p95_ms, sse_chunks, rx_bytes, engine
                        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """, params)
            except Exception:
                pass

    def _sampler_loop(self):
        reader = KernelSocketReader()
        last_inode_scan = 0.0
        cached_inodes = set()

        while True:
            self.wake_ev.wait()
            with self.lock:
                if not self.active_invs:
                    self.wake_ev.clear()
                    continue
                pid = self.agy_pid

            now = time.monotonic()
            if pid and (now - last_inode_scan >= 0.05 or not cached_inodes):
                cached_inodes = get_tree_socket_inodes(pid)
                last_inode_scan = now

            snap, engine = reader.read_https_sockets(cached_inodes)
            now = time.monotonic()

            with self.lock:
                for act in self.active_invs.values():
                    act["engine"] = engine
                    if not act["initialized"]:
                        act["initialized"] = True
                        for sk, (tx, rx) in snap.items():
                            act["socks"][sk] = {
                                "base_tx": tx, "prev_tx": tx,
                                "base_rx": rx, "prev_rx": rx,
                                "t_req_sent": None, "bursts": [],
                            }
                        continue

                    for sk, (tx, rx) in snap.items():
                        tr = act["socks"].get(sk)
                        if tr is None:
                            tr = {
                                "base_tx": 0, "prev_tx": tx,
                                "base_rx": 0, "prev_rx": rx,
                                "t_req_sent": now if tx >= 120 else None,
                                "bursts": [(now, rx)] if (tx >= 120 and rx >= 90) else [],
                            }
                            act["socks"][sk] = tr
                        else:
                            d_tx = max(0, tx - tr["prev_tx"])
                            d_rx = max(0, rx - tr["prev_rx"])
                            tot_tx = max(0, tx - tr["base_tx"])
                            tot_rx = max(0, rx - tr["base_rx"])
                            if d_tx >= 120 and tot_rx < 90:
                                tr["t_req_sent"] = now
                            if tot_tx >= 120 and d_rx >= 90:
                                tr["bursts"].append((now, d_rx))
                            tr["prev_tx"] = tx
                            tr["prev_rx"] = rx

            time.sleep(0.005)

    def _best_trace(self, act):
        cands = [
            tr for tr in act["socks"].values()
            if (tr["prev_tx"] - tr["base_tx"]) >= 120 and (tr["prev_rx"] - tr["base_rx"]) >= 90
        ]
        if not cands:
            return None
        cands.sort(key=lambda tr: (tr["prev_rx"] - tr["base_rx"], len(tr["bursts"])))
        return cands[-1]

    def on_rpc(self, req):
        ev = req.get("event")
        now = time.monotonic()

        if ev == "pre":
            cid = req.get("cid") or "default"
            with self.lock:
                self.active_invs[cid] = {
                    "inv": req.get("inv", 0),
                    "t_pre": now,
                    "initialized": False,
                    "engine": "netlink",
                    "socks": {},
                }
            self.wake_ev.set()
            return {"ok": True}

        if ev == "post":
            cid = req.get("cid") or "default"
            time.sleep(0.008)
            now = time.monotonic()
            with self.lock:
                act = self.active_invs.pop(cid, None)
                if not act:
                    return {"ok": True}
                llm_ms = max(1.0, (now - act["t_pre"]) * 1000.0)
                best = self._best_trace(act)
                parsed = analyze_trace(best, act["t_pre"], now, finalized=True) if best else None

                self.pending_invs.setdefault(cid, []).append({
                    "inv": act["inv"],
                    "ttft_ms": parsed["ttft_ms"] if parsed else None,
                    "decode_ms": parsed["decode_ms"] if parsed else None,
                    "llm_ms": llm_ms,
                    "itl_p50": percentile(parsed["itls_ms"], 0.50) if parsed else None,
                    "itl_p95": percentile(parsed["itls_ms"], 0.95) if parsed else None,
                    "chunks": parsed["chunks"] if parsed else 0,
                    "rx_bytes": parsed["rx_bytes"] if parsed else 0,
                    "engine": act["engine"],
                })
            return {"ok": True}

        if ev == "statusline":
            with self.lock:
                line = self._render_statusline(req.get("payload") or {}, now)
            try:
                tmp = f"{self.hud_cache_path}.tmp"
                with open(tmp, "w") as f:
                    f.write(line)
                os.replace(tmp, self.hud_cache_path)
            except OSError:
                pass
            return {"line": line}

        return {"ok": False}

    def _render_statusline(self, data, now):
        cid = data.get("conversation_id") or data.get("session_id") or "default"
        raw_state = (data.get("agent_state") or data.get("state") or "idle").lower()
        term_w = int(data.get("terminal_width") or 120)

        m_raw = data.get("model")
        model = (m_raw.get("display_name") or m_raw.get("id") if isinstance(m_raw, dict) else m_raw) or "unknown"

        cw = data.get("context_window") or {}
        tot_out = cw.get("total_output_tokens") or 0
        used_pct = cw.get("used_percentage") or 0.0
        cur = cw.get("current_usage") or {}
        in_tok = cur.get("input_tokens") or 0
        cache_tok = cur.get("cache_read_input_tokens") or cur.get("cached_tokens") or 0
        cur_out = cur.get("output_tokens") or 0

        pending = self.pending_invs.get(cid) or []
        if cid not in self.seen_tot_out and not pending:
            self.seen_tot_out[cid] = tot_out

        prev_out = self.seen_tot_out.get(cid, 0)
        if pending and tot_out > prev_out:
            delta_out = tot_out - prev_out
            if prev_out == 0 and len(pending) == 1 and 0 < cur_out < delta_out:
                delta_out = cur_out
            self.seen_tot_out[cid] = tot_out
            self.pending_invs[cid] = []

            sum_llm_ms = sum(x["llm_ms"] for x in pending)
            tps_eff = delta_out / (sum_llm_ms / 1000.0) if sum_llm_ms > 0 else 0.0

            all_have_dec = all(x["decode_ms"] is not None for x in pending)
            sum_dec_ms = sum(x["decode_ms"] for x in pending if x["decode_ms"] is not None)
            sum_chunks = sum(x["chunks"] for x in pending)
            sum_rx = sum(x["rx_bytes"] for x in pending)
            bytes_per_tok = sum_rx / max(1, delta_out)

            # Valid pure streaming requires >=5 chunks, >=800ms window, and guards against
            # hidden pre-TTFT server-side thinking tokens inflating short tail flushes.
            is_valid_stream = (
                all_have_dec
                and sum_chunks >= 5
                and sum_dec_ms >= 800.0
                and (sum_dec_ms >= 0.50 * sum_llm_ms or bytes_per_tok >= 4.0)
            )
            tps_gen_measured = (delta_out / (sum_dec_ms / 1000.0)) if is_valid_stream else None

            if tps_gen_measured is not None:
                self.last_valid_gen[cid] = tps_gen_measured
                self.global_valid_gen = tps_gen_measured

            carried_gen = self.last_valid_gen.get(cid) or self.global_valid_gen
            last = pending[-1]
            hud = {
                "ttft_ms": last["ttft_ms"],
                "tps_gen": tps_gen_measured if tps_gen_measured is not None else carried_gen,
                "gen_carried": tps_gen_measured is None and carried_gen is not None,
                "tps_eff": tps_eff,
                "delta_out": delta_out,
            }
            self.last_hud[cid] = hud
            self.global_hud = hud

            self.db_queue.put((
                time.time(), cid, last["inv"], str(model),
                in_tok, cache_tok, delta_out,
                last["ttft_ms"], sum_dec_ms if all_have_dec else None, sum_llm_ms,
                tps_gen_measured, tps_eff, last["itl_p50"], last["itl_p95"],
                sum_chunks, sum_rx, last["engine"]
            ))

        hud = self.last_hud.get(cid) or self.global_hud or {}
        settled_ttft = fmt_dur(hud.get("ttft_ms"))
        tps_gen_v = hud.get("tps_gen")
        gen_carried = hud.get("gen_carried", False)
        tps_eff_v = hud.get("tps_eff")
        delta_out_v = hud.get("delta_out")

        act = self.active_invs.get(cid)
        if act:
            elapsed = max(0.0, now - act["t_pre"])
            best = self._best_trace(act)
            live = analyze_trace(best, act["t_pre"], now, finalized=False) if best else None
            if live:
                state_badge = f"\033[32m▶ STREAM {elapsed:.1f}s\033[0m"
                ttft_slot = f"\033[33m{fmt_dur(live['ttft_ms'])}\033[0m"
            else:
                state_badge = f"\033[35m◐ PREFILL {elapsed:.1f}s\033[0m"
                prev_hint = f" (last {settled_ttft})" if settled_ttft != "--" else ""
                ttft_slot = f"\033[33m…{elapsed:.1f}s\033[0m{prev_hint}"
        else:
            state_map = {
                "idle": "\033[36m● READY\033[0m",
                "tool_use": "\033[33m⚙ TOOL\033[0m",
                "working": "\033[32m◐ WORK\033[0m",
                "thinking": "\033[35m◐ THINK\033[0m",
                "reviewing": "\033[34m◈ REVIEW\033[0m",
                "error": "\033[31m✖ ERROR\033[0m",
            }
            state_badge = state_map.get(raw_state, f"\033[36m● {raw_state.upper()}\033[0m")
            ttft_slot = f"\033[33m{settled_ttft}\033[0m"

        if tps_gen_v is not None and tps_eff_v is not None:
            gen_disp = f"~{tps_gen_v:.1f}" if gen_carried else f"\033[32m{tps_gen_v:.1f}\033[0m"
            tps_slot = f"{gen_disp} gen / {tps_eff_v:.1f} eff"
        elif tps_eff_v is not None:
            tps_slot = f"batch / {tps_eff_v:.1f} eff"
        else:
            tps_slot = "--"

        out_prefix = f"+{delta_out_v} " if delta_out_v else ""
        out_slot = f"{out_prefix}({tot_out / 1000.0:.1f}k)"

        line = (
            f"{state_badge} │ TTFT {ttft_slot} │ TPS {tps_slot} │ "
            f"Out {out_slot} │ Ctx {used_pct:.0f}%"
        )
        return clip_ansi(line, max(40, term_w - 2))


def handle_client(conn, daemon):
    try:
        raw = b""
        while not raw.endswith(b"\n"):
            chunk = conn.recv(65536)
            if not chunk:
                break
            raw += chunk
        if raw:
            resp = daemon.on_rpc(json.loads(raw.decode("utf-8", "replace")))
            conn.sendall((json.dumps(resp) + "\n").encode("utf-8"))
    except Exception:
        pass
    finally:
        conn.close()


def serve_unix_socket(sock_path, daemon):
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(sock_path)
    srv.listen(32)
    while True:
        try:
            conn, _ = srv.accept()
        except Exception:
            break
        threading.Thread(target=handle_client, args=(conn, daemon), daemon=True).start()


def main():
    real_agy = find_real_agy()
    pid = os.getpid()
    sock_path = f"/tmp/agy_meter_{pid}.sock"
    hud_cache_path = f"/tmp/agy_hud_{pid}.txt"

    daemon = MeterDaemon(hud_cache_path)
    threading.Thread(target=serve_unix_socket, args=(sock_path, daemon), daemon=True).start()

    env = os.environ.copy()
    env["AGY_METER_SOCK"] = sock_path
    env["AGY_HUD_CACHE"] = hud_cache_path

    rc = 1
    try:
        proc = subprocess.Popen([real_agy] + sys.argv[1:], env=env)
        with daemon.lock:
            daemon.agy_pid = proc.pid
        rc = proc.wait()
        daemon.db_queue.put(None)
    finally:
        for p in (sock_path, hud_cache_path):
            try:
                os.unlink(p)
            except OSError:
                pass
    sys.exit(rc)


if __name__ == "__main__":
    main()

```

#### ② 写入钩子与状态栏客户端 `~/.gemini/antigravity-cli/meter/meter_client.py`

```python
#!/usr/bin/env python3
import json, os, socket, sys

mode = sys.argv[1] if len(sys.argv) > 1 else "statusline"
try:
    payload = json.load(sys.stdin)
except Exception:
    payload = {}

sock_path = os.environ.get("AGY_METER_SOCK")
hud_cache = os.environ.get("AGY_HUD_CACHE")


def call_daemon(msg, timeout=0.6):
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
    except Exception:
        return None
    finally:
        s.close()


if mode in ("pre", "post"):
    cid = payload.get("conversationId") or payload.get("conversation_id") or "default"
    call_daemon({"event": mode, "cid": cid, "inv": payload.get("invocationNum", 0)}, timeout=0.8)
    print("{}")
elif mode == "statusline":
    resp = call_daemon({"event": "statusline", "payload": payload}, timeout=0.4)
    if resp and resp.get("line"):
        print(resp["line"])
    elif hud_cache and os.path.exists(hud_cache):
        try:
            with open(hud_cache, "r") as f:
                print(f.read().strip())
        except OSError:
            print("● READY │ TTFT -- │ TPS -- │ Out (0.0k) │ Ctx 0%")
    else:
        st = (payload.get("agent_state") or "idle").upper()
        cw = payload.get("context_window") or {}
        print(f"● {st} │ TTFT -- │ TPS -- │ Out ({(cw.get('total_output_tokens') or 0)/1000:.1f}k) │ Ctx {cw.get('used_percentage', 0):.0f}%")

```

#### ③ 写入时间加权多维统计查询工具 `~/.gemini/antigravity-cli/meter/agy-perf`

```python
#!/usr/bin/env python3
import argparse, datetime, json, os, re, sqlite3, time

DB_PATH = os.path.expanduser("~/.gemini/antigravity-cli/meter/metrics.db")


def parse_time(val, is_until=False):
    if not val:
        return None
    m = re.fullmatch(r"(\d+)([smhd])", val.strip())
    if m:
        unit = {"s": 1, "m": 60, "h": 3600, "d": 86400}[m.group(2)]
        return time.time() - int(m.group(1)) * unit
    for fmt in ("%Y-%m-%d %H:%M:%S", "%Y-%m-%d %H:%M", "%Y-%m-%d"):
        try:
            dt = datetime.datetime.strptime(val, fmt)
            if fmt == "%Y-%m-%d" and is_until:
                dt = dt.replace(hour=23, minute=59, second=59)
            return dt.timestamp()
        except ValueError:
            continue
    raise SystemExit(f"Unsupported time format: {val}")


def pct(vals, q):
    v = sorted(x for x in vals if x is not None)
    return v[min(len(v) - 1, int(round((len(v) - 1) * q)))] if v else None


def fmt_num(v, digits=1):
    return f"{v:.{digits}f}" if v is not None else "--"


def fmt_ms(v):
    if v is None:
        return "--"
    return f"{v:.0f}ms" if v < 1000.0 else f"{v / 1000.0:.2f}s"


def main():
    ap = argparse.ArgumentParser(description="Query Antigravity CLI TTFT & TPS metrics")
    ap.add_argument("view", nargs="?", default="models", choices=["models", "sessions", "tail"])
    ap.add_argument("--last", help="Relative window, e.g. 1h, 24h, 7d")
    ap.add_argument("--since", help="YYYY-MM-DD [HH:MM[:SS]]")
    ap.add_argument("--until", help="YYYY-MM-DD [HH:MM[:SS]]")
    ap.add_argument("--cid", help="Filter by conversation_id prefix")
    ap.add_argument("-n", type=int, default=20, help="Rows for tail view")
    ap.add_argument("--json", action="store_true", help="Export JSON")
    args = ap.parse_args()

    if not os.path.exists(DB_PATH):
        raise SystemExit(f"No metrics database yet at {DB_PATH}")

    t_min = parse_time(args.last) if args.last else parse_time(args.since)
    t_max = parse_time(args.until, is_until=True)

    clauses, params = [], []
    if t_min is not None:
        clauses.append("ts >= ?")
        params.append(t_min)
    if t_max is not None:
        clauses.append("ts <= ?")
        params.append(t_max)
    if args.cid:
        clauses.append("conversation_id LIKE ?")
        params.append(f"{args.cid}%")

    where_sql = ("WHERE " + " AND ".join(clauses)) if clauses else ""
    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    rows = [dict(r) for r in conn.execute(f"SELECT * FROM invocations {where_sql} ORDER BY ts ASC", params)]

    if not rows:
        print("No matching records.")
        return

    if args.view == "tail":
        sub = rows[-args.n:]
        if args.json:
            print(json.dumps(sub, indent=2))
            return
        print(f"{'TIME':<19} {'CID':<9} {'MODEL':<23} {'OUT':>5} {'TTFT':>8} {'TPS_gen':>8} {'TPS_eff':>8} {'ITL_p50':>8} {'ITL_p95':>8} {'CHKS':>5}")
        print("-" * 110)
        for r in sub:
            ts_s = datetime.datetime.fromtimestamp(r["ts"]).strftime("%Y-%m-%d %H:%M:%S")
            gen_s = fmt_num(r["tps_gen"]) if r["tps_gen"] is not None else "batch"
            print(
                f"{ts_s:<19} {r['conversation_id'][:8]:<9} {r['model'][:23]:<23} "
                f"{r['output_tokens']:>5} {fmt_ms(r['ttft_ms']):>8} "
                f"{gen_s:>8} {fmt_num(r['tps_eff']):>8} "
                f"{fmt_ms(r['itl_p50_ms']):>8} {fmt_ms(r['itl_p95_ms']):>8} {(r.get('sse_chunks') or 0):>5}"
            )
        return

    key_fn = (lambda r: r["model"]) if args.view == "models" else (lambda r: r["conversation_id"])
    groups = {}
    for r in rows:
        groups.setdefault(key_fn(r), []).append(r)

    summary = []
    for k, grp in groups.items():
        tot_out = sum(x["output_tokens"] for x in grp)
        tot_llm_s = sum((x["llm_ms"] or 0.0) for x in grp) / 1000.0
        tps_eff_w = (tot_out / tot_llm_s) if tot_llm_s > 0 else None

        s_grp = [x for x in grp if x.get("tps_gen") is not None and (x.get("decode_ms") or 0.0) > 0]
        s_out = sum(x["output_tokens"] for x in s_grp)
        s_dec_s = sum(x["decode_ms"] for x in s_grp) / 1000.0
        tps_gen_w = (s_out / s_dec_s) if s_dec_s > 0 else None

        summary.append({
            "key": k,
            "calls": len(grp),
            "stream_calls": len(s_grp),
            "out_tokens": tot_out,
            "ttft_p50_ms": pct([x["ttft_ms"] for x in grp], 0.50),
            "ttft_p95_ms": pct([x["ttft_ms"] for x in grp], 0.95),
            "tps_gen_weighted": tps_gen_w,
            "tps_eff_weighted": tps_eff_w,
            "itl_p50_ms": pct([x["itl_p50_ms"] for x in grp], 0.50),
            "itl_p95_ms": pct([x["itl_p95_ms"] for x in grp], 0.95),
            "last_ts": max(x["ts"] for x in grp),
        })
    summary.sort(key=lambda x: x["last_ts"], reverse=True)

    if args.json:
        print(json.dumps(summary, indent=2))
        return

    hdr = "MODEL" if args.view == "models" else "CONVERSATION_ID"
    w = 24 if args.view == "models" else 36
    print(f"{hdr:<{w}} {'CALLS':>5} {'OUT_TOK':>8} {'TTFT_p50':>8} {'TTFT_p95':>8} {'TPS_gen':>8} {'TPS_eff':>8} {'ITL_p50':>8} {'ITL_p95':>8}")
    print("-" * (w + 69))
    for s in summary:
        print(
            f"{s['key'][:w]:<{w}} {s['calls']:>5} {s['out_tokens']:>8} "
            f"{fmt_ms(s['ttft_p50_ms']):>8} {fmt_ms(s['ttft_p95_ms']):>8} "
            f"{fmt_num(s['tps_gen_weighted']):>8} {fmt_num(s['tps_eff_weighted']):>8} "
            f"{fmt_ms(s['itl_p50_ms']):>8} {fmt_ms(s['itl_p95_ms']):>8}"
        )


if __name__ == "__main__":
    main()

```

设置脚本可执行权限：

```bash
chmod +x ~/.gemini/antigravity-cli/meter/agy-mon \
         ~/.gemini/antigravity-cli/meter/meter_client.py \
         ~/.gemini/antigravity-cli/meter/agy-perf

```

#### ④ 配置 `Hooks` 与 `statusLine`

写入 **`~/.gemini/config/hooks.json`**（如已有其他顶层配置项请合并）：

```json
{
  "agy-ttft-tps-meter": {
    "enabled": true,
    "PreInvocation": [
      {
        "type": "command",
        "command": "python3 \"$HOME/.gemini/antigravity-cli/meter/meter_client.py\" pre",
        "timeout": 5
      }
    ],
    "PostInvocation": [
      {
        "type": "command",
        "command": "python3 \"$HOME/.gemini/antigravity-cli/meter/meter_client.py\" post",
        "timeout": 5
      }
    ]
  }
}

```

在 **`~/.gemini/antigravity-cli/settings.json`** 中合并加入 `statusLine` 配置：

```json
{
  "statusLine": {
    "type": "command",
    "command": "python3 \"$HOME/.gemini/antigravity-cli/meter/meter_client.py\" statusline",
    "stack_with_default": true,
    "enabled": true
  }
}

```

---

### 3. 容器运行、底栏读数说明与历史统计查询

#### ① 启动容器运行 `agy`

普通无特权 `docker run`（或附加 `--runtime=runsc`）直接以 `agy-mon` 作为入口：

```bash
docker run -it --rm \
  -e GEMINI_FORCE_FILE_STORAGE=true \
  -v "$HOME/.gemini:/root/.gemini" \
  -v "$(pwd):/workspace" -w /workspace \
  your-agy-image \
  /root/.gemini/antigravity-cli/meter/agy-mon --dangerously-skip-permissions

```

> 首次进入新项目工作区时，需在 `agy` 内确认信任（Trust）当前目录以激活 `Hooks`。

#### ② 实时底栏五槽位读数说明

* **等待首包**：`◐ PREFILL 2.4s │ TTFT …2.4s (last 4.12s) │ TPS 54.2 gen / 43.8 eff │ Out +620 (5.1k) │ Ctx 8%`
* **流式吐字**：`▶ STREAM 9.6s │ TTFT 4.35s │ TPS 54.2 gen / 43.8 eff │ Out +620 (5.1k) │ Ctx 8%`
* **流式步骤结算**：`● READY │ TTFT 4.35s │ TPS 56.8 gen / 45.1 eff │ Out +940 (6.0k) │ Ctx 9%`
* **短工具调用 / 静默思考单包批次结算**：`⚙ TOOL │ TTFT 18.20s │ TPS ~56.8 gen / 36.4 eff │ Out +710 (6.7k) │ Ctx 10%`（`~56.8 gen` 表示当前步为单包批次下发，沿用会话最近一次有效流式解码速率展示，同时实时更新本步 `TTFT` 与 `eff`）。

#### ③ 多维统计查询（宿主机或容器内直接运行）

```bash
# 1. 按模型汇总时间加权 TPS_gen、TPS_eff、TTFT (P50/P95) 及 ITL (P50/P95)
~/.gemini/antigravity-cli/meter/agy-perf models --last 24h

# 2. 按对话（Session）统计指定日期区间表现
~/.gemini/antigravity-cli/meter/agy-perf sessions --since "2026-11-20" --until "2026-11-24"

# 3. 查看最近 20 次底层模型调用逐笔明细（自动标注流式数值或 batch 批次）
~/.gemini/antigravity-cli/meter/agy-perf tail -n 20

# 4. 导出 JSON 供外部脚本分析
~/.gemini/antigravity-cli/meter/agy-perf models --last 7d --json

```