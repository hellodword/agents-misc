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
    label: "Required",
    rowKey: "required",
    kinds: new Set(["required_changed"]),
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

const CATEGORY_SEVERITY = [
  "breakingLike",
  "review",
  "behavior",
  "compatible",
  "documentation",
];

export function stableValueKey(value) {
  if (value === null) {
    return "null";
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new Error("JSON numbers must be finite");
    }
    return JSON.stringify(Object.is(value, -0) ? 0 : value);
  }
  if (typeof value === "boolean" || typeof value === "string") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => stableValueKey(item)).join(",")}]`;
  }

  if (typeof value === "object") {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableValueKey(value[key])}`)
      .join(",")}}`;
  }

  throw new Error(`enum value is not JSON-compatible: ${String(value)}`);
}

function compareStableValues(left, right) {
  const leftKey = stableValueKey(left);
  const rightKey = stableValueKey(right);
  return leftKey < rightKey ? -1 : leftKey > rightKey ? 1 : 0;
}

function additionalPropertiesRank(mode) {
  const order = {
    forbid: 0,
    typed: 1,
    allow_any: 2,
  };
  return order[mode] ?? 0;
}

function setIsSubset(left, right) {
  for (const value of left) {
    if (!right.has(value)) {
      return false;
    }
  }
  return true;
}

function setsEqual(left, right) {
  return left.size === right.size && setIsSubset(left, right);
}

function indexEnumValues(field) {
  const index = new Map();
  for (const value of field.enum ?? []) {
    const key = stableValueKey(value);
    if (!index.has(key)) {
      index.set(key, value);
    }
  }
  return index;
}

export function summarizeChanges(changes) {
  const summary = {
    breakingLike: 0,
    review: 0,
    behavior: 0,
    compatible: 0,
    documentation: 0,
  };

  for (const change of changes) {
    if (summary[change.category] !== undefined) {
      summary[change.category] += 1;
    }
  }

  return summary;
}

export function highestChangeCategory(changes) {
  for (const category of CATEGORY_SEVERITY) {
    if (changes.some((change) => change.category === category)) {
      return category;
    }
  }
  throw new Error("change group has no recognized category");
}

export function requiredState(field) {
  const state = field?.required;
  if (!new Set(["always", "conditional", "never"]).has(state)) {
    throw new Error(
      `field ${field?.path ?? "<unknown>"} has invalid required state: ${String(state)}`,
    );
  }
  return state;
}

export function addedFieldCategory(required) {
  if (required === "always") {
    return "breakingLike";
  }
  if (required === "conditional") {
    return "review";
  }
  if (required === "never") {
    return "compatible";
  }
  throw new Error(`invalid required state: ${String(required)}`);
}

export function requiredChangeCategory(before, after) {
  if (after === "always") {
    return "breakingLike";
  }
  if (before === "conditional" || after === "conditional") {
    return "review";
  }
  return "compatible";
}

export function buildSchemaDiff(fromVersion, toVersion, fromFields, toFields) {
  const before = new Map(fromFields.map((field) => [field.path, field]));
  const after = new Map(toFields.map((field) => [field.path, field]));
  for (const field of [...before.values(), ...after.values()]) {
    requiredState(field);
  }
  const changes = [];
  const allPaths = Array.from(
    new Set([...before.keys(), ...after.keys()]),
  ).sort((left, right) => left.localeCompare(right));

  for (const path of allPaths) {
    const left = before.get(path);
    const right = after.get(path);

    if (!left) {
      changes.push({
        kind: "field_added",
        category: addedFieldCategory(right.required),
        path,
        to: {
          types: right.types,
          hasDefault: right.hasDefault,
          required: right.required,
        },
      });
      continue;
    }

    if (!right) {
      changes.push({
        kind: "field_removed",
        category: "breakingLike",
        path,
        from: {
          types: left.types,
          hasDefault: left.hasDefault,
          required: left.required,
        },
      });
      continue;
    }

    const leftTypes = new Set(left.types ?? []);
    const rightTypes = new Set(right.types ?? []);
    if (!setsEqual(leftTypes, rightTypes)) {
      let kind = "type_changed";
      let category = "breakingLike";

      if (setIsSubset(rightTypes, leftTypes)) {
        kind = "type_narrowed";
      } else if (setIsSubset(leftTypes, rightTypes)) {
        kind = "type_widened";
        category = "compatible";
      }

      changes.push({
        kind,
        category,
        path,
        from: Array.from(leftTypes).sort(),
        to: Array.from(rightTypes).sort(),
      });
    }

    const leftEnum = indexEnumValues(left);
    const rightEnum = indexEnumValues(right);
    const removedEnum = Array.from(leftEnum.entries())
      .filter(([key]) => !rightEnum.has(key))
      .map(([, value]) => value)
      .sort(compareStableValues);
    if (removedEnum.length > 0) {
      changes.push({
        kind: "enum_values_removed",
        category: "breakingLike",
        path,
        values: removedEnum,
      });
    }

    const addedEnum = Array.from(rightEnum.entries())
      .filter(([key]) => !leftEnum.has(key))
      .map(([, value]) => value)
      .sort(compareStableValues);
    if (addedEnum.length > 0) {
      changes.push({
        kind: "enum_values_added",
        category: "compatible",
        path,
        values: addedEnum,
      });
    }

    if (left.required !== right.required) {
      changes.push({
        kind: "required_changed",
        category: requiredChangeCategory(left.required, right.required),
        path,
        from: left.required,
        to: right.required,
      });
    }

    const leftMode = left.additionalPropertiesMode;
    const rightMode = right.additionalPropertiesMode;
    if (leftMode !== rightMode) {
      const restricted =
        additionalPropertiesRank(rightMode) <
        additionalPropertiesRank(leftMode);
      changes.push({
        kind: restricted
          ? "additional_properties_restricted"
          : "additional_properties_relaxed",
        category: restricted ? "breakingLike" : "compatible",
        path,
        from: leftMode,
        to: rightMode,
      });
    }

    if (left.hasDefault && right.hasDefault) {
      if (stableValueKey(left.default) !== stableValueKey(right.default)) {
        changes.push({
          kind: "default_changed",
          category: "behavior",
          path,
          from: left.default,
          to: right.default,
        });
      }
    } else if (left.hasDefault && !right.hasDefault) {
      changes.push({
        kind: "default_removed",
        category: "behavior",
        path,
        from: left.default,
      });
    } else if (!left.hasDefault && right.hasDefault) {
      changes.push({
        kind: "default_added",
        category: "behavior",
        path,
        to: right.default,
      });
    }

    if ((left.description ?? "") !== (right.description ?? "")) {
      changes.push({
        kind: "description_changed",
        category: "documentation",
        path,
      });
    }

    if (Boolean(left.deprecated) !== Boolean(right.deprecated)) {
      changes.push({
        kind: "deprecated_changed",
        category: "documentation",
        path,
        from: Boolean(left.deprecated),
        to: Boolean(right.deprecated),
      });
    }
  }

  const visibleChanges = suppressDuplicateProfileChanges(
    changes,
    before,
    after,
  );
  return {
    from: fromVersion,
    to: toVersion,
    summary: summarizeChanges(visibleChanges),
    changes: visibleChanges,
  };
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
      return field.enum && field.enum.length
        ? field.enum.map(stableValueKey).join(", ")
        : "-";
    case "required":
      return requiredState(field);
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

export function changedFieldRows(changes) {
  const kinds = new Set(changes.map((change) => change.kind));
  if (kinds.has("field_added") || kinds.has("field_removed")) {
    return CHANGE_ROW_KEYS.map(({ label, rowKey }) => ({ label, rowKey }));
  }
  return CHANGE_ROW_KEYS.filter((row) =>
    Array.from(row.kinds).some((kind) => kinds.has(kind)),
  ).map(({ label, rowKey }) => ({ label, rowKey }));
}

function detailLines(prefix, label, value) {
  const [firstLine, ...continuationLines] = String(value).split("\n");
  return [
    `${prefix}   ${label}: ${firstLine}`,
    ...continuationLines.map((line) => `${prefix}     ${line}`),
  ];
}

function renderChangedGroup(group) {
  const rows = changedFieldRows(group.changes);
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
