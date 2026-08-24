import {
  buildSchemaDiff,
  changedFieldRows,
  formatDeveloperDiff,
  highestChangeCategory,
  renderFieldValue,
  summarizeChanges,
} from "./diff_helpers.mjs";

const state = {
  meta: null,
  versionCache: new Map(),
  diffCache: new Map(),
  selection: null,
};

const GROUP_SECTIONS = [
  {
    id: "breakingLike",
    title: "Breaking-like",
    chipClass: "chip-breaking-like",
  },
  {
    id: "review",
    title: "Needs review",
    chipClass: "chip-review",
  },
  {
    id: "behavior",
    title: "Behavior",
    chipClass: "chip-behavior",
  },
  {
    id: "compatible",
    title: "Compatible",
    chipClass: "chip-compatible",
  },
  {
    id: "documentation",
    title: "Documentation",
    chipClass: "chip-documentation",
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

function emptyDiffPayload(fromVersion, toVersion) {
  return {
    from: fromVersion,
    to: toVersion,
    summary: summarizeChanges([]),
    changes: [],
  };
}

function getDiffPayload(fromVersion, toVersion, fromPayload, toPayload) {
  const key = `${fromVersion}..${toVersion}`;
  if (!state.diffCache.has(key)) {
    state.diffCache.set(
      key,
      buildSchemaDiff(
        fromVersion,
        toVersion,
        fromPayload.fields,
        toPayload.fields,
      ),
    );
  }
  return state.diffCache.get(key);
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
  return highestChangeCategory(group.changes);
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

function renderDiffSummary(diffPayload, groups, fromVersion, toVersion) {
  return `
    <div class="summary-grid">
      <div class="stat">
        <p class="stat-label">Comparing</p>
        <p class="stat-value">${escapeHtml(fromVersion)} -> ${escapeHtml(toVersion)}</p>
      </div>
      <div class="stat">
        <p class="stat-label">Changed Fields</p>
        <p class="stat-value">${groups.length}</p>
      </div>
      <div class="stat">
        <p class="stat-label">Change Events</p>
        <p class="stat-value">${diffPayload.changes.length}</p>
      </div>
    </div>
    <div class="chip-row">
      ${GROUP_SECTIONS.filter((section) => diffPayload.summary[section.id] > 0)
        .map(
          (section) =>
            `<span class="chip ${section.chipClass}">${escapeHtml(
              section.title,
            )} ${diffPayload.summary[section.id]}</span>`,
        )
        .join("")}
    </div>
  `;
}

function renderDiffGroup(group) {
  const rows = changedFieldRows(group.changes);

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
            ({ label, rowKey }) => `
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

function renderDiffContent(fromVersion, toVersion, diffPayload, groups) {
  const sections = partitionGroups(groups);
  const diffCards = sections.length
    ? sections.map((section) => renderDiffSection(section)).join("")
    : '<div class="empty-card">No differences for the selected versions.</div>';

  return `
    ${renderDiffSummary(diffPayload, groups, fromVersion, toVersion)}
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
        ? emptyDiffPayload(fromVersion, toVersion)
        : getDiffPayload(fromVersion, toVersion, fromPayload, toPayload);
    const groups = groupChanges(diffPayload, fromPayload, toPayload);
    console.log(formatDeveloperDiff(groups));

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
        ${renderDiffContent(fromVersion, toVersion, diffPayload, groups)}
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
