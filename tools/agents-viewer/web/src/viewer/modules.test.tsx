import { act, render, renderHook, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import type {
  EntryListItem,
  SessionGroup,
  SessionSummary,
} from "@/generated/api";
import { ApiClientError } from "@/lib/api";
import { copyStateLabel, reactNodeText } from "@/viewer/clipboard";
import { conversationPageTarget } from "@/viewer/conversation";
import { shouldOpenSearch } from "@/viewer/controller";
import { activityParts } from "@/viewer/entry-format";
import { TranscriptEntryView } from "@/viewer/entry-renderer";
import { EntryTime } from "@/viewer/entry-time";
import { mergeEntries, message } from "@/viewer/format";
import { Inspector } from "@/viewer/inspector";
import { requestUserInputDetails } from "@/viewer/request-user-input";
import { useSearchAllTypes } from "@/viewer/search";
import { SessionSidebar } from "@/viewer/session-browser";
import { SettingsControl } from "@/viewer/settings";

const session: SessionSummary = {
  id: "module-session",
  source: "cli",
  sourceLocation: {
    rootKind: "active",
    relativePath: "2026/08/22/module-session.jsonl",
  },
  contentStatus: {
    freshness: "current",
    liveState: "inactive",
    hasSnapshot: true,
    snapshotRevision: 1,
    syncedThroughBytes: 100,
    observedBytes: 100,
  },
  title: "Module session",
  preview: "Direct module test",
  createdAt: "2026-08-22T00:00:00Z",
  updatedAt: "2026-08-22T00:01:00Z",
  archived: false,
  entryCount: 1,
  diagnosticCount: 0,
  indexState: "ready",
  completeness: "complete",
  freshness: "current",
};

const entry: EntryListItem = {
  id: "module-entry",
  sessionId: session.id,
  sequence: 1,
  kind: "message",
  presentation: "user",
  role: "user",
  title: "User",
  titleComplete: true,
  primaryPreview: "Direct entry",
  secondaryPreview: "",
  primaryBytes: 12,
  secondaryBytes: 0,
  primaryComplete: true,
  secondaryComplete: true,
  defaultCollapsed: false,
  metadata: {},
  metadataComplete: true,
  rawRefCount: 0,
};

describe("extracted Viewer modules", () => {
  it("tests controller keyboard routing directly", () => {
    const event = (key: string, ctrlKey = false, shiftKey = false) => ({
      key,
      ctrlKey,
      shiftKey,
      metaKey: false,
    });
    expect(shouldOpenSearch(event("f", true, true), true, true)).toBe(true);
    expect(shouldOpenSearch(event("/"), false, false)).toBe(true);
    expect(shouldOpenSearch(event("/"), false, true)).toBe(false);
  });

  it("tests conversation anchor selection directly", () => {
    expect(conversationPageTarget("linked", { atBottom: true })).toEqual({
      kind: "around",
      id: "linked",
    });
    expect(
      conversationPageTarget(undefined, {
        atBottom: false,
        anchorId: "visible",
      }),
    ).toEqual({ kind: "around", id: "visible" });
    expect(conversationPageTarget(undefined, { atBottom: true })).toEqual({
      kind: "bottom",
    });
  });

  it("tests clipboard labels and React text extraction directly", () => {
    expect(copyStateLabel("copied", "copy", "copying", "done", "failed")).toBe(
      "done",
    );
    expect(
      reactNodeText(
        <span>
          A<strong>B</strong>
        </span>,
      ),
    ).toBe("AB");
  });

  it("tests entry formatting and rendering directly", () => {
    expect(activityParts({ ...entry, kind: "warning" }, (key) => key)).toEqual({
      label: "warning:",
      body: "Direct entry",
    });
    const inspect = vi.fn();
    render(
      <TooltipProvider>
        <TranscriptEntryView
          entry={entry}
          highlighted={false}
          locale="en"
          onInspect={inspect}
        />
      </TooltipProvider>,
    );
    expect(screen.getByText("Direct entry")).toBeInTheDocument();
  });

  it("tests entry time and shared format helpers directly", () => {
    const { container } = render(
      <EntryTime value={new Date("2026-08-22T12:34:00Z")} locale="en" />,
    );
    expect(container.querySelector("time")).toHaveAttribute(
      "dateTime",
      "2026-08-22T12:34:00.000Z",
    );
    expect(
      mergeEntries(
        [{ ...entry, sequence: 2 }],
        [{ ...entry, id: "earlier", sequence: 1 }],
      ).map(({ id }) => id),
    ).toEqual(["earlier", "module-entry"]);
    expect(message(new ApiClientError(400, "invalid_argument", "bad"))).toBe(
      "bad",
    );
  });

  it("tests request_user_input parsing directly", () => {
    const details = requestUserInputDetails({
      ...entry,
      kind: "tool",
      presentation: "technical",
      role: undefined,
      toolKind: "requestUserInput",
      metadata: {
        requestUserInputQuestions: [
          {
            id: "target",
            question: "Choose",
            isSecret: false,
            options: [{ label: "Safe", description: "Use safe" }],
          },
        ],
        requestUserInputAnswers: {
          target: { answers: ["Safe", "user_note: synthetic note"] },
        },
      },
    });
    expect(details?.questions[0].question).toBe("Choose");
    expect(details?.answers.get("target")).toEqual({
      selections: ["Safe"],
      note: "synthetic note",
    });
  });

  it("tests inspector, session browser, and settings modules directly", () => {
    render(<Inspector />);
    expect(
      screen.getByText("Choose Inspect on an entry to view technical details."),
    ).toBeInTheDocument();

    const group: SessionGroup = {
      root: { session, children: [] },
      latestSessionId: session.id,
      updatedAt: session.updatedAt,
      hierarchyComplete: false,
    };
    render(
      <MemoryRouter initialEntries={[`/sessions/${session.id}`]}>
        <SessionSidebar
          groups={[group]}
          loading={false}
          hasMore={false}
          loadingMore={false}
          error=""
          onNavigate={() => {}}
          onLoadMore={() => {}}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("Module session")).toBeInTheDocument();
    expect(
      screen.getByText("Deep hierarchy simplified for safe display"),
    ).toBeInTheDocument();

    render(
      <TooltipProvider>
        <SettingsControl
          archived="exclude"
          source=""
          cwd=""
          conversationDisplayTypes={[
            "received",
            "sent",
            "requestUserInput",
            "plan",
          ]}
          theme="system"
          language="en"
          searchCtrlShiftF={false}
          onApply={() => {}}
        />
      </TooltipProvider>,
    );
    expect(
      screen.getByRole("button", { name: /^Settings/ }),
    ).toBeInTheDocument();
  });

  it("tests search preference persistence directly", () => {
    localStorage.removeItem("agents-viewer-search-all-types");
    const { result } = renderHook(() => useSearchAllTypes());
    expect(result.current[0]).toBe(false);
    act(() => result.current[1](true));
    expect(result.current[0]).toBe(true);
    expect(localStorage.getItem("agents-viewer-search-all-types")).toBe("true");
  });
});
