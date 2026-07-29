const PROFILE_PATH_PREFIX = "profiles.<name>.";

const FIELD_SEMANTIC_KEYS = [
  "kind",
  "types",
  "required",
  "hasDefault",
  "default",
  "enum",
  "description",
  "deprecated",
  "additionalPropertiesMode",
];

const CHANGE_ROW_KEYS = [
  {
    label: "Type",
    rowKey: "type",
    kinds: new Set(["type_narrowed", "type_widened", "type_changed"]),
  },
  {
    label: "Default",
    rowKey: "default",
    kinds: new Set(["default_changed", "default_added", "default_removed"]),
  },
  {
    label: "Enum",
    rowKey: "enum",
    kinds: new Set(["enum_values_added", "enum_values_removed"]),
  },
  {
    label: "Optional",
    rowKey: "optional",
    kinds: new Set(["required_became_true", "required_became_false"]),
  },
  {
    label: "Description",
    rowKey: "description",
    kinds: new Set(["description_changed"]),
  },
  {
    label: "Deprecated",
    rowKey: "deprecated",
    kinds: new Set(["deprecated_changed"]),
  },
  {
    label: "Additional properties",
    rowKey: "additionalProperties",
    kinds: new Set([
      "additional_properties_restricted",
      "additional_properties_relaxed",
    ]),
  },
];

export function stableValueKey(value) {
  if (Array.isArray(value)) {
    return `[${value.map((item) => stableValueKey(item)).join(",")}]`;
  }

  if (value && typeof value === "object") {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableValueKey(value[key])}`)
      .join(",")}}`;
  }

  const encoded = JSON.stringify(value);
  return typeof encoded === "string" ? encoded : String(value);
}

function changeWithoutPath(change) {
  return Object.fromEntries(
    Object.entries(change).filter(([key]) => key !== "path"),
  );
}

function fieldSemantics(field) {
  if (!field) {
    return null;
  }
  return Object.fromEntries(
    FIELD_SEMANTIC_KEYS.map((key) => [key, field[key] ?? null]),
  );
}

function isDuplicateProfileChange(
  change,
  baseChanges,
  before,
  after,
  basePath,
) {
  const signature = stableValueKey(changeWithoutPath(change));
  if (
    !baseChanges.some(
      (candidate) => stableValueKey(changeWithoutPath(candidate)) === signature,
    )
  ) {
    return false;
  }

  const path = change.path;
  if (change.kind === "field_added") {
    return (
      stableValueKey(fieldSemantics(after.get(path))) ===
      stableValueKey(fieldSemantics(after.get(basePath)))
    );
  }
  if (change.kind === "field_removed") {
    return (
      stableValueKey(fieldSemantics(before.get(path))) ===
      stableValueKey(fieldSemantics(before.get(basePath)))
    );
  }
  if (change.kind === "description_changed") {
    const profileDescriptions = [
      before.get(path)?.description ?? "",
      after.get(path)?.description ?? "",
    ];
    const baseDescriptions = [
      before.get(basePath)?.description ?? "",
      after.get(basePath)?.description ?? "",
    ];
    return (
      stableValueKey(profileDescriptions) === stableValueKey(baseDescriptions)
    );
  }
  return true;
}

export function suppressDuplicateProfileChanges(changes, before, after) {
  const changesByPath = new Map();
  for (const change of changes) {
    if (!changesByPath.has(change.path)) {
      changesByPath.set(change.path, []);
    }
    changesByPath.get(change.path).push(change);
  }

  return changes.filter((change) => {
    if (!change.path.startsWith(PROFILE_PATH_PREFIX)) {
      return true;
    }
    const basePath = change.path.slice(PROFILE_PATH_PREFIX.length);
    return !isDuplicateProfileChange(
      change,
      changesByPath.get(basePath) ?? [],
      before,
      after,
      basePath,
    );
  });
}

function formatValue(value) {
  if (typeof value === "undefined") {
    return "—";
  }
  return JSON.stringify(value);
}

function formatTypeList(types) {
  return types && types.length ? types.join(" | ") : "—";
}

export function renderFieldValue(field, rowKey) {
  if (!field) {
    return "-";
  }

  switch (rowKey) {
    case "type":
      return formatTypeList(field.types);
    case "default":
      return field.hasDefault ? formatValue(field.default) : "-";
    case "enum":
      return field.enum && field.enum.length ? field.enum.join(", ") : "-";
    case "optional":
      return field.required ? "no" : "yes";
    case "description":
      return field.description || "-";
    case "deprecated":
      return field.deprecated ? "yes" : "no";
    case "additionalProperties":
      return field.additionalPropertiesMode || "-";
    default:
      return "-";
  }
}

function changedRows(group) {
  const kinds = new Set(group.changes.map((change) => change.kind));
  return CHANGE_ROW_KEYS.filter((row) =>
    Array.from(row.kinds).some((kind) => kinds.has(kind)),
  );
}

function detailLines(prefix, label, value) {
  const [firstLine, ...continuationLines] = String(value).split("\n");
  return [
    `${prefix}   ${label}: ${firstLine}`,
    ...continuationLines.map((line) => `${prefix}     ${line}`),
  ];
}

function renderChangedGroup(group) {
  const rows = changedRows(group);
  const beforeLines = [`- ${group.path}`];
  const afterLines = [`+ ${group.path}`];
  for (const row of rows) {
    beforeLines.push(
      ...detailLines(
        "-",
        row.label,
        renderFieldValue(group.beforeField, row.rowKey),
      ),
    );
    afterLines.push(
      ...detailLines(
        "+",
        row.label,
        renderFieldValue(group.afterField, row.rowKey),
      ),
    );
  }
  return [...beforeLines, ...afterLines].join("\n");
}

export function formatDeveloperDiff(groups) {
  const sortedGroups = [...groups].sort((left, right) =>
    left.path.localeCompare(right.path),
  );
  const added = sortedGroups.filter((group) =>
    group.changes.some((change) => change.kind === "field_added"),
  );
  const removed = sortedGroups.filter((group) =>
    group.changes.some((change) => change.kind === "field_removed"),
  );
  const changed = sortedGroups.filter(
    (group) => !added.includes(group) && !removed.includes(group),
  );
  const sections = [];

  if (added.length > 0) {
    sections.push(
      ["# 新增", ...added.map((group) => `+ ${group.path}`)].join("\n"),
    );
  }
  if (removed.length > 0) {
    sections.push(
      ["# 移除", ...removed.map((group) => `- ${group.path}`)].join("\n"),
    );
  }
  if (changed.length > 0) {
    sections.push(
      ["# 变更", changed.map((group) => renderChangedGroup(group)).join("\n\n")]
        .filter(Boolean)
        .join("\n"),
    );
  }

  return sections.length > 0 ? sections.join("\n\n\n") : "# 无变更";
}
