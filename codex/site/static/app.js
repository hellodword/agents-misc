const state = {
  meta: null,
  versionCache: new Map(),
  diffCache: new Map(),
  selection: null,
};

const GROUP_SECTIONS = [
  {
    id: "defaults",
    title: "Defaults changed",
    chipClass: "chip-behavior",
  },
  {
    id: "removed",
    title: "Removed configs",
    chipClass: "chip-breaking-like",
  },
  {
    id: "added",
    title: "Added configs",
    chipClass: "chip-compatible",
  },
  {
    id: "deprecated",
    title: "Deprecated configs",
    chipClass: "chip-documentation",
  },
  {
    id: "docsOnly",
    title: "Docs-only changes",
    chipClass: "chip-documentation",
  },
  {
    id: "other",
    title: "Other changes",
    chipClass: "chip",
  },
];

function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function versionParts(version) {
  return version.split(".").map((part) => Number(part));
}

function compareVersions(a, b) {
  const left = versionParts(a);
  const right = versionParts(b);
  for (let index = 0; index < Math.max(left.length, right.length); index += 1) {
    const diff = (left[index] ?? 0) - (right[index] ?? 0);
    if (diff !== 0) {
      return diff;
    }
  }
  return 0;
}

function sortedVersions() {
  return state.meta.versions.map((item) => item.version).sort(compareVersions);
}

function defaultSelection() {
  const versions = sortedVersions();
  const toVersion = versions.at(-1);
  const fromVersion = versions.at(-2) ?? toVersion;
  return { fromVersion, toVersion };
}

function normalizeSelection(fromVersion, toVersion) {
  const versions = sortedVersions();
  let from = versions.includes(fromVersion) ? fromVersion : versions[0];
  let to = versions.includes(toVersion) ? toVersion : versions.at(-1);

  if (compareVersions(to, from) < 0) {
    to = from;
  }

  return { fromVersion: from, toVersion: to };
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

function renderFieldValue(field, rowKey) {
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
    default:
      return "-";
  }
}

async function loadJson(path) {
  const response = await fetch(path);
  if (!response.ok) {
    throw new Error(`Failed to load ${path}`);
  }
  return response.json();
}

async function loadVersions() {
  return loadJson("data/versions.json");
}

async function loadVersion(version) {
  if (state.versionCache.has(version)) {
    return state.versionCache.get(version);
  }

  const fieldsPayload = await loadJson(`data/versions/${version}/fields.json`);

  const payload = {
    fields: fieldsPayload.fields,
    fieldIndex: new Map(
      fieldsPayload.fields.map((field) => [field.path, field]),
    ),
  };
  state.versionCache.set(version, payload);
  return payload;
}

async function loadDiff(fromVersion, toVersion) {
  const key = `${fromVersion}..${toVersion}`;
  if (!state.diffCache.has(key)) {
    state.diffCache.set(key, loadJson(`data/diffs/${key}.json`));
  }
  return state.diffCache.get(key);
}

function versionOptions(selected, predicate) {
  return sortedVersions()
    .filter(predicate)
    .map((version) => {
      const active = version === selected ? " selected" : "";
      return `<option value="${escapeHtml(version)}"${active}>${escapeHtml(version)}</option>`;
    })
    .join("");
}

function groupChanges(diffPayload, beforePayload, afterPayload) {
  const grouped = new Map();
  for (const change of diffPayload.changes) {
    if (!grouped.has(change.path)) {
      grouped.set(change.path, {
        path: change.path,
        changes: [],
        beforeField: beforePayload.fieldIndex.get(change.path) || null,
        afterField: afterPayload.fieldIndex.get(change.path) || null,
      });
    }

    const entry = grouped.get(change.path);
    entry.changes.push(change);
  }

  return Array.from(grouped.values())
    .map((group) => ({
      ...group,
      sectionId: resolveGroupSection(group),
    }))
    .sort((left, right) => left.path.localeCompare(right.path));
}

function resolveGroupSection(group) {
  const kinds = new Set(group.changes.map((change) => change.kind));

  if (
    kinds.has("default_changed") ||
    kinds.has("default_added") ||
    kinds.has("default_removed")
  ) {
    return "defaults";
  }

  if (kinds.has("field_removed")) {
    return "removed";
  }

  if (kinds.has("field_added")) {
    return "added";
  }

  if (kinds.has("deprecated_changed")) {
    return "deprecated";
  }

  if (group.changes.every((change) => change.kind === "description_changed")) {
    return "docsOnly";
  }

  return "other";
}

function partitionGroups(groups) {
  const buckets = new Map(GROUP_SECTIONS.map((section) => [section.id, []]));
  for (const group of groups) {
    buckets.get(group.sectionId).push(group);
  }

  return GROUP_SECTIONS.map((section) => ({
    ...section,
    groups: buckets.get(section.id),
  })).filter((section) => section.groups.length > 0);
}

function renderDiffSummary(diffPayload, sections, fromVersion, toVersion) {
  return `
    <div class="summary-grid">
      <div class="stat">
        <p class="stat-label">Comparing</p>
        <p class="stat-value">${escapeHtml(fromVersion)} -> ${escapeHtml(toVersion)}</p>
      </div>
      <div class="stat">
        <p class="stat-label">Changed Fields</p>
        <p class="stat-value">${sections.reduce((sum, section) => sum + section.groups.length, 0)}</p>
      </div>
      <div class="stat">
        <p class="stat-label">Change Events</p>
        <p class="stat-value">${diffPayload.changes.length}</p>
      </div>
    </div>
    <div class="chip-row">
      ${sections
        .map(
          (section) =>
            `<span class="chip ${section.chipClass}">${escapeHtml(
              section.title,
            )} ${section.groups.length}</span>`,
        )
        .join("")}
    </div>
  `;
}

function renderDiffGroup(group) {
  const rows = [
    ["Type", "type"],
    ["Default", "default"],
    ["Optional", "optional"],
    ["Description", "description"],
  ];

  if (
    (group.beforeField?.enum && group.beforeField.enum.length) ||
    (group.afterField?.enum && group.afterField.enum.length)
  ) {
    rows.splice(2, 0, ["Enum", "enum"]);
  }

  return `
    <article class="change-item field-change-item">
      <div>
        <p class="field-path"><code>${escapeHtml(group.path)}</code></p>
      </div>
      <div class="field-matrix">
        <div class="field-matrix-header"></div>
        <div class="field-matrix-header">${escapeHtml(state.selection.fromVersion)}</div>
        <div class="field-matrix-header">${escapeHtml(state.selection.toVersion)}</div>
        ${rows
          .map(
            ([label, rowKey]) => `
              <div class="field-matrix-label">${escapeHtml(label)}</div>
              <div class="field-matrix-value">${escapeHtml(
                renderFieldValue(group.beforeField, rowKey),
              )}</div>
              <div class="field-matrix-value">${escapeHtml(
                renderFieldValue(group.afterField, rowKey),
              )}</div>
            `,
          )
          .join("")}
      </div>
    </article>
  `;
}

function renderDiffSection(section) {
  return `
    <section class="card">
      <div class="section-head">
        <h3>${escapeHtml(section.title)}</h3>
        <p class="muted">${section.groups.length} item${section.groups.length === 1 ? "" : "s"}</p>
      </div>
      <div class="change-list">
        ${section.groups.map((group) => renderDiffGroup(group)).join("")}
      </div>
    </section>
  `;
}

function renderDiffContent(
  fromVersion,
  toVersion,
  fromPayload,
  toPayload,
  diffPayload,
) {
  const groups = groupChanges(diffPayload, fromPayload, toPayload);
  const sections = partitionGroups(groups);
  const diffCards = sections.length
    ? sections.map((section) => renderDiffSection(section)).join("")
    : '<div class="empty-card">No differences for the selected versions.</div>';

  return `
    ${renderDiffSummary(diffPayload, sections, fromVersion, toVersion)}
    <section class="stack">
      ${diffCards}
    </section>
  `;
}

async function renderApp() {
  try {
    if (!state.meta) {
      state.meta = await loadVersions();
    }

    if (!state.selection) {
      state.selection = defaultSelection();
    } else {
      state.selection = normalizeSelection(
        state.selection.fromVersion,
        state.selection.toVersion,
      );
    }

    const { fromVersion, toVersion } = state.selection;
    const [fromPayload, toPayload] = await Promise.all([
      loadVersion(fromVersion),
      loadVersion(toVersion),
    ]);
    const diffPayload =
      fromVersion === toVersion
        ? {
            summary: {
              breakingLike: 0,
              behavior: 0,
              compatible: 0,
              documentation: 0,
            },
            changes: [],
          }
        : await loadDiff(fromVersion, toVersion);

    document.getElementById("app").innerHTML = `
      <section class="stack">
        <div class="card controls">
          <div class="control-row">
            <div class="control">
              <label for="from-select">From</label>
              <select id="from-select">${versionOptions(
                fromVersion,
                (version) => compareVersions(version, toVersion) <= 0,
              )}</select>
            </div>
            <div class="control">
              <label for="to-select">To</label>
              <select id="to-select">${versionOptions(
                toVersion,
                (version) => compareVersions(version, fromVersion) >= 0,
              )}</select>
            </div>
          </div>
          <div class="chip-row">
            <span class="chip">current ${escapeHtml(state.meta.current)}</span>
            <span class="chip">${state.meta.versions.length} tagged versions</span>
          </div>
        </div>
        ${renderDiffContent(
          fromVersion,
          toVersion,
          fromPayload,
          toPayload,
          diffPayload,
        )}
      </section>
    `;

    bindControls();
  } catch (error) {
    document.getElementById("app").innerHTML = `
      <div class="empty-card">
        <p><strong>Failed to load data.</strong></p>
        <p class="muted">${escapeHtml(error.message || String(error))}</p>
      </div>
    `;
  }
}

function bindControls() {
  document.getElementById("from-select").addEventListener("change", (event) => {
    state.selection = normalizeSelection(
      event.target.value,
      state.selection.toVersion,
    );
    renderApp();
  });

  document.getElementById("to-select").addEventListener("change", (event) => {
    state.selection = normalizeSelection(
      state.selection.fromVersion,
      event.target.value,
    );
    renderApp();
  });
}

renderApp();
