# AGY Meter Architecture

## 1. Observability constraints in closed-source `agy`

Computing accurate **TTFT (Time To First Token)** and **TPS (Tokens Per Second)** requires three measurements for each model invocation:

1. Exact request dispatch timestamp $t_{\text{req}}$;
2. First content chunk arrival $t_{\text{first}}$ and final chunk completion $t_{\text{last}}$;
3. Exact output token delta $\Delta N_{\text{out}}$ produced during that invocation.

When running the closed-source `agy` binary inside an unprivileged container over encrypted HTTPS (`:443`), each official extension mechanism exposes half of the picture:

- **Lifecycle `Hooks` (`PreInvocation` / `PostInvocation`)** fire once immediately before model dispatch and once after the full response completes. They provide invocation boundaries without visibility into first-token arrival or token counts.
- **`statusLine` (~300 ms refresh cadence)** supplies official cumulative session token counters (`context_window.total_output_tokens`) and renders returned ANSI text at the bottom of the TUI, without millisecond request or first-token timestamps.
- **Unprivileged TLS `:443` transport** encrypts SSE payloads end-to-end in environments without privileged kernel eBPF probes or MITM certificate injection.

## 2. Unprivileged socket byte telemetry

During an active turn, `agy` communicates with upstream model endpoints over TCP port `443`. Linux exposes per-socket transport counters to unprivileged user space through `NETLINK_SOCK_DIAG` (`SOCK_DIAG_BY_FAMILY` with `INET_DIAG_INFO`—the same kernel interface `ss -ti` queries):

> _"For the `:443` TCP sockets owned by `agy`'s process tree, how many cumulative bytes have been transmitted (`tx`) and received (`rx`) at this millisecond?"_

Polling those byte counters every `5 ms` (`200 Hz`) acts as a high-frequency scale at the container boundary without inspecting encrypted payloads:

- **Request dispatch ($t\_{\text{req}}$)**: Outbound `tx` jumps by `>= 120 B` while `rx` remains `< 90 B`, anchoring the moment the serialized prompt leaves the client.
- **First token arrival ($t\_{\text{first}}$)**: After prefill silence, inbound `rx` jumps by `>= 90 B` (above HTTP/2 control-frame noise), yielding $\text{TTFT} = t_{\text{first}} - t_{\text{req}}$ at millisecond resolution.
- **Pure decode window ($T\_{\text{decode}}$) and `ITL`**: Each subsequent `rx >= 90 B` burst marks a streamed chunk arrival. Inter-burst deltas form the inter-token/chunk latency distribution (`ITL` P50/P95), and $T_{\text{decode}} = t_{\text{last}} - t_{\text{first}}$ isolates pure token generation duration from prefill wait.

When Netlink socket byte counters are masked by a sandbox kernel (such as gVisor `runsc`), `KernelSocketReader` falls back automatically to summing non-loopback interface counters from the container's dedicated `/proc/net/dev` namespace (`engine = "netdev"`).

## 3. Component coordination and invocation timeline

Four roles coordinate over a per-process Unix domain socket (`/tmp/agy_meter_<pid>.sock`). Outside active model invocations, the sampling thread blocks on a `threading.Event` and consumes `0%` CPU:

```text
[agy-mon wrapper] ──spawns──▶ [native agy binary] (direct TTY passthrough)
       │                             │
       │ (200 Hz :443 byte poll      ├── 1. PreInvocation  ──▶ meter_client.py pre  ──┐
       │  during active turns)       ├── 2. PostInvocation ──▶ meter_client.py post ──┼─▶ Unix socket RPC
       ▼                             └── 3. ~300ms refresh ──▶ meter_client.py statusline ◀─ 5-slot ANSI HUD
[NETLINK_SOCK_DIAG / netdev]                                                              │
                                                                                          ▼
[agy-perf CLI (host or container)] ◀─── read-only SQLite URI ─── [~/.gemini/.../meter/metrics.db]
```

Across a single prompt turn:

1. **`PreInvocation` (`0.00 s` — arm sampler)**: `meter_client.py pre` notifies `agy-mon`, waking the `200 Hz` sampler thread, scanning `/proc/<pid>/fd` and `/proc/<pid>/task/*/children` for `:443` socket inodes, and recording baseline `tx`/`rx`. Status bar displays `◐ PREFILL …`.
2. **First valid chunk (`e.g. 4.35 s` — lock `TTFT`)**: Inbound `:443` bytes cross the content threshold; `agy-mon` locks live `TTFT = 4.35s` and switches the HUD to `▶ STREAM │ TTFT 4.35s` while recording subsequent chunk timestamps.
3. **`PostInvocation` (`e.g. 12.10 s` — park trace)**: Model output finishes. `meter_client.py post` pops the active invocation, parks the sampler back into `0%` CPU sleep, selects the primary `:443` socket trace, and queues `ttft_ms`, `decode_ms`, `llm_ms`, `itl_p50`, `itl_p95`, `chunks`, and `rx_bytes` for token reconciliation.
4. **`statusLine` reconciliation (`e.g. 12.20 s` — dual TPS settlement)**: `agy` invokes `statusLine` with updated `context_window.total_output_tokens`. `agy-mon` computes $\Delta N_{\text{out}} = N_{\text{new}} - N_{\text{prev}}$ and settles two complementary throughput metrics:
   - **Pure streaming throughput (`TPS_gen` $= \Delta N\_{\text{out}} / T\_{\text{decode}}$)**: generation speed once streaming begins, excluding prefill and queueing latency.
   - **End-to-end effective throughput (`TPS_eff` $= \Delta N\_{\text{out}} / T\_{\text{llm}}$)**: net token delivery rate across the entire invocation window (queueing + prefill + thinking + decode).
5. **Asynchronous persistence**: A background worker writes the settled record to `~/.gemini/antigravity-cli/meter/metrics.db` using `PRAGMA journal_mode=TRUNCATE`, avoiding shared-memory `-shm` files across container bind mounts while letting `agy-perf` aggregate time-weighted throughput ($\sum \Delta N_{\text{out}} / \sum T$) at any time.

## 4. Anti-distortion calibrations

### Stripping early empty `200 OK` gateway headers

Upstream HTTP/2 gateways frequently flush an initial `200 OK` response header frame (`<= 380 B`) within `< 0.35 s` of request transmission while the model is still queued or running prefill, followed by seconds of silence before the first SSE token arrives. Treating that header burst as $t_{\text{first}}$ would falsely report `~100 ms` `TTFT`.

`analyze_trace` inspects the first burst `(t0, b0)`:

- While streaming is still in progress with a single burst (`len(bursts) == 1`), a small early burst (`b0 <= 380`, `t0 - t_origin < 0.35 s`) followed by `> 0.25 s` of silence stays in `PREFILL` rather than prematurely triggering `STREAM`.
- Once subsequent bursts arrive, if `b0 <= 380`, `t0 - t_origin < 0.35 s`, and the gap to the second burst exceeds `max(250 ms, 4.0 * median_ITL)`, burst `0` is stripped so $t_{\text{first}}$ anchors on the first real SSE token chunk.

### Separating server-side silent thinking from token streaming

Reasoning models often spend tens of seconds generating hidden thinking tokens silently on the server (zero inbound socket bytes) and then flush a compact tool call in `< 0.3 s` over two or three packets, while `total_output_tokens` bills both hidden thinking tokens and visible output tokens together. Dividing billed tokens by a `0.3 s` tail flush would produce an artificial `TPS_gen` in the thousands.

`agy-mon` classifies an invocation as a valid pure stream (`tps_gen` measured) only when all three conditions hold:

1. **Chunk granularity**: `sse_chunks >= 5`;
2. **Sustained decode window**: `decode_ms >= 800 ms`;
3. **Decode share or wire-byte density**: `decode_ms >= 0.45 * llm_ms` or `rx_bytes / output_tokens >= 2.5`.

Invocations that fail this check are recorded with `tps_gen = NULL` (`batch` in `agy-perf tail`), excluded from `agy-perf`'s weighted `TPS_gen` denominator, and rendered in the status bar with a `~` prefix carrying forward the session's most recent valid streaming rate alongside the current step's live `TTFT` and `TPS_eff`.
