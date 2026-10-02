import {
  buildSchemaDiff,
  changedFieldRows,
  formatDeveloperDiff,
  highestChangeCategory,
  renderFieldValue,
} from "./diff_helpers.mjs";

const CATEGORIES = [
  {
    id: "breakingLike",
    title: "可能不兼容",
  },
  {
    id: "review",
    title: "需要确认",
  },
  {
    id: "behavior",
    title: "行为变化",
  },
  {
    id: "compatible",
    title: "兼容变更",
  },
  {
    id: "documentation",
    title: "文档更新",
  },
];
const state = {
  meta: null,
  fields: new Map(),
  examples: new Map(),
  view: "diff",
  from: "",
  to: "",
  version: "",
  query: "",
  category: "all",
  section: "all",
  groups: [],
  example: "",
  request: 0,
};
const app = document.getElementById("app");
const status = document.getElementById("status");

function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
function versions() {
  // The registry owns stable semantic-version ordering.
  return state.meta.versions.map((entry) => entry.version);
}
function readLocation() {
  const params = new URLSearchParams(location.search);
  const all = versions();
  const valid = (value, fallback) => (all.includes(value) ? value : fallback);
  state.view = params.get("view") === "example" ? "example" : "diff";
  // Read only the active view; keep the other view's selections in memory.
  if (state.view === "diff") {
    state.from = valid(params.get("from"), all.at(-2) ?? all.at(-1));
    state.to = valid(params.get("to"), all.at(-1));
    state.query = params.get("q") ?? "";
    state.category = CATEGORIES.some(
      (category) => category.id === params.get("category"),
    )
      ? params.get("category")
      : "all";
  } else {
    state.version = valid(params.get("version"), all.at(-1));
    state.section = params.get("section") ?? "all";
  }
  state.from ||= all.at(-2) ?? all.at(-1);
  state.to ||= all.at(-1);
  state.version ||= state.to;
}
function writeLocation(push = false) {
  const url = new URL(location.href);
  url.search = "";
  url.searchParams.set("view", state.view);
  if (state.view === "diff") {
    url.searchParams.set("from", state.from);
    url.searchParams.set("to", state.to);
    if (state.query) url.searchParams.set("q", state.query);
    if (state.category !== "all")
      url.searchParams.set("category", state.category);
  } else {
    url.searchParams.set("version", state.version);
    if (state.section !== "all")
      url.searchParams.set("section", state.section);
  }
  history[push ? "pushState" : "replaceState"](null, "", url);
}
async function fetchContent(path, json = true) {
  const response = await fetch(path);
  if (!response.ok) throw new Error(`${path}: HTTP ${response.status}`);
  return json ? response.json() : response.text();
}
function cached(cache, version, file, json = true) {
  if (!cache.has(version)) {
    cache.set(
      version,
      fetchContent(`data/versions/${version}/${file}`, json).catch((error) => {
        cache.delete(version);
        throw error;
      }),
    );
  }
  return cache.get(version);
}
function versionOptions(selected) {
  return [...versions()]
    .reverse()
    .map(
      (version) =>
        `<option value="${escapeHtml(version)}"${version === selected ? " selected" : ""}>${escapeHtml(version)}${version === versions().at(-1) ? " · 最新" : ""}</option>`,
    )
    .join("");
}
function sourceLink(version) {
  const entry = state.meta.versions.find((item) => item.version === version);
  return `<a class="source-link" href="${escapeHtml(entry.schemaUrl)}" target="_blank" rel="noreferrer">Schema ↗<span class="sr-only">（${escapeHtml(version)}，新窗口）</span></a>`;
}
function renderShell() {
  document.getElementById("registry-info").textContent =
    `${state.meta.versions.length} 个版本 ↗`;
  document
    .querySelectorAll("[data-view]")
    .forEach((button) =>
      button.setAttribute(
        "aria-pressed",
        String(button.dataset.view === state.view),
      ),
    );
  const diff = state.view === "diff";
  app.innerHTML = `
    <section class="version-panel" aria-label="${diff ? "对比版本" : "示例版本"}"><div class="version-controls">
      ${
        diff
          ? `<div class="control"><label for="from-select">起始版本</label><select id="from-select">${versionOptions(state.from)}</select></div>
        <button id="swap-versions" class="icon-button" title="交换起始与目标版本" aria-label="交换起始与目标版本">⇄</button>
        <div class="control"><label for="to-select">目标版本</label><select id="to-select">${versionOptions(state.to)}</select></div>`
          : `<div class="control"><label for="example-version">版本</label><select id="example-version">${versionOptions(state.version)}</select></div>`
      }
      </div>${sourceLink(diff ? state.to : state.version)}</section>
    <div id="content" aria-busy="true"><div class="state-card"><span class="loader" aria-hidden="true"></span>正在载入${diff ? "版本差异" : "配置示例"}…</div></div>`;
  if (diff) {
    document.getElementById("from-select").onchange = (event) =>
      changeVersion("from", event.target.value);
    document.getElementById("to-select").onchange = (event) =>
      changeVersion("to", event.target.value);
    document.getElementById("swap-versions").onclick = () => {
      [state.from, state.to] = [state.to, state.from];
      state.version = state.to;
      writeLocation();
      refresh();
    };
  } else
    document.getElementById("example-version").onchange = (event) =>
      changeVersion("version", event.target.value);
}
function changeVersion(key, version) {
  state[key] = version;
  if (key === "to") state.version = version;
  writeLocation();
  refresh();
}
function groupChanges(diff, before, after) {
  const beforeIndex = new Map(
    before.fields.map((field) => [field.path, field]),
  );
  const afterIndex = new Map(after.fields.map((field) => [field.path, field]));
  const groups = new Map();
  for (const change of diff.changes) {
    if (!groups.has(change.path))
      groups.set(change.path, {
        path: change.path,
        changes: [],
        beforeField: beforeIndex.get(change.path) ?? null,
        afterField: afterIndex.get(change.path) ?? null,
      });
    groups.get(change.path).changes.push(change);
  }
  return [...groups.values()]
    .map((group) => ({
      ...group,
      category: highestChangeCategory(group.changes),
    }))
    .sort((a, b) => a.path.localeCompare(b.path));
}
function renderGroup(group, open) {
  const kind = !group.beforeField
    ? "新增"
    : !group.afterField
      ? "移除"
      : "变更";
  const rows = changedFieldRows(group.changes).filter(
    ({ rowKey }) =>
      kind === "变更" ||
      renderFieldValue(group.beforeField, rowKey) !== "-" ||
      renderFieldValue(group.afterField, rowKey) !== "-",
  );
  return `<details class="field-card"${open ? " open" : ""}>
    <summary><span class="change-kind kind-${kind}">${kind}</span><code>${escapeHtml(group.path)}</code><span class="disclosure" aria-hidden="true">⌄</span></summary>
    <div class="matrix-wrap"><table class="field-matrix"><caption class="sr-only">${escapeHtml(group.path)} 的版本差异</caption>
      <thead><tr><th scope="col">属性</th><th scope="col">${escapeHtml(state.from)} <span>之前</span></th><th scope="col">${escapeHtml(state.to)} <span>之后</span></th></tr></thead>
      <tbody>${rows
        .map(
          ({ label, rowKey }) =>
            `<tr><th scope="row">${escapeHtml(label)}</th><td data-label="${escapeHtml(state.from)} · 之前">${escapeHtml(renderFieldValue(group.beforeField, rowKey))}</td><td data-label="${escapeHtml(state.to)} · 之后">${escapeHtml(renderFieldValue(group.afterField, rowKey))}</td></tr>`,
        )
        .join("")}</tbody>
    </table></div></details>`;
}
function renderDiff(diff) {
  const groups = state.groups;
  const added = groups.filter((group) => !group.beforeField).length;
  const removed = groups.filter((group) => !group.afterField).length;
  document.getElementById("content").innerHTML = `
    <div class="diff-overview"><div class="overview-total"><strong>${groups.length}</strong><span>个字段变化</span></div>
      <div class="overview-breakdown"><span><i class="dot added"></i>${added} 新增</span><span><i class="dot removed"></i>${removed} 移除</span><span><i class="dot changed"></i>${groups.length - added - removed} 变更</span></div>
      <button class="button secondary" id="copy-diff"${groups.length ? "" : " disabled"}>复制差异</button></div>
    <section class="filter-panel" aria-label="筛选差异"${groups.length ? "" : " hidden"}><div class="search-control"><label class="sr-only" for="field-search">搜索配置字段或说明</label><input type="search" id="field-search" placeholder="搜索字段名称或说明…" value="${escapeHtml(state.query)}" autocomplete="off" /></div>
      <div class="category-list" aria-label="变更分类">${[
        { id: "all", title: "全部" },
        ...CATEGORIES,
      ]
        .map((category) => {
          const count =
            category.id === "all"
              ? groups.length
              : groups.filter((group) => group.category === category.id).length;
          return `<button class="category-button category-${category.id}" data-category="${category.id}" aria-pressed="${category.id === state.category}">${category.title}<span>${count}</span></button>`;
        })
        .join("")}</div></section>
    <div class="results-toolbar"${groups.length ? "" : " hidden"}><p id="result-count" class="muted" role="status"></p><div><button id="expand-all" class="text-button">展开全部</button><button id="collapse-all" class="text-button">收起全部</button></div></div><div id="diff-results"></div>`;
  document.getElementById("field-search").oninput = (event) => {
    state.query = event.target.value;
    writeLocation();
    filterDiff();
  };
  document.querySelectorAll("[data-category]").forEach((button) => {
    button.onclick = () => {
      state.category = button.dataset.category;
      writeLocation();
      filterDiff();
    };
  });
  document.getElementById("copy-diff").onclick = () =>
    copyText(formatDeveloperDiff(groups), "已复制完整版本差异。");
  for (const [id, open] of [
    ["expand-all", true],
    ["collapse-all", false],
  ]) {
    document.getElementById(id).onclick = () =>
      document.querySelectorAll(".field-card").forEach((item) => {
        item.open = open;
      });
  }
  filterDiff();
  status.textContent = `已载入 ${state.from} 到 ${state.to} 的对比，${groups.length} 个字段、${diff.changes.length} 处变化。`;
}
function filterDiff() {
  const query = state.query.trim().toLowerCase();
  const filtered = state.groups.filter(
    (group) =>
      (state.category === "all" || group.category === state.category) &&
      [
        group.path,
        group.beforeField?.description,
        group.afterField?.description,
      ].some((value) => value?.toLowerCase().includes(query)),
  );
  document
    .querySelectorAll("[data-category]")
    .forEach((button) =>
      button.setAttribute(
        "aria-pressed",
        String(button.dataset.category === state.category),
      ),
    );
  document.getElementById("result-count").textContent =
    `显示 ${filtered.length} / ${state.groups.length} 个字段`;
  for (const id of ["expand-all", "collapse-all"])
    document.getElementById(id).disabled = !filtered.length;
  document.getElementById("diff-results").innerHTML = filtered.length
    ? CATEGORIES.map((category) => {
        const groups = filtered.filter(
          (group) => group.category === category.id,
        );
        return groups.length
          ? `<section class="diff-section category-${category.id}"><div class="section-heading"><h2>${category.title}<span>${groups.length}</span></h2></div><div class="change-list">${groups.map((group) => renderGroup(group, filtered.length <= 6)).join("")}</div></section>`
          : "";
      }).join("")
    : `<div class="state-card empty-state"><p>${state.groups.length ? "没有匹配的配置项" : "无配置差异"}</p>${state.groups.length ? '<button id="clear-filters" class="button secondary">清除筛选</button>' : ""}</div>`;
  const clear = document.getElementById("clear-filters");
  if (clear)
    clear.onclick = () => {
      state.query = "";
      state.category = "all";
      document.getElementById("field-search").value = "";
      writeLocation();
      filterDiff();
      document.getElementById("field-search").focus();
    };
}
function exampleSections() {
  const sections = [];
  for (const match of state.example.matchAll(/^# --- (.+) ---$/gm))
    sections.push({ name: match[1], index: match.index });
  return sections.map((section, index) => ({
    ...section,
    end: sections[index + 1]?.index ?? state.example.length,
  }));
}
function renderExample() {
  const sections = exampleSections();
  if (
    state.section !== "all" &&
    !sections.some((section) => section.name === state.section)
  ) {
    state.section = "all";
    writeLocation();
  }
  document.getElementById("content").innerHTML = `
    <section class="code-panel" aria-label="TOML 配置预览"><div class="code-toolbar"><div class="example-filter"><label for="example-section">配置组</label><select id="example-section"><option value="all">全部 · ${sections.length} 组</option>${sections.map((section) => `<option value="${escapeHtml(section.name)}"${section.name === state.section ? " selected" : ""}>${escapeHtml(section.name)}</option>`).join("")}</select></div><div class="code-actions"><button class="button secondary" id="copy-example">复制完整配置</button><a class="button primary" id="download-example" href="data/versions/${escapeHtml(state.version)}/example.toml" download="codex-${escapeHtml(state.version)}.example.toml">下载 TOML</a></div></div>
      <pre id="example-preview" tabindex="0" aria-label="生成的 TOML 示例"><code></code></pre><div class="code-footer"><span>可选项已注释；启用前请替换占位值。</span><span id="line-count"></span></div></section>`;
  document.getElementById("copy-example").onclick = () =>
    copyText(state.example, "已复制完整 TOML 示例。");
  document.getElementById("example-section").onchange = (event) => {
    state.section = event.target.value;
    writeLocation();
    renderPreview();
  };
  renderPreview();
  status.textContent = `已生成 ${state.version} 配置示例，包含 ${sections.length} 个配置组。`;
}
function renderPreview() {
  const section = exampleSections().find((item) => item.name === state.section);
  const source = section
    ? state.example.slice(section.index, section.end)
    : state.example;
  const lines = source.trimEnd().split("\n");
  document.querySelector("#example-preview code").innerHTML = lines
    .map((line) => {
      const style = /^# --- /.test(line)
        ? "code-heading"
        : /^# [^#]+ = /.test(line)
          ? "code-assignment"
          : "code-comment";
      return `<span class="code-line ${style}">${escapeHtml(line) || " "}</span>`;
    })
    .join("\n");
  document.getElementById("example-preview").scrollTop = 0;
  document.getElementById("example-preview").scrollLeft = 0;
  document.getElementById("line-count").textContent =
    `${lines.length.toLocaleString()} 行`;
}
async function copyText(value, message) {
  try {
    await navigator.clipboard.writeText(value);
    status.textContent = message;
  } catch {
    status.textContent = "无法访问剪贴板。可手动选取文本，或下载 TOML 文件。";
  }
  status.classList.add("is-toast");
  clearTimeout(state.toastTimer);
  state.toastTimer = setTimeout(
    () => status.classList.remove("is-toast"),
    6000,
  );
}
async function refresh() {
  const request = ++state.request;
  const focused = document.activeElement?.id;
  renderShell();
  if (focused) document.getElementById(focused)?.focus({ preventScroll: true });
  status.textContent = "正在载入…";
  status.classList.remove("is-toast");
  const { from, to, version, view } = state;
  try {
    if (view === "diff") {
      const [before, after] = await Promise.all([
        cached(state.fields, from, "fields.json"),
        cached(state.fields, to, "fields.json"),
      ]);
      if (request !== state.request) return;
      const diff = buildSchemaDiff(from, to, before.fields, after.fields);
      state.groups = groupChanges(diff, before, after);
      console.log(formatDeveloperDiff(state.groups));
      renderDiff(diff);
    } else {
      const example = await cached(
        state.examples,
        version,
        "example.toml",
        false,
      );
      if (request !== state.request) return;
      state.example = example;
      renderExample();
    }
  } catch (error) {
    if (request !== state.request) return;
    console.error(error);
    document.getElementById("content").innerHTML =
      '<div class="state-card error-state" role="alert"><h2>暂时无法载入版本数据</h2><p>请检查网络连接后重试，或选择另一个版本。</p><button id="retry" class="button primary">重试</button></div>';
    document.getElementById("retry").onclick = refresh;
    status.textContent = "载入失败。";
  }
  if (request === state.request)
    document.getElementById("content").setAttribute("aria-busy", "false");
}
async function start() {
  try {
    state.meta = await fetchContent("data/versions.json");
    if (!state.meta.versions?.length) throw new Error("Empty registry");
    readLocation();
    writeLocation();
    document.querySelectorAll("[data-view]").forEach((button) => {
      button.onclick = () => {
        if (state.view === button.dataset.view) return;
        state.view = button.dataset.view;
        writeLocation(true);
        refresh();
      };
    });
    window.addEventListener("popstate", () => {
      readLocation();
      writeLocation();
      refresh();
    });
    await refresh();
  } catch (error) {
    console.error(error);
    app.innerHTML =
      '<div class="state-card error-state" role="alert"><h2>无法载入版本列表</h2><p>请检查网络连接后重试。</p><button id="retry-start" class="button primary">重新载入</button></div>';
    document.getElementById("retry-start").onclick = start;
    status.textContent = "载入失败。";
  }
}
start();
