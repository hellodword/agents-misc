import { appendFile } from "node:fs/promises";
import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { test, expect } from "./fixtures";

async function expectTranscriptRowsNotToOverlap(page: Page) {
  await expect
    .poll(async () =>
      page.locator(".entry-wrap").evaluateAll((elements) => {
        const rows = elements
          .map((element) => {
            const box = element.getBoundingClientRect();
            const contents = [
              ...element.querySelectorAll("[data-transcript-entry]"),
            ].map((content) => content.getBoundingClientRect());
            const contentTop = Math.min(
              ...contents.map((content) => content.top),
            );
            const contentBottom = Math.max(
              ...contents.map((content) => content.bottom),
            );
            return {
              index: Number(element.getAttribute("data-index")),
              top: box.top,
              bottom: box.bottom,
              height: box.height,
              contentHeight:
                contents.length > 0 ? contentBottom - contentTop : 0,
            };
          })
          .sort((left, right) => left.index - right.index);
        const overlaps: string[] = [];
        const underMeasured: string[] = [];
        for (let index = 0; index < rows.length; index++) {
          const row = rows[index];
          if (row.height + 1 < row.contentHeight)
            underMeasured.push(
              `${row.index}:${row.height}<${row.contentHeight}`,
            );
          const previous = rows[index - 1];
          if (
            previous &&
            row.index === previous.index + 1 &&
            row.top < previous.bottom - 1
          )
            overlaps.push(
              `${previous.index}-${row.index}:${previous.bottom}>${row.top}`,
            );
        }
        return { overlaps, underMeasured };
      }),
    )
    .toEqual({ overlaps: [], underMeasured: [] });
}

async function cssVariableColor(page: Page, variable: string) {
  return page.evaluate((variable) => {
    const marker = document.createElement("span");
    marker.style.color = `var(${variable})`;
    document.body.append(marker);
    const color = getComputedStyle(marker).color;
    marker.remove();
    return color;
  }, variable);
}

test("indexes an empty cache, searches content, reloads a deep link, and exposes raw chunks", async ({
  page,
  baseURL,
}) => {
  const status = await page.request.get(`${baseURL}/api/v1/status`);
  expect(status.ok()).toBeTruthy();
  const planParent = page.locator(
    'a[href="/sessions/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"]',
  );
  const planChild = page.locator(
    'a[href="/sessions/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"]',
  );
  await expect(planParent).toBeVisible();
  await expect(planChild).toBeVisible();
  await expect(planParent).toContainText("Plan session grouping");
  await expect(planChild).toContainText("Implement · Plan session grouping");
  await expect(
    planParent
      .locator("xpath=../ul")
      .locator('a[href="/sessions/bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"]'),
  ).toBeVisible();
  await page.getByRole("button", { name: "Filter" }).click();
  await page.getByLabel("Source").selectOption("exec");
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(planParent).toBeVisible();
  await expect(planChild).toBeVisible();
  await page.getByRole("button", { name: "Filter" }).click();
  await page.getByRole("button", { name: "Reset" }).click();
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page.getByText("Pagination message 109").first()).toBeVisible();
  await page.getByRole("button", { name: "Go to first message" }).click();
  await expect(
    page.getByText("Inspect synthetic fixture").first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "Go to latest message" }).click();
  await expect(page.getByText("Pagination message 109").first()).toBeVisible();
  await page.getByRole("button", { name: "Copy message" }).last().click();
  await expect(page.getByRole("button", { name: "Copied" })).toBeVisible();

  await page.getByRole("button", { name: "Search" }).click();
  const searchDialog = page.getByRole("dialog", { name: "Search" });
  const searchInput = page.getByRole("combobox", { name: "Search" });
  const [dialogBox, inputBox] = await Promise.all([
    searchDialog.boundingBox(),
    searchInput.boundingBox(),
  ]);
  expect(dialogBox).not.toBeNull();
  expect(inputBox).not.toBeNull();
  expect(inputBox!.x).toBeGreaterThanOrEqual(dialogBox!.x);
  expect(inputBox!.x + inputBox!.width).toBeLessThanOrEqual(
    dialogBox!.x + dialogBox!.width,
  );
  await expect(searchInput).toBeFocused();
  await expect(searchInput).toHaveCSS("outline-style", "none");
  const searchInputWrapper = searchDialog.locator(
    '[data-slot="command-input-wrapper"]',
  );
  const accentColor = await cssVariableColor(page, "--accent");
  await expect(searchInputWrapper).toHaveCSS(
    "border-bottom-color",
    accentColor,
  );
  expect(
    await searchInputWrapper.evaluate(
      (element) => getComputedStyle(element).boxShadow,
    ),
  ).toContain(accentColor);
  await searchInput.fill("synthetic");
  await expect(
    page.getByText(/Synthetic result|Inspect synthetic fixture/).first(),
  ).toBeVisible();
  await searchInput.fill("synthetic output");
  await expect(page.getByText("No matching entries")).toBeVisible();
  await page
    .getByRole("checkbox", { name: /Search all activity types/ })
    .check();
  await expect(page.getByText("synthetic output").first()).toBeVisible();
  expect(
    await page.evaluate(() =>
      localStorage.getItem("agents-viewer-search-all-types"),
    ),
  ).toBe("true");
  await page.keyboard.press("Escape");

  const session = (
    await (await page.request.get(`${baseURL}/api/v1/sessions?limit=10`)).json()
  ).data[0];
  const entries = (
    await (
      await page.request.get(
        `${baseURL}/api/v1/sessions/${session.id}/entries?limit=100&direction=forward`,
      )
    ).json()
  ).data;
  const command = entries.find(
    (entry: { toolKind?: string }) => entry.toolKind === "command",
  );
  expect(command).toBeTruthy();
  await page.goto(`${baseURL}/sessions/${session.id}?entry=${command.id}`);
  await page.reload();
  await expect(
    page.locator("[data-transcript-entry][aria-current=true]"),
  ).toBeVisible();
  await expect(page.locator("#entry-inspector")).toHaveCount(0);
  const commandActivity = page.getByRole("button", {
    name: /Executing: printf synthetic/,
  });
  await expect(commandActivity).toBeVisible();
  await expect(commandActivity.locator("time")).toHaveCount(0);
  await expect(commandActivity.locator(".activity-label")).toHaveCSS(
    "color",
    await cssVariableColor(page, "--fg"),
  );
  await expect(
    page.locator("#main-content").getByText("synthetic output"),
  ).toHaveCount(0);
  await commandActivity.click();
  await expect(page.locator("#entry-inspector")).toBeVisible();
  await expect(page.getByText("Result").first()).toBeVisible();
  await expect(
    page.locator("#entry-inspector").getByText("synthetic output"),
  ).toBeVisible();
  await expect(page.getByText("Raw records").first()).toBeVisible();
  const raw = page.locator(".raw-item").first();
  await raw.click();
  await expect(page.locator(".raw-content").first()).toContainText("timestamp");
  await page
    .locator("#entry-inspector")
    .getByRole("button", { name: "Close inspector" })
    .click();
  await expect(page.locator("#entry-inspector")).toHaveCount(0);
});

test("renders multiple request_user_input questions as responsive poll messages", async ({
  page,
  rollout,
}) => {
  const questions = [
    {
      id: "target",
      header: "Target",
      question: "Where should this run?",
      isOther: true,
      isSecret: false,
      options: [
        {
          label: "Staging",
          description: "Use the synthetic staging environment.",
        },
        {
          label: "Production",
          description: "Use the synthetic production environment.",
        },
      ],
    },
    {
      id: "rollout",
      header: "Rollout",
      question: "How should rollout proceed?",
      isOther: true,
      isSecret: false,
      options: [
        { label: "Safe", description: "Use a slower synthetic rollout." },
        { label: "Fast", description: "Use a faster synthetic rollout." },
      ],
    },
    {
      id: "fallback",
      header: "Fallback",
      question: "What should happen after a synthetic failure?",
      isOther: true,
      isSecret: false,
      options: [
        { label: "Retry", description: "Retry the synthetic operation." },
        { label: "Stop", description: "Stop the synthetic operation." },
      ],
    },
  ];
  const records = [
    {
      timestamp: "2025-01-02T03:10:00.000Z",
      type: "response_item",
      payload: {
        type: "function_call",
        call_id: "call-browser-poll",
        name: "request_user_input",
        arguments: JSON.stringify({ questions }),
      },
    },
    {
      timestamp: "2025-01-02T03:10:00.100Z",
      type: "event_msg",
      payload: {
        type: "request_user_input",
        call_id: "call-browser-poll",
        turn_id: "turn-browser-poll",
        questions,
      },
    },
    {
      timestamp: "2025-01-02T03:10:01.000Z",
      type: "response_item",
      payload: {
        type: "function_call_output",
        call_id: "call-browser-poll",
        output: JSON.stringify({
          answers: {
            target: {
              answers: ["Production", "user_note: Use the synthetic canary."],
            },
          },
        }),
      },
    },
  ];
  await appendFile(
    rollout,
    `\n${records.map((record) => JSON.stringify(record)).join("\n")}\n`,
  );

  const polls = page.locator(".request-user-input-message");
  await expect(polls).toHaveCount(3);
  await expect(
    page.getByRole("article", { name: "Where should this run?" }),
  ).toBeVisible();
  await expect(polls.first()).toHaveCSS("justify-content", "flex-start");
  await expect(
    polls.first().getByText("Use the synthetic production environment."),
  ).toBeVisible();
  const production = polls.first().locator("li", { hasText: "Production" });
  await expect(production).toHaveClass(/is-selected/);
  await expect(production.locator(".request-user-input-radio svg")).toHaveCount(
    1,
  );
  await expect(
    production.locator(".request-user-input-option-note"),
  ).toHaveText("Use the synthetic canary.");
  expect(
    await polls.evaluateAll((elements) =>
      elements.every((element, index) => {
        const box = element.getBoundingClientRect();
        const previous = elements[index - 1]?.getBoundingClientRect();
        return !previous || box.top >= previous.bottom;
      }),
    ),
  ).toBe(true);
  await expectTranscriptRowsNotToOverlap(page);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect
    .poll(() =>
      polls.evaluateAll((elements) =>
        elements.every((element) => {
          const box = element
            .querySelector(".request-user-input-poll")
            ?.getBoundingClientRect();
          return Boolean(
            box && box.left >= 0 && box.right <= window.innerWidth,
          );
        }),
      ),
    )
    .toBe(true);
  await expectTranscriptRowsNotToOverlap(page);
  const results = await new AxeBuilder({ page })
    .include(".request-user-input-message-group")
    .analyze();
  expect(
    results.violations.filter(
      (item) => item.impact === "critical" || item.impact === "serious",
    ),
  ).toEqual([]);

  await polls
    .first()
    .getByRole("button", { name: "Open inspector: Where should this run?" })
    .click();
  await expect(page.locator("#entry-inspector")).toBeVisible();
});

test("supports locale, theme, keyboard focus, responsive sheets, and accessibility", async ({
  page,
}) => {
  await expect(
    page.locator(".session-item .session-preview").first(),
  ).toBeVisible();
  await page.getByLabel("Language").selectOption("zh-CN");
  await expect(page.getByText("Agents Viewer")).toBeVisible();
  await page.getByRole("button", { name: "筛选" }).click();
  await expect(page.getByRole("option", { name: "代码审查" })).toBeAttached();
  await page.getByText("这些来源分别是什么意思？").click();
  await expect(
    page.getByText("由 codex exec 启动的非交互任务。"),
  ).toBeVisible();
  await page.getByLabel("归档").selectOption("only");
  await page.getByRole("button", { name: "应用" }).click();
  await page.getByRole("button", { name: "主题" }).click();
  await page.getByRole("menuitemradio", { name: "深色" }).click();
  await expect(page.locator("html")).toHaveClass(/dark/);

  await page.getByRole("button", { name: "搜索" }).focus();
  await page.keyboard.press("Control+k");
  await expect(page.getByRole("dialog", { name: "搜索" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: "搜索" })).toBeFocused();

  await page.setViewportSize({ width: 900, height: 800 });
  await expect(
    page.getByRole("banner").getByRole("button", { name: "打开检查器" }),
  ).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "打开会话列表" }).click();
  await expect(page.getByRole("dialog", { name: "会话" })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "搜索" }).click();
  const mobileDialog = page.getByRole("dialog", { name: "搜索" });
  const mobileInput = page.getByRole("combobox", { name: "搜索" });
  const [mobileDialogBox, mobileInputBox] = await Promise.all([
    mobileDialog.boundingBox(),
    mobileInput.boundingBox(),
  ]);
  expect(mobileInputBox!.x).toBeGreaterThanOrEqual(mobileDialogBox!.x);
  expect(mobileInputBox!.x + mobileInputBox!.width).toBeLessThanOrEqual(
    mobileDialogBox!.x + mobileDialogBox!.width,
  );
  await page.keyboard.press("Escape");

  const results = await new AxeBuilder({ page }).analyze();
  expect(
    results.violations.filter(
      (item) => item.impact === "critical" || item.impact === "serious",
    ),
  ).toEqual([]);
});

test("delivers appended entries over SSE and serves security headers", async ({
  page,
  rollout,
  baseURL,
}) => {
  const response = await page.request.get(`${baseURL}/api/v1/status`);
  expect(response.headers()["content-security-policy"]).toContain(
    "default-src 'self'",
  );
  expect(response.headers()["x-content-type-options"]).toBe("nosniff");
  await expect(page.getByText("Pagination message 109").first()).toBeVisible();
  const apiRequests: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname.startsWith("/api/v1/"))
      apiRequests.push(`${request.method()} ${url.pathname}`);
  });
  await page.evaluate(() => {
    type ProbeWindow = typeof window & {
      __indexProgressProbe?: { flashed: boolean; observer: MutationObserver };
    };
    const target = window as ProbeWindow;
    const probe = {
      flashed: Boolean(document.querySelector(".index-progress")),
      observer: null as unknown as MutationObserver,
    };
    probe.observer = new MutationObserver((records) => {
      for (const record of records) {
        for (const node of record.addedNodes) {
          if (
            node instanceof Element &&
            (node.matches(".index-progress") ||
              node.querySelector(".index-progress"))
          ) {
            probe.flashed = true;
          }
        }
      }
    });
    probe.observer.observe(document.body, { childList: true, subtree: true });
    target.__indexProgressProbe = probe;
  });
  await appendFile(
    rollout,
    '{"timestamp":"2025-01-02T03:05:00.000Z","type":"event_msg","payload":{"type":"agent_message","message":"Live appended 中文 code_needle","phase":"final"}}\n',
  );
  await expect(
    page.getByText("Live appended 中文 code_needle").first(),
  ).toBeVisible({ timeout: 8_000 });
  expect(
    await page.evaluate(() => {
      type ProbeWindow = typeof window & {
        __indexProgressProbe?: { flashed: boolean; observer: MutationObserver };
      };
      const probe = (window as ProbeWindow).__indexProgressProbe;
      probe?.observer.disconnect();
      return probe?.flashed;
    }),
  ).toBe(false);
  expect(
    apiRequests.filter((request) => request === "GET /api/v1/sessions").length,
  ).toBeLessThanOrEqual(1);
  expect(
    apiRequests.filter((request) =>
      /GET \/api\/v1\/sessions\/[^/]+$/.test(request),
    ).length,
  ).toBeLessThanOrEqual(1);
  expect(
    apiRequests.filter((request) =>
      /GET \/api\/v1\/sessions\/[^/]+\/entries$/.test(request),
    ).length,
  ).toBeLessThanOrEqual(1);
  apiRequests.length = 0;
  await page.getByRole("button", { name: "Go to first message" }).click();
  await expect(
    page.getByText("Inspect synthetic fixture").first(),
  ).toBeVisible();
  await appendFile(
    rollout,
    '{"timestamp":"2025-01-02T03:06:00.000Z","type":"event_msg","payload":{"type":"agent_message","message":"Second live message while reading","phase":"final"}}\n',
  );
  await expect(
    page.getByRole("button", { name: /Go to 1 new item/ }),
  ).toBeVisible({ timeout: 8_000 });
  await expect(page.getByText("Second live message while reading")).toHaveCount(
    0,
  );
  await page.getByRole("button", { name: /Go to 1 new item/ }).click();
  await expect(
    page.getByText("Second live message while reading").first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "Search" }).click();
  await page.getByRole("combobox", { name: "Search" }).fill("code_needle");
  await expect(
    page.getByText(/Live appended 中文 code_needle/).first(),
  ).toBeVisible();
});

test("measures mixed-height transcript rows after updates, jumps, and responsive wrapping", async ({
  page,
  rollout,
}) => {
  await page.getByRole("button", { name: "Filter" }).click();
  await page.getByRole("checkbox", { name: /Show technical activity/ }).check();
  await page.getByRole("button", { name: "Apply" }).click();

  const longMessage = Array.from(
    { length: 36 },
    (_, index) => `Long line ${index}: ${"content ".repeat(12)}`,
  ).join("\n");
  const records = [
    {
      timestamp: "2025-01-02T03:08:00.000Z",
      type: "event_msg",
      payload: { type: "user_message", message: longMessage },
    },
    {
      timestamp: "2025-01-02T03:08:00.100Z",
      type: "response_item",
      payload: {
        type: "reasoning",
        id: "geometry-reasoning",
        summary: [
          {
            type: "summary_text",
            text: "Check every measured row before positioning the next row",
          },
        ],
      },
    },
    {
      timestamp: "2025-01-02T03:08:00.200Z",
      type: "event_msg",
      payload: {
        type: "exec_command_begin",
        call_id: "geometry-command",
        command: "printf first\nprintf second\nprintf third",
      },
    },
    {
      timestamp: "2025-01-02T03:08:00.300Z",
      type: "event_msg",
      payload: {
        type: "exec_command_end",
        call_id: "geometry-command",
        status: "completed",
        stdout: "first\nsecond\nthird",
      },
    },
    {
      timestamp: "2025-01-02T03:08:01.000Z",
      type: "event_msg",
      payload: {
        type: "agent_message",
        message: "Geometry sentinel",
        phase: "final",
      },
    },
  ];
  await appendFile(
    rollout,
    `${records.map((record) => JSON.stringify(record)).join("\n")}\n`,
  );
  await expect(page.getByText("Geometry sentinel")).toBeVisible({
    timeout: 8_000,
  });
  const multiLineActivity = page.getByRole("button", {
    name: /Executing: printf first/,
  });
  await expect(multiLineActivity).toBeVisible();
  const activityBody = multiLineActivity.locator(".activity-body");
  await expect(activityBody).toHaveText("printf first…");
  await expect(activityBody).toHaveCSS("white-space", "nowrap");
  await expect(activityBody).toHaveCSS("text-overflow", "ellipsis");
  await expect(activityBody).toHaveCSS("overflow", "hidden");
  const [activityLabelBox, assistantBubbleBox] = await Promise.all([
    multiLineActivity.locator(".activity-label").boundingBox(),
    page
      .getByText("Geometry sentinel")
      .locator("xpath=ancestor::article")
      .locator(".message-bubble")
      .boundingBox(),
  ]);
  expect(activityLabelBox).not.toBeNull();
  expect(assistantBubbleBox).not.toBeNull();
  expect(activityLabelBox!.x).toBeGreaterThan(assistantBubbleBox!.x);
  expect(activityLabelBox!.x - assistantBubbleBox!.x).toBeLessThan(32);
  await multiLineActivity.hover();
  await expect(
    page.getByRole("tooltip", { name: /printf first/ }),
  ).toContainText("printf second");
  await expectTranscriptRowsNotToOverlap(page);

  await page.setViewportSize({ width: 900, height: 800 });
  await expectTranscriptRowsNotToOverlap(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await expectTranscriptRowsNotToOverlap(page);
  await page.setViewportSize({ width: 1440, height: 900 });

  await page.getByRole("button", { name: "Go to first message" }).click();
  await expect(
    page.getByText("Inspect synthetic fixture").first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "Go to latest message" }).click();
  await expect(page.getByText("Geometry sentinel")).toBeVisible();
  await expectTranscriptRowsNotToOverlap(page);
});
