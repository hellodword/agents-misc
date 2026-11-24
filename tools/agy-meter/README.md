# AGY Meter

`agy-meter` adds a persistent five-slot real-time status bar (`TTFT`, streaming `TPS_gen`, end-to-end `TPS_eff`, output token delta, and context usage) and historical SQLite telemetry to Antigravity CLI (`agy`) on Linux hosts and inside unprivileged `runc` or `runsc` containers, using Python 3's standard library.

See [architecture.md](architecture.md) for the kernel socket byte-sampling model, IPC request timeline, and streaming signal calibrations.

## Layout

```text
tools/agy-meter/
  README.md
  architecture.md
  agy-mon                # Wrapper daemon: spawns agy, samples :443 bytes, serves HUD & SQLite
  meter_client.py        # Hook (pre/post) and statusLine RPC client
  agy-perf               # Read-only SQLite multi-dimensional query CLI
  examples/
    hooks.json           # ~/.gemini/config/hooks.json snippet
    settings.json        # ~/.gemini/antigravity-cli/settings.json snippet
```

## Setup

Installing the executables under `~/.gemini/antigravity-cli/meter` makes both the meter scripts and `metrics.db` available simultaneously on the host and inside any container mounting `~/.gemini`.

1. **Install the executables**:

```bash
mkdir -p ~/.gemini/antigravity-cli/meter ~/.gemini/config
install -m 755 \
  tools/agy-meter/agy-mon \
  tools/agy-meter/meter_client.py \
  tools/agy-meter/agy-perf \
  ~/.gemini/antigravity-cli/meter/
```

2. **Register lifecycle hooks and `statusLine`**:

Merge [`examples/hooks.json`](examples/hooks.json) into `~/.gemini/config/hooks.json` and [`examples/settings.json`](examples/settings.json) into `~/.gemini/antigravity-cli/settings.json` (preserving any existing keys in both files).

## Running `agy` with live telemetry

Replace `agy` with `agy-mon` and pass any native `agy` arguments directly after it. Inside an unprivileged container (`runc` or `--runtime=runsc`) with `python3` installed:

```bash
docker run -it --rm \
  -v "$HOME/.gemini:/root/.gemini" \
  -v "$(pwd):/workspace" -w /workspace \
  your-agy-image \
  /root/.gemini/antigravity-cli/meter/agy-mon
```

On the first visit to a workspace directory, confirm workspace trust inside `agy` so `PreInvocation` and `PostInvocation` hooks execute.

Override `AGY_REAL_BIN` when the underlying `agy` binary sits outside `PATH` and `~/.local/bin/agy`, or `AGY_METER_DIR` to relocate `metrics.db` (default `~/.gemini/antigravity-cli/meter`).

## Five-slot status bar

`statusLine` renders five anchored slots (`State │ TTFT │ TPS │ Out │ Ctx`):

- **Prefill / waiting for first token**:
  `◐ PREFILL 2.4s │ TTFT …2.4s (last 4.12s) │ TPS 54.2 gen / 43.8 eff │ Out +620 (5.1k) │ Ctx 8%`
- **Active token streaming**:
  `▶ STREAM 9.6s │ TTFT 4.35s │ TPS 54.2 gen / 43.8 eff │ Out +620 (5.1k) │ Ctx 8%`
- **Settled streaming turn**:
  `● READY │ TTFT 4.35s │ TPS 56.8 gen / 45.1 eff │ Out +940 (6.0k) │ Ctx 9%`
- **Settled batch / silent-thinking tool turn**:
  `⚙ TOOL │ TTFT 18.20s │ TPS ~56.8 gen / 36.4 eff │ Out +710 (6.7k) │ Ctx 10%`
  (`~` marks a carried-forward streaming `TPS_gen` from the most recent multi-chunk stream when the current step flushed in a single batch after server-side thinking; `TTFT` and `eff` always reflect the current step).

## Historical queries (`agy-perf`)

`agy-perf` opens `metrics.db` in read-only URI mode (`file:...?mode=ro`) and runs either on the host or inside the container:

```text
agy-perf [models|sessions|tail] [--last WINDOW] [--since TIME] [--until TIME] [--cid PREFIX] [-n ROWS] [--db PATH] [--json]
```

Exit status is `0` on success, `2` on invalid flags or time expressions, and `1` when the database file is missing or unreadable (diagnostics go to `stderr`; empty result sets print `No matching records.` or `[]` with `--json`).

```bash
# Time-weighted TPS_gen, TPS_eff, TTFT (P50/P95), and ITL (P50/P95) grouped by model
~/.gemini/antigravity-cli/meter/agy-perf models --last 24h

# Per-conversation performance across a date range
~/.gemini/antigravity-cli/meter/agy-perf sessions --since "2026-11-20" --until "2026-11-24"

# Last 20 raw model invocations (labels streaming TPS_gen or 'batch')
~/.gemini/antigravity-cli/meter/agy-perf tail -n 20

# Export structured JSON summary
~/.gemini/antigravity-cli/meter/agy-perf models --last 7d --json
```
