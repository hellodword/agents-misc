import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AppShell as App } from "@/viewer/app-shell";
import { executedContent } from "@/viewer/entry-format";
import { SafeMarkdown } from "@/viewer/markdown";
import {
  conversationDisplayType,
  isDefaultVisible,
} from "@/viewer/preferences";
import {
  shouldApplyScrollTarget,
  VirtualTranscript,
} from "@/viewer/virtual-transcript";
import { resources } from "@/lib/i18n";
import type {
  EntryListItem,
  SessionGroup,
  SessionSummary,
} from "@/generated/api";

const session: SessionSummary = {
  id: "s1",
  source: "cli",
  sourceLocation: {
    rootKind: "active",
    relativePath: "2026/07/01/s1.jsonl",
  },
  firstUserMessage: {
    text: "Hello **world**",
    preview: "Hello **world**",
    timestamp: "2026-07-01T00:10:00Z",
  },
  contentStatus: {
    freshness: "current",
    liveState: "inactive",
    hasSnapshot: true,
    snapshotRevision: 1,
    syncedThroughBytes: 100,
    observedBytes: 100,
  },
  title: "Hello session",
  preview: "Preview",
  createdAt: "2026-07-01T00:00:00.000000Z",
  updatedAt: "2026-07-01T01:00:00.000000Z",
  archived: false,
  entryCount: 2,
  diagnosticCount: 0,
  indexState: "ready",
  completeness: "complete",
  freshness: "current",
};
const sessionGroups: SessionGroup[] = [
  {
    root: { session, children: [] },
    latestSessionId: "s1",
    updatedAt: session.updatedAt,
    hierarchyComplete: true,
  },
];
const entry: EntryListItem = {
  id: "e1",
  sessionId: "s1",
  sequence: 1,
  timestamp: "2026-07-01T00:10:00Z",
  kind: "message",
  presentation: "user",
  role: "user",
  title: "User",
  titleComplete: true,
  primaryPreview: "Hello **world**",
  secondaryPreview: "",
  primaryBytes: 15,
  secondaryBytes: 0,
  primaryComplete: true,
  secondaryComplete: true,
  defaultCollapsed: false,
  metadata: {},
  metadataComplete: true,
  rawRefCount: 1,
};
const warningEntry: EntryListItem = {
  ...entry,
  id: "warning-entry",
  sequence: 2,
  kind: "warning",
  presentation: "technical",
  role: undefined,
  title: "Warning",
  primaryPreview: "Linked warning detail",
};
const liveEntry: EntryListItem = {
  ...entry,
  id: "e2",
  sequence: 2,
  presentation: "response",
  role: "assistant",
  title: "Assistant",
  primaryPreview: "First live tail entry",
};
const laterLiveEntry: EntryListItem = {
  ...liveEntry,
  id: "e3",
  sequence: 3,
  primaryPreview: "Second live tail entry",
};
type EventSourceHarness = {
  instances: Array<{
    emit: (name: string, data: unknown) => void;
    onerror: ((event: Event) => void) | null;
  }>;
};
const eventSources = () => EventSource as unknown as EventSourceHarness;
const callsFor = (fragment: string) =>
  vi
    .mocked(fetch)
    .mock.calls.filter(([input]) => String(input).includes(fragment)).length;

beforeEach(() => {
  localStorage.clear();
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
  eventSources().instances.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string | URL | Request) => {
      const url = String(input);
      let body: unknown;
      if (url.includes("/sessions/s1/entries/e1/content")) {
        const secondary = url.includes("field=secondary");
        body = {
          field: secondary ? "secondary" : "primary",
          text: secondary ? "" : "Hello **world**",
          byteOffset: 0,
          totalBytes: secondary ? 0 : 15,
          complete: true,
        };
      } else if (url.includes("/sessions/s1/entries/warning-entry"))
        body = {
          item: warningEntry,
          derivedMetadata: {},
          rawRefs: [],
        };
      else if (url.includes("/sessions/s1/entries/e1"))
        body = {
          item: entry,
          derivedMetadata: {},
          rawRefs: [
            {
              id: "r1",
              line: 1,
              byteOffset: 0,
              byteLength: 10,
              envelopeType: "event_msg",
            },
          ],
        };
      else if (url.includes("/sessions/s1/raw/r1"))
        body = {
          summary: {
            id: "r1",
            sessionId: "s1",
            line: 1,
            byteOffset: 0,
            byteLength: 10,
            envelopeType: "event_msg",
            parseStatus: "valid",
            encoding: "utf8",
            oversize: false,
          },
          chunk: {
            field: "primary",
            text: '{"safe":true}',
            byteOffset: 0,
            totalBytes: 13,
            complete: true,
          },
        };
      else if (url.includes("/sessions/s1/entries"))
        body = {
          data: url.includes("aroundEntryId=warning-entry")
            ? [warningEntry]
            : [entry],
          partial: false,
        };
      else if (url.endsWith("/sessions/s1"))
        body = { summary: session, diagnostics: [] };
      else if (url.includes("/session-groups"))
        body = { data: sessionGroups, partial: false };
      else if (url.includes("/sessions"))
        body = { data: [session], partial: false };
      else if (url.includes("/search"))
        body = {
          data: [
            {
              session,
              entryId: "e1",
              kind: "message",
              snippet: "Hello world",
              matchRanges: [{ start: 0, end: 5 }],
              field: "primary",
              rank: 1,
            },
          ],
          partial: false,
        };
      else
        body = {
          appVersion: "0.1.0",
          generation: 1,
          phase: "ready",
          progress: {
            totalFiles: 1,
            processedFiles: 1,
            totalBytes: 1,
            processedBytes: 1,
            failedFiles: 0,
            excludedFiles: 0,
            excludedBytes: 0,
          },
          ftsReady: true,
          databaseBytes: 1,
        };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }),
  );
});

describe("Agents Viewer UI", () => {
  it("keeps English and Chinese locale keys identical", () => {
    expect(Object.keys(resources.en.translation).sort()).toEqual(
      Object.keys(resources["zh-CN"].translation).sort(),
    );
  });
  it("classifies every normalized conversation display type", () => {
    const cases: Array<
      [ReturnType<typeof conversationDisplayType>, Partial<EntryListItem>]
    > = [
      ["received", { kind: "message", presentation: "response" }],
      ["sent", { kind: "message", presentation: "user" }],
      [
        "requestUserInput",
        {
          kind: "tool",
          presentation: "technical",
          toolKind: "requestUserInput",
        },
      ],
      ["reasoning", { kind: "reasoning", presentation: "technical" }],
      [
        "exec",
        { kind: "tool", presentation: "technical", toolKind: "command" },
      ],
      ["plan", { kind: "plan", presentation: "technical" }],
      ["patch", { kind: "tool", presentation: "technical", toolKind: "patch" }],
      ["mcp", { kind: "tool", presentation: "technical", toolKind: "mcp" }],
      [
        "webSearch",
        { kind: "tool", presentation: "technical", toolKind: "webSearch" },
      ],
      [
        "function",
        { kind: "tool", presentation: "technical", toolKind: "function" },
      ],
      [
        "dynamic",
        { kind: "tool", presentation: "technical", toolKind: "dynamic" },
      ],
      [
        "terminal",
        { kind: "tool", presentation: "technical", toolKind: "terminal" },
      ],
      [
        "viewImage",
        { kind: "tool", presentation: "technical", toolKind: "viewImage" },
      ],
      [
        "otherTool",
        { kind: "tool", presentation: "technical", toolKind: "other" },
      ],
      ["warning", { kind: "warning", presentation: "technical" }],
      ["error", { kind: "error", presentation: "technical" }],
      ["context", { kind: "context", presentation: "technical" }],
      ["marker", { kind: "marker", presentation: "technical" }],
      ["technicalMessage", { kind: "message", presentation: "technical" }],
      ["internalMessage", { kind: "message", presentation: "internal" }],
      ["unknown", { kind: "unknown", presentation: "technical" }],
    ];
    for (const [expected, override] of cases)
      expect(conversationDisplayType({ ...entry, ...override })).toBe(expected);
  });
  it("characterizes major transcript entries before module extraction", () => {
    const entries: EntryListItem[] = [
      { ...entry, timestamp: undefined },
      {
        ...entry,
        id: "assistant",
        sequence: 2,
        timestamp: undefined,
        presentation: "response",
        role: "assistant",
        title: "Assistant",
        primaryPreview: "A complete answer",
      },
      {
        ...entry,
        id: "request",
        sequence: 3,
        timestamp: undefined,
        kind: "tool",
        presentation: "technical",
        role: undefined,
        toolKind: "requestUserInput",
        toolStatus: "running",
        title: "request_user_input",
        primaryPreview: "",
        metadata: {
          requestUserInputQuestions: [
            {
              id: "choice",
              question: "Choose a target",
              isOther: true,
              isSecret: false,
              options: [
                { label: "Safe", description: "Use the safe target." },
                { label: "Fast", description: "Use the fast target." },
              ],
            },
          ],
        },
      },
      {
        ...entry,
        id: "reasoning",
        sequence: 4,
        timestamp: undefined,
        kind: "reasoning",
        presentation: "technical",
        role: undefined,
        title: "Reasoning",
        primaryPreview: "Compare the boundaries",
      },
      {
        ...entry,
        id: "command",
        sequence: 5,
        timestamp: undefined,
        kind: "tool",
        presentation: "technical",
        role: undefined,
        toolKind: "command",
        toolStatus: "succeeded",
        title: "exec_command",
        primaryPreview: '{"cmd":"just agents-viewer-test"}',
      },
      {
        ...entry,
        id: "plan",
        sequence: 6,
        timestamp: undefined,
        kind: "plan",
        presentation: "technical",
        role: undefined,
        title: "Plan",
        primaryPreview: "# Plan\n\nShip safely",
      },
      {
        ...entry,
        id: "warning",
        sequence: 7,
        timestamp: undefined,
        kind: "warning",
        presentation: "technical",
        role: undefined,
        title: "Warning",
        primaryPreview: "Synthetic warning",
      },
      {
        ...entry,
        id: "context",
        sequence: 8,
        timestamp: undefined,
        kind: "context",
        presentation: "internal",
        role: undefined,
        title: "Context",
        primaryPreview: "Synthetic context",
      },
    ];
    const { container } = render(
      <VirtualTranscript entries={entries} onInspect={() => {}} />,
    );
    const signature = Array.from(
      container.querySelectorAll<HTMLElement>("[data-transcript-entry]"),
    ).map((element) => ({
      className: element.className,
      text: element.textContent?.replace(/\s+/g, " ").trim(),
    }));
    expect(signature).toEqual([
      {
        className: "message-row message-user",
        text: "User: Hello world",
      },
      {
        className: "message-row message-assistant",
        text: "Assistant: A complete answer",
      },
      {
        className: "message-row message-assistant request-user-input-message",
        text: "Choose a targetSafe — Use the safe target.Fast — Use the fast target.",
      },
      {
        className: "notice-row",
        text: "Reasoning:Compare the boundaries",
      },
      {
        className: "notice-row",
        text: "Executing:just agents-viewer-test",
      },
      {
        className: "message-row message-assistant",
        text: "Assistant: Plan Ship safely",
      },
      { className: "notice-row", text: "Warning:Synthetic warning" },
      { className: "notice-row", text: "Context:Synthetic context" },
    ]);
  });
  it("renders session, deep link, inspector raw chunk, search, and SSE-safe states", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/sessions/s1?entry=e1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByRole("heading", { name: "Hello session" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("world")).toBeInTheDocument();
    expect(
      screen.queryByRole("complementary", { name: "Inspector" }),
    ).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("banner")).queryByRole("button", {
        name: "Open inspector",
      }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Settings" }));
    expect(
      screen.getByRole("option", { name: "Code review" }),
    ).toBeInTheDocument();
    const settings = screen.getByRole("dialog", { name: "Settings" });
    expect(within(settings).getAllByRole("checkbox")).toHaveLength(22);
    for (const name of [
      "Received replies",
      "Sent messages",
      "request_user_input",
      "Plans",
    ]) {
      const required = within(settings).getByRole("checkbox", { name });
      expect(required).toBeChecked();
      expect(required).toBeDisabled();
    }
    expect(
      within(settings).getByRole("checkbox", { name: "Reasoning" }),
    ).toBeChecked();
    expect(
      within(settings).getByRole("checkbox", { name: "Exec commands" }),
    ).toBeChecked();
    expect(
      within(settings).getByRole("checkbox", { name: "Warnings" }),
    ).not.toBeChecked();
    await user.click(
      within(settings).getByRole("checkbox", { name: "Reasoning" }),
    );
    await user.click(
      within(settings).getByRole("checkbox", { name: "Warnings" }),
    );
    await user.click(
      screen.getByRole("checkbox", { name: /Use Ctrl\+Shift\+F to search/ }),
    );
    await user.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(
        vi
          .mocked(fetch)
          .mock.calls.some(([input]) =>
            String(input).includes(
              "displayTypes=received%2Csent%2CrequestUserInput%2Cexec%2Cplan%2Cwarning",
            ),
          ),
      ).toBe(true),
    );
    expect(
      JSON.parse(
        localStorage.getItem("agents-viewer-conversation-display-types") ??
          "null",
      ),
    ).toEqual([
      "received",
      "sent",
      "requestUserInput",
      "exec",
      "plan",
      "warning",
    ]);
    expect(localStorage.getItem("agents-viewer-search-ctrl-shift-f")).toBe(
      "true",
    );
    const inspectorButtons = screen.getAllByRole("button", {
      name: "Open inspector",
    });
    await user.click(inspectorButtons.at(-1)!);
    expect(
      await screen.findByRole("complementary", { name: "Inspector" }),
    ).toBeInTheDocument();
    const rawRecords = await screen.findAllByText("#1 event_msg");
    await user.click(rawRecords[0]);
    expect((await screen.findAllByText(/safe/)).length).toBeGreaterThan(0);
    fireEvent.keyDown(window, { key: "F", ctrlKey: true, shiftKey: true });
    expect(
      await screen.findByRole("dialog", { name: "Search" }),
    ).toBeInTheDocument();
    await user.type(screen.getByRole("combobox", { name: "Search" }), "Hello");
    expect(await screen.findByText("Hello world")).toBeInTheDocument();
    await user.click(
      screen.getByRole("checkbox", { name: /Search all activity types/ }),
    );
    await waitFor(() =>
      expect(
        vi
          .mocked(fetch)
          .mock.calls.some(([input]) =>
            String(input).includes("allTypes=true"),
          ),
      ).toBe(true),
    );
    expect(localStorage.getItem("agents-viewer-search-all-types")).toBe("true");
  });
  it("uses the new display defaults instead of the legacy technical preference", async () => {
    localStorage.setItem("agents-viewer-show-technical", "true");
    localStorage.setItem(
      "agents-viewer-conversation-display-types",
      "not-json",
    );
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    await screen.findByRole("heading", { name: "Hello session" });
    await user.click(screen.getByRole("button", { name: "Settings" }));
    expect(screen.getByRole("checkbox", { name: "Reasoning" })).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Exec commands" }),
    ).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Patch activity" }),
    ).not.toBeChecked();
    const plan = screen.getByRole("checkbox", { name: "Plans" });
    expect(plan).toBeChecked();
    expect(plan).toBeDisabled();
    expect(
      screen.getByRole("checkbox", { name: "Warnings" }),
    ).not.toBeChecked();
  });
  it("adds required plans to existing display preferences and every entries request", async () => {
    localStorage.setItem(
      "agents-viewer-conversation-display-types",
      JSON.stringify(["received", "sent", "requestUserInput", "exec"]),
    );
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByRole("heading", { name: "Hello session" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        vi
          .mocked(fetch)
          .mock.calls.some(([input]) =>
            String(input).includes(
              "displayTypes=received%2Csent%2CrequestUserInput%2Cexec%2Cplan",
            ),
          ),
      ).toBe(true),
    );
    await user.click(screen.getByRole("button", { name: /^Settings/ }));
    const plan = screen.getByRole("checkbox", { name: "Plans" });
    expect(plan).toBeChecked();
    expect(plan).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(
      JSON.parse(
        localStorage.getItem("agents-viewer-conversation-display-types") ??
          "null",
      ),
    ).toEqual(["received", "sent", "requestUserInput", "exec", "plan"]);
  });
  it("temporarily includes a linked entry type without persisting it", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/sessions/s1?entry=warning-entry"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByText("Linked warning detail"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        vi
          .mocked(fetch)
          .mock.calls.some(([input]) =>
            String(input).includes(
              "displayTypes=received%2Csent%2CrequestUserInput%2Creasoning%2Cexec%2Cplan%2Cwarning",
            ),
          ),
      ).toBe(true),
    );
    await user.click(screen.getByRole("button", { name: "Settings" }));
    expect(
      screen.getByRole("checkbox", { name: "Warnings" }),
    ).not.toBeChecked();
    expect(
      screen.getByText(
        "Warnings is temporarily shown to reveal the linked entry.",
      ),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Apply" }));
    expect(
      JSON.parse(
        localStorage.getItem("agents-viewer-conversation-display-types") ??
          "null",
      ),
    ).toEqual([
      "received",
      "sent",
      "requestUserInput",
      "reasoning",
      "exec",
      "plan",
    ]);
    expect(screen.getByText("Linked warning detail")).toBeInTheDocument();
  });
  it("sanitizes raw HTML, scripts, and remote images while rendering GFM", () => {
    const { container } = render(
      <SafeMarkdown
        text={
          "# Heading\n\n<script>alert(1)</script>\n\n![secret](https://evil.test/x)\n\n| A | B |\n| - | - |\n| 1 | `code` |\n\n[safe](https://example.com)"
        }
      />,
    );
    expect(document.querySelector("script")).toBeNull();
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText(/Attachment/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "safe" })).toHaveAttribute(
      "rel",
      "noreferrer noopener",
    );
    expect(container.querySelector("table")).toBeInTheDocument();
    expect(container.querySelector("code")).toHaveTextContent("code");
  });
  it("copies inline and fenced code while applying language highlighting", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    const { container } = render(
      <SafeMarkdown
        text={
          "Use `foo` here.\n\n```typescript\nconst answer: number = 42;\nconsole.log(answer);\n```"
        }
      />,
    );

    const inline = screen.getByRole("button", {
      name: "Copy inline code: foo",
    });
    expect(inline).toHaveTextContent("foo");
    await user.click(inline);
    expect(writeText).toHaveBeenNthCalledWith(1, "foo");
    expect(inline).toHaveAttribute("data-copy-state", "copied");

    const highlighted = container.querySelector(
      "pre code.hljs.language-typescript",
    );
    expect(highlighted).toBeInTheDocument();
    expect(highlighted?.querySelector(".hljs-keyword")).toHaveTextContent(
      "const",
    );
    expect(container.querySelector("pre button")).toBeNull();

    const copyBlock = screen.getByRole("button", { name: "Copy code" });
    await user.click(copyBlock);
    expect(writeText).toHaveBeenNthCalledWith(
      2,
      "const answer: number = 42;\nconsole.log(answer);",
    );
    expect(copyBlock).toHaveAttribute("data-copy-state", "copied");
  });
  it("does not request session filters until Apply", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByRole("heading", { name: "Hello session" }),
    ).toBeInTheDocument();
    const before = callsFor("/api/v1/session-groups?");
    await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.type(
      screen.getByRole("textbox", { name: "Working directory" }),
      "/work/demo",
    );
    expect(callsFor("/api/v1/session-groups?")).toBe(before);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Settings" }));
    await user.type(
      screen.getByRole("textbox", { name: "Working directory" }),
      "/work/demo",
    );
    await user.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(callsFor("/api/v1/session-groups?")).toBe(before + 1),
    );
    expect(
      vi
        .mocked(fetch)
        .mock.calls.some(([input]) =>
          String(input).includes("cwd=%2Fwork%2Fdemo"),
        ),
    ).toBe(true);
  });
  it("renders every parentThreadId relationship as an expanded tree", async () => {
    const parent = { ...session, title: "Plan session" };
    const child: SessionSummary = {
      ...session,
      id: "s2",
      title: "Review child",
      parentThreadId: "s1",
      parentRelation: "parent",
      source: "subagent",
    };
    const handoff: SessionSummary = {
      ...session,
      id: "s3",
      title: "Handoff payload",
      parentThreadId: "s1",
      parentRelation: "planHandoff",
      source: "exec",
    };
    const groups: SessionGroup[] = [
      {
        root: {
          session: parent,
          children: [
            { session: child, children: [] },
            { session: handoff, children: [] },
          ],
        },
        latestSessionId: "s3",
        updatedAt: handoff.updatedAt,
        hierarchyComplete: true,
      },
    ];
    const fallback = vi.mocked(fetch);
    vi.stubGlobal(
      "fetch",
      vi.fn((input: string | URL | Request, init?: RequestInit) =>
        String(input).includes("/session-groups")
          ? Promise.resolve(
              new Response(JSON.stringify({ data: groups, partial: false }), {
                status: 200,
                headers: { "content-type": "application/json" },
              }),
            )
          : fallback(input, init),
      ),
    );
    const { container } = render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(await screen.findByText("Review child")).toBeInTheDocument();
    expect(screen.getByText("Implement · Plan session")).toBeInTheDocument();
    expect(
      container.querySelectorAll(".session-children > .session-tree-node"),
    ).toHaveLength(2);
    expect(container.querySelector('a[href="/sessions/s1"]')).toHaveAttribute(
      "aria-current",
      "page",
    );
  });
  it("renders copyable chat bubbles and single-line activity without per-item times", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    const inspect = vi.fn();
    const reasoning = {
      ...entry,
      id: "e2",
      sequence: 2,
      kind: "reasoning" as const,
      presentation: "technical" as const,
      role: undefined,
      title: "Reasoning",
      primaryPreview: "First useful line",
      defaultCollapsed: true,
    };
    const command = {
      ...entry,
      id: "e3",
      sequence: 3,
      kind: "tool" as const,
      presentation: "technical" as const,
      role: undefined,
      toolKind: "command" as const,
      title: "exec_command",
      primaryPreview: '{"cmd":"printf hello\\nprintf world"}',
      secondaryPreview: "secret output",
      defaultCollapsed: true,
    };
    const { container } = render(
      <VirtualTranscript
        entries={[entry, reasoning, command]}
        onInspect={inspect}
      />,
    );
    expect(container.querySelector(".message-user")).toBeInTheDocument();
    expect(screen.getByText("Reasoning:")).toHaveClass("activity-label");
    expect(screen.getByText("First useful line")).toHaveClass("activity-body");
    expect(screen.getByText("Executing:")).toHaveClass("activity-label");
    const executingButton = screen.getByRole("button", {
      name: /Executing: printf hello/,
    });
    expect(executingButton.querySelector(".activity-body")).toHaveTextContent(
      "printf hello…",
    );
    await user.hover(executingButton);
    expect(await screen.findByRole("tooltip")).toHaveTextContent(
      /printf hello\s+printf world/,
    );
    expect(screen.queryByText("secret output")).not.toBeInTheDocument();
    const reasoningButton = screen.getByRole("button", {
      name: /Reasoning: First useful line/,
    });
    expect(within(reasoningButton).queryByRole("time")).not.toBeInTheDocument();
    await user.click(reasoningButton);
    expect(inspect).toHaveBeenCalledWith("e2");
    await user.click(screen.getByRole("button", { name: "Copy message" }));
    expect(writeText).toHaveBeenCalledWith("Hello **world**");
    expect(executedContent('{"action":{"command":["git","status"]}}')).toBe(
      "git status",
    );
    expect(isDefaultVisible(command)).toBe(true);
  });
  it("renders plans as copyable inspectable assistant bubbles", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    const inspect = vi.fn();
    const plan = {
      ...entry,
      id: "plan-entry",
      kind: "plan" as const,
      presentation: "technical" as const,
      role: undefined,
      title: "Plan",
      primaryPreview: "# Delivery plan\n\n- Ship it",
      primaryBytes: 26,
    };
    const { container } = render(
      <VirtualTranscript entries={[plan]} onInspect={inspect} />,
    );

    expect(container.querySelector(".message-assistant")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Delivery plan" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy message" }));
    expect(writeText).toHaveBeenCalledWith("# Delivery plan\n\n- Ship it");
    await user.click(screen.getByRole("button", { name: "Open inspector" }));
    expect(inspect).toHaveBeenCalledWith("plan-entry");
    expect(isDefaultVisible(plan)).toBe(true);
  });
  it("renders localized attachment counts without rendering or copying payloads", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    const attached = {
      ...entry,
      id: "attachment-only",
      primaryPreview: "",
      primaryBytes: 0,
      metadata: {
        attachmentCount: 4,
        imageAttachmentCount: 2,
        audioAttachmentCount: 1,
        imageUrl: "data:image/png;base64,must-not-render",
        audioUrl: "data:audio/wav;base64,must-not-render",
      },
    };
    const { container } = render(
      <VirtualTranscript entries={[attached]} onInspect={vi.fn()} />,
    );

    expect(
      screen.getByRole("list", { name: "Attachments" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Images: 2")).toBeInTheDocument();
    expect(screen.getByText("Audio: 1")).toBeInTheDocument();
    expect(screen.getByText("Other: 1")).toBeInTheDocument();
    expect(container.querySelector("img")).not.toBeInTheDocument();
    expect(container.querySelector("audio")).not.toBeInTheDocument();
    expect(screen.queryByText(/must-not-render/)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Copy message" }));
    expect(writeText).toHaveBeenCalledWith("");
  });
  it("renders each request_user_input question as an incoming poll message", async () => {
    const user = userEvent.setup();
    const inspect = vi.fn();
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
          {
            label: "Safe",
            description: "Use the slower synthetic rollout.",
          },
          {
            label: "Fast",
            description: "Use the faster synthetic rollout.",
          },
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
    const request = {
      ...entry,
      id: "request-user-input",
      kind: "tool" as const,
      presentation: "technical" as const,
      role: undefined,
      toolKind: "requestUserInput" as const,
      toolStatus: "running" as const,
      title: "request_user_input",
      primaryPreview: "synthetic request",
      metadata: { requestUserInputQuestions: questions },
      defaultCollapsed: true,
    };
    const { container, rerender } = render(
      <VirtualTranscript entries={[request]} onInspect={inspect} />,
    );
    let polls = container.querySelectorAll<HTMLElement>(
      ".request-user-input-message",
    );
    expect(polls).toHaveLength(3);
    for (const poll of polls) {
      expect(poll).toHaveClass("message-assistant");
      expect(poll).toHaveAttribute("data-transcript-entry");
      expect(
        within(poll).getByRole("button", { name: /Open inspector:/ }),
      ).toBeVisible();
    }
    let options = container.querySelectorAll(".request-user-input-option");
    expect(options).toHaveLength(6);
    expect(options[0]).toHaveTextContent(
      "Staging — Use the synthetic staging environment.",
    );
    expect(options[1]).toHaveTextContent(
      "Production — Use the synthetic production environment.",
    );
    expect(
      container.querySelector(".request-user-input-option.is-selected"),
    ).not.toBeInTheDocument();
    expect(
      container.querySelector(".request-user-input-radio svg"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("notes:")).not.toBeInTheDocument();
    expect(screen.queryByText("Target")).not.toBeInTheDocument();
    expect(screen.queryByText(/^Q:/)).not.toBeInTheDocument();
    expect(isDefaultVisible(request)).toBe(true);

    const answered = {
      ...request,
      toolStatus: "succeeded" as const,
      metadata: {
        requestUserInputQuestions: questions,
        requestUserInputAnswers: {
          target: {
            answers: ["Production", "user_note: Use the synthetic canary."],
          },
          rollout: {
            answers: [
              "None of the above",
              "user_note: Use a custom synthetic rollout.",
            ],
          },
          fallback: { answers: ["Retry"] },
        },
      },
    };
    rerender(<VirtualTranscript entries={[answered]} onInspect={inspect} />);
    polls = container.querySelectorAll<HTMLElement>(
      ".request-user-input-message",
    );
    expect(polls).toHaveLength(3);
    options = container.querySelectorAll(
      ".request-user-input-option.is-selected",
    );
    expect(options).toHaveLength(3);
    const production = screen.getByText("Production").closest("li");
    expect(production).toHaveClass("is-selected");
    expect(
      production?.querySelector(".request-user-input-radio svg"),
    ).toBeInTheDocument();
    expect(
      within(production!).getByText("Use the synthetic canary."),
    ).toHaveClass("request-user-input-option-note");
    const other = screen.getByText("None of the above").closest("li");
    expect(other).toHaveClass("is-selected");
    expect(
      within(other!).getByText("Use a custom synthetic rollout."),
    ).toHaveClass("request-user-input-option-note");
    expect(screen.queryByText("notes:")).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", {
        name: "Open inspector: Where should this run?",
      }),
    );
    expect(inspect).toHaveBeenCalledWith("request-user-input");
  });
  it("places legacy request_user_input notes under the sole selected option", () => {
    const request = {
      ...entry,
      id: "legacy-request-user-input",
      kind: "tool" as const,
      presentation: "technical" as const,
      role: undefined,
      toolKind: "requestUserInput" as const,
      title: "request_user_input",
      metadata: {
        requestUserInputQuestions: [
          {
            id: "target",
            question: "Where should this run?",
            isSecret: false,
            options: [
              { label: "Staging", description: "Use synthetic staging." },
              { label: "Production", description: "Use synthetic production." },
            ],
          },
        ],
        requestUserInputAnswers: { target: { answers: ["Production"] } },
        requestUserInputNotes: "Legacy synthetic note.",
      },
    };
    render(<VirtualTranscript entries={[request]} onInspect={() => {}} />);
    const production = screen.getByText("Production").closest("li");
    expect(within(production!).getByText("Legacy synthetic note.")).toHaveClass(
      "request-user-input-option-note",
    );
  });
  it("keeps ambiguous legacy notes as a footer on the final poll", () => {
    const question = (id: string, prompt: string) => ({
      id,
      question: prompt,
      isSecret: false,
      options: [
        { label: "First", description: "Use the first synthetic choice." },
        { label: "Second", description: "Use the second synthetic choice." },
      ],
    });
    const request = {
      ...entry,
      id: "ambiguous-legacy-request-user-input",
      kind: "tool" as const,
      presentation: "technical" as const,
      role: undefined,
      toolKind: "requestUserInput" as const,
      title: "request_user_input",
      metadata: {
        requestUserInputQuestions: [
          question("first", "Choose the first value"),
          question("second", "Choose the second value"),
        ],
        requestUserInputAnswers: {
          first: { answers: ["First"] },
          second: { answers: ["Second"] },
        },
        requestUserInputNotes: "Ambiguous synthetic legacy note.",
      },
    };
    const { container } = render(
      <VirtualTranscript entries={[request]} onInspect={() => {}} />,
    );
    const polls = container.querySelectorAll<HTMLElement>(
      ".request-user-input-message",
    );
    expect(polls).toHaveLength(2);
    expect(
      within(polls[1]).getByText(/Ambiguous synthetic legacy note\./),
    ).toHaveClass("request-user-input-legacy-note");
  });
  it("does not expose secret request_user_input answers or notes", () => {
    const secret = {
      ...entry,
      id: "secret-request-user-input",
      kind: "tool" as const,
      presentation: "technical" as const,
      role: undefined,
      toolKind: "requestUserInput" as const,
      title: "request_user_input",
      metadata: {
        requestUserInputQuestions: [
          {
            id: "secret",
            question: "Choose a secret value",
            isSecret: true,
            options: [
              { label: "First", description: "The first secret value." },
              { label: "Second", description: "The second secret value." },
            ],
          },
        ],
        requestUserInputAnswers: {
          secret: {
            answers: ["Second", "user_note: Sensitive synthetic note."],
          },
        },
      },
    };
    const { container } = render(
      <VirtualTranscript entries={[secret]} onInspect={() => {}} />,
    );
    expect(
      container.querySelector(".request-user-input-option.is-selected"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("notes:")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Sensitive synthetic note."),
    ).not.toBeInTheDocument();
  });
  it("loads and displays complete message content before copying a truncated bubble", async () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, "writeText");
    render(
      <VirtualTranscript
        entries={[
          { ...entry, primaryPreview: "Hello…", primaryComplete: false },
        ]}
        onInspect={() => {}}
      />,
    );
    expect(screen.getByText("Hello…")).toBeInTheDocument();
    expect(await screen.findByText("world")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy message" }));
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith("Hello **world**"),
    );
    expect(
      vi
        .mocked(fetch)
        .mock.calls.some(([input]) =>
          String(input).includes("/entries/e1/content?field=primary"),
        ),
    ).toBe(true);
  });
  it("shows a retry state when complete message loading fails", async () => {
    const user = userEvent.setup();
    const fallback = vi.mocked(fetch);
    let attempts = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn((input: string | URL | Request, init?: RequestInit) => {
        if (
          String(input).includes("/entries/e1/content?field=primary") &&
          attempts++ === 0
        )
          return Promise.resolve(
            new Response(JSON.stringify({ error: "synthetic failure" }), {
              status: 500,
              headers: { "content-type": "application/json" },
            }),
          );
        return fallback(input, init);
      }),
    );
    render(
      <VirtualTranscript
        entries={[
          { ...entry, primaryPreview: "Hello…", primaryComplete: false },
        ]}
        onInspect={() => {}}
      />,
    );
    expect(
      await screen.findByText("Could not load the complete message."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("world")).toBeInTheDocument();
    expect(attempts).toBe(2);
  });
  it("renders true-boundary navigation and keeps a 10,000-entry transcript below 200 DOM rows", async () => {
    const user = userEvent.setup();
    const top = vi.fn();
    const bottom = vi.fn();
    const entries = Array.from({ length: 10000 }, (_, index) => ({
      ...entry,
      id: `e${index}`,
      sequence: index,
    }));
    const { container } = render(
      <VirtualTranscript
        entries={entries}
        hasOlder
        hasNewer
        newCount={3}
        onInspect={() => {}}
        onJumpTop={top}
        onJumpBottom={bottom}
      />,
    );
    await waitFor(() =>
      expect(
        container.querySelectorAll("[data-transcript-entry]").length,
      ).toBeGreaterThan(0),
    );
    expect(
      container.querySelectorAll("[data-transcript-entry]").length,
    ).toBeLessThan(200);
    await user.click(
      screen.getByRole("button", { name: "Go to first message" }),
    );
    await user.click(screen.getByRole("button", { name: "Go to 3 new items" }));
    expect(top).toHaveBeenCalled();
    expect(bottom).toHaveBeenCalled();
  });
  it("treats transcript scroll target tokens as one-shot commands", () => {
    expect(shouldApplyScrollTarget(1, undefined, 20)).toBe(true);
    expect(shouldApplyScrollTarget(1, 1, 21)).toBe(false);
    expect(shouldApplyScrollTarget(2, 1, 21)).toBe(true);
    expect(shouldApplyScrollTarget(undefined, 1, 21)).toBe(false);
    expect(shouldApplyScrollTarget(1, undefined, 0)).toBe(false);
  });
  it("uses one event stream and coalesces refreshes by event type", async () => {
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByRole("heading", { name: "Hello session" }),
    ).toBeInTheDocument();
    expect(eventSources().instances).toHaveLength(1);
    const stream = eventSources().instances[0];
    expect(stream.onerror).toBeNull();
    const listBefore = callsFor("/api/v1/session-groups?");
    const statusBefore = callsFor("/api/v1/status");
    stream.emit("heartbeat", { generation: 2 });
    stream.emit("catalogProgress", {
      generation: 2,
      phase: "indexing",
      progress: {
        totalFiles: 10,
        processedFiles: 5,
        totalBytes: 10,
        processedBytes: 5,
        failedFiles: 0,
        excludedFiles: 0,
        excludedBytes: 0,
      },
    });
    expect(await screen.findAllByText("Indexing 5 / 10")).toHaveLength(2);
    expect(callsFor("/api/v1/session-groups?")).toBe(listBefore);
    expect(callsFor("/api/v1/status")).toBe(statusBefore);
    const entriesBefore = callsFor("/api/v1/sessions/s1/entries");
    stream.emit("catalogUpdated", {
      generation: 3,
      sessionId: "s1",
    });
    stream.emit("catalogUpdated", { generation: 3, sessionId: "s2" });
    stream.emit("catalogUpdated", { generation: 3, sessionId: "s3" });
    stream.emit("snapshotUpdated", {
      generation: 3,
      sessionId: "s1",
      entryId: "e1",
      snapshotRevision: 2,
    });
    await waitFor(() =>
      expect(callsFor("/api/v1/session-groups?")).toBe(listBefore + 1),
    );
    await waitFor(() =>
      expect(callsFor("/api/v1/sessions/s1/entries")).toBe(entriesBefore + 1),
    );
    expect(callsFor("/api/v1/session-groups?")).toBe(listBefore + 1);
  });
  it("prefetches a stale live tail without moving the reader", async () => {
    const fallback = vi.mocked(fetch);
    let resolveFirstTail: (response: Response) => void = () => {};
    const firstTail = new Promise<Response>((resolve) => {
      resolveFirstTail = resolve;
    });
    const controlled = vi.fn(
      (input: string | URL | Request, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("aroundEntryId=e1")) return firstTail;
        if (url.includes("aroundEntryId=e2"))
          return Promise.resolve(
            new Response(
              JSON.stringify({
                data: [liveEntry, laterLiveEntry],
                partial: false,
              }),
              { status: 200, headers: { "content-type": "application/json" } },
            ),
          );
        return fallback(input, init);
      },
    );
    vi.stubGlobal("fetch", controlled);
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByRole("heading", { name: "Hello session" }),
    ).toBeInTheDocument();
    const transcript = document.getElementById("transcript-scroll")!;
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, value: 1200 },
    });
    fireEvent.wheel(transcript, { deltaY: -100 });
    fireEvent.scroll(transcript, { target: { scrollTop: 400 } });
    const readerPosition = transcript.scrollTop;
    const stream = eventSources().instances[0];

    stream.emit("snapshotUpdated", {
      generation: 2,
      sessionId: "s1",
      entryId: liveEntry.id,
      snapshotRevision: 2,
    });
    await waitFor(() =>
      expect(
        controlled.mock.calls.filter(([input]) =>
          String(input).includes("aroundEntryId=e1"),
        ),
      ).toHaveLength(1),
    );
    expect(
      await screen.findByRole("button", { name: "Go to 1 new items" }),
    ).toBeInTheDocument();

    stream.emit("snapshotUpdated", {
      generation: 3,
      sessionId: "s1",
      entryId: laterLiveEntry.id,
      snapshotRevision: 3,
    });
    expect(
      await screen.findByRole("button", { name: "Go to 2 new items" }),
    ).toBeInTheDocument();
    expect(
      controlled.mock.calls.filter(([input]) =>
        String(input).includes("aroundEntryId=e1"),
      ),
    ).toHaveLength(1);

    resolveFirstTail(
      new Response(
        JSON.stringify({ data: [entry, liveEntry], partial: false }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    );
    await waitFor(() =>
      expect(
        controlled.mock.calls.some(([input]) =>
          String(input).includes("aroundEntryId=e2"),
        ),
      ).toBe(true),
    );
    expect(await screen.findByText("Second live tail entry")).toBeVisible();
    expect(transcript.scrollTop).toBe(readerPosition);

    fireEvent.scroll(transcript, { target: { scrollTop: 800 } });
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /new items?/ }),
      ).not.toBeInTheDocument(),
    );
  });
  it("allows only one in-flight request for a pagination cursor", async () => {
    const fallback = vi.mocked(fetch);
    let resolveCursor: (response: Response) => void = () => {};
    const pending = new Promise<Response>((resolve) => {
      resolveCursor = resolve;
    });
    const controlled = vi.fn(
      (input: string | URL | Request, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("cursor=cursor-1")) return pending;
        if (url.includes("/sessions/s1/entries"))
          return Promise.resolve(
            new Response(
              JSON.stringify({
                data: [entry],
                previousCursor: "cursor-1",
                partial: false,
              }),
              { status: 200, headers: { "content-type": "application/json" } },
            ),
          );
        return fallback(input, init);
      },
    );
    vi.stubGlobal("fetch", controlled);
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByRole("heading", { name: "Hello session" }),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: "Go to first message" }),
    ).toBeInTheDocument();
    const transcript = document.getElementById("transcript-scroll")!;
    Object.defineProperties(transcript, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, value: 1200 },
    });
    fireEvent.scroll(transcript, { target: { scrollTop: 0 } });
    fireEvent.scroll(transcript, { target: { scrollTop: 0 } });
    await waitFor(() =>
      expect(
        controlled.mock.calls.filter(([input]) =>
          String(input).includes("cursor=cursor-1"),
        ),
      ).toHaveLength(1),
    );
    await new Promise((resolve) => setTimeout(resolve, 150));
    expect(
      controlled.mock.calls.filter(([input]) =>
        String(input).includes("cursor=cursor-1"),
      ),
    ).toHaveLength(1);
    resolveCursor(
      new Response(JSON.stringify({ data: [], partial: false }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
  });

  it("starts content synchronization only after an explicit page-scoped action", async () => {
    const fallback = vi.mocked(fetch);
    const uncached = {
      ...session,
      entryCount: 0,
      contentStatus: {
        ...session.contentStatus,
        freshness: "neverSynced" as const,
        hasSnapshot: false,
        snapshotRevision: 0,
        syncedThroughBytes: 0,
      },
    };
    let leaseSignal: AbortSignal | undefined;
    const controlled = vi.fn(
      (input: string | URL | Request, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith("/sessions/s1/live-sync")) {
          leaseSignal = init?.signal ?? undefined;
          return Promise.resolve(
            new Response(new ReadableStream(), {
              status: 200,
              headers: { "content-type": "text/event-stream" },
            }),
          );
        }
        if (url.endsWith("/sessions/s1"))
          return Promise.resolve(
            new Response(JSON.stringify({ summary: uncached, diagnostics: [] }), {
              status: 200,
              headers: { "content-type": "application/json" },
            }),
          );
        if (url.includes("/sessions/s1/entries"))
          return Promise.resolve(
            new Response(JSON.stringify({ data: [], partial: false }), {
              status: 200,
              headers: { "content-type": "application/json" },
            }),
          );
        return fallback(input, init);
      },
    );
    vi.stubGlobal("fetch", controlled);
    const user = userEvent.setup();
    const view = render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByText("Conversation content has not been synchronized yet."),
    ).toBeInTheDocument();
    expect(screen.getByText("Hello **world**")).toBeInTheDocument();
    expect(
      controlled.mock.calls.filter(([input]) =>
        String(input).endsWith("/sessions/s1/live-sync"),
      ),
    ).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Start live sync" }));
    await waitFor(() => expect(leaseSignal).toBeDefined());
    expect(leaseSignal?.aborted).toBe(false);
    expect(
      screen.getByRole("button", { name: "Stop live sync" }),
    ).toBeInTheDocument();
    view.unmount();
    expect(leaseSignal?.aborted).toBe(true);
  });

  it("loads and merges raw record chunks only when requested", async () => {
    const fallback = vi.mocked(fetch);
    vi.stubGlobal(
      "fetch",
      vi.fn((input: string | URL | Request, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("/sessions/s1/raw/r1")) {
          const second = url.includes("offset=6");
          return Promise.resolve(
            new Response(
              JSON.stringify({
                summary: {
                  id: "r1",
                  sessionId: "s1",
                  line: 1,
                  byteOffset: 0,
                  byteLength: 12,
                  envelopeType: "event_msg",
                  parseStatus: "valid",
                  encoding: "utf8",
                  oversize: false,
                },
                chunk: {
                  field: "primary",
                  text: second ? "second" : "first-",
                  byteOffset: second ? 6 : 0,
                  totalBytes: 12,
                  complete: second,
                  ...(second ? {} : { nextOffset: 6 }),
                },
              }),
              { status: 200, headers: { "content-type": "application/json" } },
            ),
          );
        }
        return fallback(input, init);
      }),
    );
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    await screen.findByRole("heading", { name: "Hello session" });
    await user.click(
      screen.getAllByRole("button", { name: "Open inspector" }).at(-1)!,
    );
    const inspector = await screen.findByRole("complementary", {
      name: "Inspector",
    });
    await user.click(
      within(inspector).getByRole("button", { name: /event_msg/ }),
    );
    expect(await within(inspector).findByText("first-")).toBeInTheDocument();
    expect(callsFor("/sessions/s1/raw/r1")).toBe(1);
    await user.click(
      within(inspector).getByRole("button", { name: "Load more" }),
    );
    expect(
      await within(inspector).findByText("first-second"),
    ).toBeInTheDocument();
    expect(callsFor("/sessions/s1/raw/r1")).toBe(2);
  });

  it("loads the next sidebar page and retains that depth after live refresh", async () => {
    const firstPage = Array.from({ length: 200 }, (_, index) => {
      const rootSession =
        index === 0
          ? session
          : {
              ...session,
              id: `root-${index}`,
              title: `Root ${index}`,
            };
      return {
        root: { session: rootSession, children: [] },
        latestSessionId: rootSession.id,
        updatedAt: rootSession.updatedAt,
        hierarchyComplete: true,
      } satisfies SessionGroup;
    });
    const secondSession = {
      ...session,
      id: "root-200",
      title: "Second page root",
    };
    const secondPage: SessionGroup[] = [
      {
        root: { session: secondSession, children: [] },
        latestSessionId: secondSession.id,
        updatedAt: secondSession.updatedAt,
        hierarchyComplete: true,
      },
    ];
    const fallback = vi.mocked(fetch);
    const controlled = vi.fn(
      (input: string | URL | Request, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("/session-groups")) {
          const next = url.includes("cursor=page-2");
          return Promise.resolve(
            new Response(
              JSON.stringify({
                data: next ? secondPage : firstPage,
                nextCursor: next ? undefined : "page-2",
                partial: false,
              }),
              { status: 200, headers: { "content-type": "application/json" } },
            ),
          );
        }
        return fallback(input, init);
      },
    );
    vi.stubGlobal("fetch", controlled);
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/sessions/s1"]}>
        <App />
      </MemoryRouter>,
    );
    await screen.findByRole("heading", { name: "Hello session" });
    expect(screen.queryByText("Second page root")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Second page root")).toBeInTheDocument();
    const secondPageCalls = () =>
      controlled.mock.calls.filter(([input]) =>
        String(input).includes("cursor=page-2"),
      ).length;
    expect(secondPageCalls()).toBe(1);

    eventSources().instances[0].emit("catalogUpdated", {
      generation: 2,
      sessionId: "s1",
    });
    await waitFor(() => expect(secondPageCalls()).toBe(2));
    expect(screen.getByText("Second page root")).toBeInTheDocument();
  });

  it("shows the same partial-search warning on the page and dialog", async () => {
    const fallback = vi.mocked(fetch);
    vi.stubGlobal(
      "fetch",
      vi.fn((input: string | URL | Request, init?: RequestInit) => {
        if (String(input).includes("/search"))
          return Promise.resolve(
            new Response(JSON.stringify({ data: [], partial: true }), {
              status: 200,
              headers: { "content-type": "application/json" },
            }),
          );
        return fallback(input, init);
      }),
    );
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/search?q=partial"]}>
        <App />
      </MemoryRouter>,
    );
    expect(
      await screen.findByText("Results may be incomplete"),
    ).toBeInTheDocument();
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    await user.type(
      screen.getByRole("combobox", { name: "Search" }),
      "partial",
    );
    await waitFor(() =>
      expect(screen.getAllByText("Results may be incomplete")).toHaveLength(2),
    );
  });

  it("marks list entries whose title or metadata was safely abbreviated", () => {
    render(
      <VirtualTranscript
        entries={[{ ...entry, titleComplete: false, metadataComplete: false }]}
        onInspect={() => {}}
      />,
    );
    expect(
      screen.getByText(
        "Some list details are abbreviated; open the inspector for complete values.",
      ),
    ).toBeInTheDocument();
  });
});
