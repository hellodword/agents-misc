import assert from "node:assert/strict";
import test from "node:test";

import {
  formatDeveloperDiff,
  suppressDuplicateProfileChanges,
} from "../web/diff_helpers.mjs";

function field(path, overrides = {}) {
  return {
    path,
    kind: "scalar",
    types: ["string"],
    required: false,
    hasDefault: false,
    default: null,
    enum: null,
    description: "Shared description",
    deprecated: false,
    additionalPropertiesMode: null,
    ...overrides,
  };
}

test("suppresses only semantically identical profile changes", () => {
  const rootPath = "feature.value";
  const profilePath = "profiles.<name>.feature.value";
  const changes = [
    {
      kind: "default_changed",
      category: "behavior",
      path: rootPath,
      from: 1,
      to: 2,
    },
    {
      kind: "default_changed",
      category: "behavior",
      path: profilePath,
      from: 1,
      to: 2,
    },
  ];
  const before = new Map([
    [rootPath, field(rootPath, { hasDefault: true, default: 1 })],
    [profilePath, field(profilePath, { hasDefault: true, default: 1 })],
  ]);
  const after = new Map([
    [rootPath, field(rootPath, { hasDefault: true, default: 2 })],
    [profilePath, field(profilePath, { hasDefault: true, default: 2 })],
  ]);

  assert.deepEqual(
    suppressDuplicateProfileChanges(changes, before, after).map(
      (change) => change.path,
    ),
    [rootPath],
  );

  const distinctChanges = structuredClone(changes);
  distinctChanges[1].to = 3;
  assert.deepEqual(
    suppressDuplicateProfileChanges(distinctChanges, before, after).map(
      (change) => change.path,
    ),
    [rootPath, profilePath],
  );
});

test("formats the developer-console diff contract", () => {
  const oldLogDescription =
    "Directory where Codex writes log files, for example `codex-tui.log`. Defaults to `$CODEX_HOME/log`.";
  const newLogDescription =
    "Directory where Codex writes log files. Setting this value explicitly also enables the TUI text log in this directory. Defaults to `$CODEX_HOME/log`.";
  const groups = [
    {
      path: "allow_login_shell",
      changes: [{ kind: "default_removed" }],
      beforeField: field("allow_login_shell", {
        hasDefault: true,
        default: true,
      }),
      afterField: field("allow_login_shell"),
    },
    {
      path: "features.deferred_tool_world_state",
      changes: [{ kind: "field_added" }],
      beforeField: null,
      afterField: field("features.deferred_tool_world_state"),
    },
    {
      path: "mcp_servers.<name>.experimental_environment",
      changes: [{ kind: "field_removed" }],
      beforeField: field("mcp_servers.<name>.experimental_environment"),
      afterField: null,
    },
    {
      path: "log_dir",
      changes: [{ kind: "description_changed" }],
      beforeField: field("log_dir", { description: oldLogDescription }),
      afterField: field("log_dir", { description: newLogDescription }),
    },
    {
      path: "features.code_mode_host.disable_in_process_fallback",
      changes: [{ kind: "field_added" }],
      beforeField: null,
      afterField: field("features.code_mode_host.disable_in_process_fallback"),
    },
  ];

  assert.equal(
    formatDeveloperDiff(groups),
    `# 新增
+ features.code_mode_host.disable_in_process_fallback
+ features.deferred_tool_world_state


# 移除
- mcp_servers.<name>.experimental_environment


# 变更
- allow_login_shell
-   Default: true
+ allow_login_shell
+   Default: -

- log_dir
-   Description: ${oldLogDescription}
+ log_dir
+   Description: ${newLogDescription}`,
  );
});
