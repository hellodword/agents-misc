# Codex Config Atlas 例行新增版本操作手册（SOP）

本手册面向例行收录上游 OpenAI Codex 新发布版本的执行者与自动化代理。**例行同步只需阅读本文件并按顺序执行下方固定命令，无需阅读 `README.md`、`src/`、`web/`、`tests/` 或加载 `Contracts` / `Generated artifacts` 等开发规则。**

## 一、三条硬性红线

1. **只允许通过命令更新 `tools/codex-config-atlas/schemas/**`**：严禁手动编辑 `manifest.json`、`metadata.json` 或 `config.schema.json`；严禁运行 `build-data` 或 `build-site` 向工作区写入生成目录（站点与数据完全由 Nix 在构建期生成）。
2. **严禁修改 `src/`、`web/`、`tests/`、`nix/`**：例行版本维护是纯数据收录任务。只要遇到 `ESCALATION REQUIRED` 或任何单测/构建失败，**唯一正确动作是立刻停手并原样上报报错信息**，绝不允许自行修改代码或放宽测试断言。
3. **遵守仓库 Git 安全约束**：未获明确提交授权前绝不执行 `git commit`（保留 `--intent-to-add` 状态并在报告中列出这些路径即可）；获授权提交时严禁使用 `git add .` 或 `git add -A`，只允许按显式文件路径暂存。

---

## 二、标准两步执行流程（在仓库根目录运行）

### 第 1 步：自动发现并渐进同步缺失正式版

```bash
nix run .#codex-config-atlas -- sync-releases --repo . --intent-to-add
```

- 该命令会自动分页查询上游正式发布的稳定版（自动过滤 `draft`、`prerelease` 与非 `rust-v<major>.<minor>.<patch>` 标签），按版本从旧到新逐个下载、完成语义与 TOML 预检、原子写入 `schemas/`，并对新增文件执行精确路径 `git add -N`（以便后续 Nix Flake 构建可见）。
- **若输出 `up to date: ...`**：说明当前无缺失正式版，无需跑第 2 步，直接汇报“已与上游最新正式版同步，无新增版本”即可结束。

### 第 2 步：运行一键门禁验收

当第 1 步同步了至少 1 个新版本时，运行：

```bash
bash tools/codex-config-atlas/scripts/verify.sh
```

- 该脚本会自动检查工作区卫生与 `git add -N` 状态，并一次性跑通与 CI 完全一致的 5 项 Nix 检查（`formatting`、`registry`、`data`、`site`、`tests`）。
- 全量历史版本两两对比通常耗时 **2～4 分钟**（调用工具执行命令时请将单次命令超时设为至少 `300s`），请耐心等待其正常退出并打印 `=== Atlas Verification Summary ===`，不要中途强杀。

---

## 三、交付汇报与（仅限获授权时）提交规范

### 1. 未要求提交时

直接根据 `verify.sh` 末尾打印的摘要汇报：

- 本次新增收录的版本号范围与当前最新版本号；
- `bash tools/codex-config-atlas/scripts/verify.sh` 通过结果；
- 摘要中列出的全部 `Intent-to-add paths`。

### 2. 用户或任务已明确授权创建本地提交时

仅暂存 `manifest.json` 及本次新增版本目录下的两个文件（严禁 `git add .`），使用统一提交标题格式：

- **仅新增 1 个版本 `<V>`**：
  ```bash
  git add -- \
    tools/codex-config-atlas/schemas/manifest.json \
    tools/codex-config-atlas/schemas/rust-v<V>/config.schema.json \
    tools/codex-config-atlas/schemas/rust-v<V>/metadata.json
  git diff --cached --stat
  git commit -m "feat(atlas): add schema for Codex <V>"
  ```
- **新增多个版本 `<FIRST>` 至 `<LAST>`**（把中间每个新增版本的两个文件同样显式列出）：
  ```bash
  git add -- \
    tools/codex-config-atlas/schemas/manifest.json \
    tools/codex-config-atlas/schemas/rust-v<FIRST>/config.schema.json \
    tools/codex-config-atlas/schemas/rust-v<FIRST>/metadata.json \
    tools/codex-config-atlas/schemas/rust-v<LAST>/config.schema.json \
    tools/codex-config-atlas/schemas/rust-v<LAST>/metadata.json
  git diff --cached --stat
  git commit -m "feat(atlas): add schemas for Codex <FIRST> through <LAST>"
  ```

---

## 四、异常情况速查表（无需猜测，按表执行）

| 看到的输出 / 报错                                        | 唯一正确动作                                                                                                                                                                                                                                                                                                         |
| :------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `up to date: ...`                                        | 直接结束并汇报当前已是最新正式版，无需改动任何文件。                                                                                                                                                                                                                                                                 |
| `GitHub API rate limit or access error (HTTP 403 / 429)` | 若 `command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1` 成功，带令牌重试一次：`GH_TOKEN="$(gh auth token)" nix run .#codex-config-atlas -- sync-releases --repo . --intent-to-add`；否则若报错中 `preserved synced versions` 不为 `none` 则对已保留版本跑一次 `verify.sh`，随后直接停止并汇报配额阻塞。 |
| `ESCALATION REQUIRED for Codex <V>`                      | 若报错中 `preserved synced versions` 不为 `none`，对已保留版本运行一次 `bash tools/codex-config-atlas/scripts/verify.sh` 确认健康（若为 `none` 则无需运行）；**严禁修改任何代码或测试**，立刻停手并将失败版本号 `<V>` 与完整报错原样上报。                                                                           |
| `untracked files under ... are invisible to Nix flakes`  | 复制报错提示中给出的精确 `git add -N -- ...` 命令执行一次，再重跑 `bash tools/codex-config-atlas/scripts/verify.sh`。                                                                                                                                                                                                |
