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
import {
  App,
  conversationDisplayType,
  executedContent,
  isDefaultVisible,
  SafeMarkdown,
  shouldApplyScrollTarget,
  VirtualTranscript,
} from "@/App";
import { resources } from "@/lib/i18n";
import type {
  EntryListItem,
  SessionGroup,
  SessionSummary,
} from "@/generated/api";

const session: SessionSummary = {
  id: "s1",
  source: "cli",
  title: "Hello session",
  preview: "Preview",
  createdAt: "2026-07-01T00:00:00.000000Z",
  updatedAt: "2026-07-01T01:00:00.000000Z",
  archived: false,
  entryCount: 2,
  diagnosticCount: 0,
  indexState: "ready",
  completeness: "complete",
};
const sessionGroups: SessionGroup[] = [
  {
    root: { session, children: [] },
    latestSessionId: "s1",
    updatedAt: session.updatedAt,
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
  primaryPreview: "Hello **world**",
  secondaryPreview: "",
  primaryBytes: 15,
  secondaryBytes: 0,
  primaryComplete: true,
  secondaryComplete: true,
  defaultCollapsed: false,
  metadata: {},
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
type EventSourceHarness = {
  instances: Array<{ emit: (name: string, data: unknown) => void }>;
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
          sourceHome: "/source",
          cacheDir: "/cache",
          initialIndexDays: 7,
          initialIndexCutoff: "2026-01-01T00:00:00Z",
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
      [
        ReturnType<typeof conversationDisplayType>,
        Partial<EntryListItem>,
      ]
    > = [
      ["received", { kind: "message", presentation: "response" }],
      ["sent", { kind: "message", presentation: "user" }],
      [
        "requestUserInput",
        { kind: "tool", presentation: "technical", toolKind: "requestUserInput" },
      ],
      ["reasoning", { kind: "reasoning", presentation: "technical" }],
      ["exec", { kind: "tool", presentation: "technical", toolKind: "command" }],
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
      ["otherTool", { kind: "tool", presentation: "technical", toolKind: "other" }],
      ["warning", { kind: "warning", presentation: "technical" }],
      ["error", { kind: "error", presentation: "technical" }],
      ["context", { kind: "context", presentation: "technical" }],
      ["marker", { kind: "marker", presentation: "technical" }],
      [
        "technicalMessage",
        { kind: "message", presentation: "technical" },
      ],
      ["internalMessage", { kind: "message", presentation: "internal" }],
      ["unknown", { kind: "unknown", presentation: "technical" }],
    ];
    for (const [expected, override] of cases)
      expect(conversationDisplayType({ ...entry, ...override })).toBe(expected);
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
              "displayTypes=received%2Csent%2CrequestUserInput%2Cexec%2Cwarning",
            ),
          ),
      ).toBe(true),
    );
    expect(
      JSON.parse(
        localStorage.getItem(
          "agents-viewer-conversation-display-types",
        ) ?? "null",
      ),
    ).toEqual(["received", "sent", "requestUserInput", "exec", "warning"]);
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
    expect(
      screen.getByRole("checkbox", { name: "Reasoning" }),
    ).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Exec commands" }),
    ).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Patch activity" }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Warnings" }),
    ).not.toBeChecked();
  });
  it("temporarily includes a linked entry type without persisting it", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter
        initialEntries={["/sessions/s1?entry=warning-entry"]}
      >
        <App />
      </MemoryRouter>,
    );
    expect(await screen.findByText("Linked warning detail")).toBeInTheDocument();
    await waitFor(() =>
      expect(
        vi
          .mocked(fetch)
          .mock.calls.some(([input]) =>
            String(input).includes(
              "displayTypes=received%2Csent%2CrequestUserInput%2Creasoning%2Cexec%2Cwarning",
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
        localStorage.getItem(
          "agents-viewer-conversation-display-types",
        ) ?? "null",
      ),
    ).toEqual([
      "received",
      "sent",
      "requestUserInput",
      "reasoning",
      "exec",
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
    const listBefore = callsFor("/api/v1/session-groups?");
    const statusBefore = callsFor("/api/v1/status");
    stream.emit("heartbeat", { generation: 2 });
    stream.emit("indexProgress", {
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
    stream.emit("sessionUpdated", { generation: 3, sessionId: "s1" });
    stream.emit("sessionUpdated", { generation: 3, sessionId: "s2" });
    stream.emit("sessionUpdated", { generation: 3, sessionId: "s3" });
    await waitFor(() =>
      expect(callsFor("/api/v1/session-groups?")).toBe(listBefore + 1),
    );
    const entriesBefore = callsFor("/api/v1/sessions/s1/entries");
    stream.emit("entryUpdated", {
      generation: 3,
      sessionId: "s1",
      entryId: "e1",
    });
    await waitFor(() =>
      expect(callsFor("/api/v1/sessions/s1/entries")).toBe(entriesBefore + 1),
    );
    expect(callsFor("/api/v1/session-groups?")).toBe(listBefore + 1);
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
});
