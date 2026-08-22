import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDownToLine, ArrowUpToLine } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { EntryListItem } from "@/generated/api";
import { TranscriptEntryView } from "@/viewer/entry-renderer";

export type ScrollTarget = {
  kind: "top" | "bottom" | "around";
  id?: string;
  token: number;
};
export type ViewportState = { atBottom: boolean; anchorId?: string };

export const TRANSCRIPT_BOTTOM_THRESHOLD = 80;
export const TRANSCRIPT_EXACT_BOTTOM_TOLERANCE = 1;

export function transcriptBottomDistance(element: HTMLElement) {
  return Math.max(
    0,
    element.scrollHeight - element.scrollTop - element.clientHeight,
  );
}
export function shouldApplyScrollTarget(
  targetToken: number | undefined,
  appliedToken: number | undefined,
  entryCount: number,
) {
  return (
    entryCount > 0 && targetToken !== undefined && targetToken !== appliedToken
  );
}

type VirtualTranscriptProps = {
  entries: EntryListItem[];
  around?: string;
  onInspect: (id: string) => void;
  hasOlder?: boolean;
  hasNewer?: boolean;
  newCount?: number;
  scrollTarget?: ScrollTarget;
  onLoadOlder?: () => Promise<void> | void;
  onLoadNewer?: () => Promise<void> | void;
  onJumpTop?: () => Promise<void> | void;
  onJumpBottom?: () => Promise<void> | void;
  onViewportChange?: (state: ViewportState) => void;
};

export function VirtualTranscript({
  entries,
  around,
  onInspect,
  hasOlder = false,
  hasNewer = false,
  newCount = 0,
  scrollTarget,
  onLoadOlder,
  onLoadNewer,
  onJumpTop,
  onJumpBottom,
  onViewportChange,
}: VirtualTranscriptProps) {
  const { t, i18n } = useTranslation();
  const parent = useRef<HTMLDivElement>(null);
  const transcriptInner = useRef<HTMLDivElement>(null);
  const initialized = useRef(false);
  const appliedScrollTarget = useRef<number | undefined>(undefined);
  const applyingBottomTarget = useRef<number | undefined>(undefined);
  const pinToBottom = useRef(!around);
  const geometryFrame = useRef<number | undefined>(undefined);
  const loadingOlder = useRef(false);
  const loadingNewer = useRef(false);
  const restoreAnchor = useRef<{ id: string; offset: number } | undefined>(
    undefined,
  );
  const [atTop, setAtTop] = useState(false);
  const [atBottom, setAtBottom] = useState(!around);
  const virtual = useVirtualizer({
    count: entries.length,
    getScrollElement: () => parent.current,
    getItemKey: (index) => entries[index]?.id ?? index,
    estimateSize: (index) =>
      entries[index]?.kind === "message" || entries[index]?.kind === "plan"
        ? 96
        : 36,
    overscan: 10,
    anchorTo: "end",
    initialRect: { width: 800, height: 800 },
    measureElement: (element) => element.getBoundingClientRect().height,
  });
  const measuredRows = virtual.getVirtualItems();
  const rows =
    measuredRows.length > 0
      ? measuredRows
      : entries.slice(0, 12).map((_, index) => ({
          index,
          start: index * 64,
          key: entries[index].id,
        }));

  const reportViewport = useCallback(() => {
    const element = parent.current;
    if (!element) return;
    const remaining = transcriptBottomDistance(element);
    const first = virtual
      .getVirtualItems()
      .find((row) => row.end >= element.scrollTop);
    const trueTop =
      element.scrollTop <= TRANSCRIPT_BOTTOM_THRESHOLD && !hasOlder;
    const trueBottom =
      remaining <= TRANSCRIPT_BOTTOM_THRESHOLD && !hasNewer;
    setAtTop(trueTop);
    setAtBottom(trueBottom);
    onViewportChange?.({
      atBottom: trueBottom,
      anchorId: first ? entries[first.index]?.id : entries[0]?.id,
    });
  }, [entries, hasNewer, hasOlder, onViewportChange, virtual]);

  const scrollToPinnedBottom = useCallback(() => {
    const element = parent.current;
    if (!element) return false;
    element.scrollTop = element.scrollHeight;
    return (
      transcriptBottomDistance(element) <=
      TRANSCRIPT_EXACT_BOTTOM_TOLERANCE
    );
  }, []);

  const scheduleGeometrySync = useCallback(() => {
    if (geometryFrame.current !== undefined) return;
    geometryFrame.current = requestAnimationFrame(() => {
      geometryFrame.current = undefined;
      const element = parent.current;
      if (!element) return;
      const landed = !pinToBottom.current || scrollToPinnedBottom();
      if (applyingBottomTarget.current !== undefined && landed) {
        applyingBottomTarget.current = undefined;
        initialized.current = true;
      }
      reportViewport();
    });
  }, [reportViewport, scrollToPinnedBottom]);

  useEffect(() => {
    const element = parent.current;
    const inner = transcriptInner.current;
    if (!element || !inner) return;
    const observer = new ResizeObserver(scheduleGeometrySync);
    observer.observe(element);
    observer.observe(inner);
    return () => observer.disconnect();
  }, [scheduleGeometrySync]);

  useEffect(
    () => () => {
      if (geometryFrame.current !== undefined)
        cancelAnimationFrame(geometryFrame.current);
    },
    [],
  );

  const captureRestoreAnchor = useCallback(() => {
    const element = parent.current;
    if (!element) return;
    const first = virtual.getVirtualItemForOffset(element.scrollTop);
    if (!first) return;
    restoreAnchor.current = {
      id: entries[first.index].id,
      offset: first.start - element.scrollTop,
    };
  }, [entries, virtual]);

  useEffect(() => {
    if (
      !scrollTarget ||
      !shouldApplyScrollTarget(
        scrollTarget.token,
        appliedScrollTarget.current,
        entries.length,
      )
    )
      return;
    const index =
      scrollTarget.kind === "top"
        ? 0
        : scrollTarget.kind === "bottom"
          ? entries.length - 1
          : entries.findIndex((entry) => entry.id === scrollTarget.id);
    if (index < 0) return;
    const align =
      scrollTarget.kind === "around"
        ? "center"
        : scrollTarget.kind === "bottom"
          ? "end"
          : "start";
    appliedScrollTarget.current = scrollTarget.token;
    pinToBottom.current = scrollTarget.kind === "bottom";
    applyingBottomTarget.current =
      scrollTarget.kind === "bottom" ? scrollTarget.token : undefined;
    initialized.current = false;
    if (scrollTarget.kind === "bottom") scrollToPinnedBottom();
    else virtual.scrollToIndex(index, { align });
    requestAnimationFrame(() => {
      if (appliedScrollTarget.current !== scrollTarget.token) return;
      if (scrollTarget.kind === "bottom" && !pinToBottom.current) return;
      if (scrollTarget.kind === "bottom") scrollToPinnedBottom();
      else virtual.scrollToIndex(index, { align });
      requestAnimationFrame(() => {
        if (appliedScrollTarget.current !== scrollTarget.token) return;
        if (scrollTarget.kind === "bottom" && !pinToBottom.current) return;
        if (scrollTarget.kind === "bottom") {
          if (!scrollToPinnedBottom()) {
            scheduleGeometrySync();
            return;
          }
        }
        applyingBottomTarget.current = undefined;
        initialized.current = true;
        reportViewport();
      });
    });
  }, [
    entries,
    reportViewport,
    scheduleGeometrySync,
    scrollTarget,
    scrollToPinnedBottom,
    virtual,
  ]);

  useEffect(() => {
    const anchor = restoreAnchor.current;
    if (!anchor) return;
    const index = entries.findIndex((entry) => entry.id === anchor.id);
    if (index < 0) {
      restoreAnchor.current = undefined;
      return;
    }
    initialized.current = false;
    virtual.scrollToIndex(index, { align: "start" });
    let correctionFrame = 0;
    let attempts = 0;
    let stableFrames = 0;
    const correct = () => {
      const element = parent.current;
      const row = element?.querySelector<HTMLElement>(
        `.entry-wrap[data-index="${index}"]`,
      );
      if (element && row) {
        const offset =
          row.getBoundingClientRect().top - element.getBoundingClientRect().top;
        const delta = offset - anchor.offset;
        if (Math.abs(delta) > 0.5) {
          element.scrollTop += delta;
          stableFrames = 0;
        } else {
          stableFrames += 1;
        }
      } else {
        virtual.scrollToIndex(index, { align: "start" });
        stableFrames = 0;
      }
      attempts += 1;
      if (attempts < 8 && stableFrames < 2) {
        correctionFrame = requestAnimationFrame(correct);
        return;
      }
      if (restoreAnchor.current === anchor) restoreAnchor.current = undefined;
      initialized.current = true;
      reportViewport();
    };
    correctionFrame = requestAnimationFrame(correct);
    return () => cancelAnimationFrame(correctionFrame);
  }, [entries, reportViewport, virtual]);

  const requestOlder = useCallback(async () => {
    if (!onLoadOlder || loadingOlder.current) return;
    loadingOlder.current = true;
    captureRestoreAnchor();
    const element = parent.current;
    if (element) virtual.scrollToOffset(element.scrollTop);
    try {
      await onLoadOlder();
    } finally {
      loadingOlder.current = false;
    }
  }, [captureRestoreAnchor, onLoadOlder, virtual]);

  const requestNewer = useCallback(async () => {
    if (!onLoadNewer || loadingNewer.current) return;
    loadingNewer.current = true;
    const element = parent.current;
    if (element && !pinToBottom.current)
      virtual.scrollToOffset(element.scrollTop);
    try {
      await onLoadNewer();
    } finally {
      loadingNewer.current = false;
    }
  }, [onLoadNewer, virtual]);

  const releaseBottomPin = useCallback(() => {
    pinToBottom.current = false;
    applyingBottomTarget.current = undefined;
    initialized.current = true;
  }, []);

  const handleScroll = useCallback(() => {
    const element = parent.current;
    if (!element) return;
    const remaining = transcriptBottomDistance(element);
    if (remaining <= TRANSCRIPT_BOTTOM_THRESHOLD)
      pinToBottom.current = true;
    reportViewport();
    if (!initialized.current) return;
    if (hasOlder && element.scrollTop <= 160) void requestOlder();
    if (hasNewer && remaining <= 160) void requestNewer();
  }, [hasNewer, hasOlder, reportViewport, requestNewer, requestOlder]);

  useEffect(() => {
    if (!initialized.current || measuredRows.length === 0) return;
    const first = measuredRows[0];
    const last = measuredRows.at(-1);
    if (hasOlder && first.index <= 3) void requestOlder();
    if (hasNewer && last && last.index >= entries.length - 4)
      void requestNewer();
  }, [
    entries.length,
    hasNewer,
    hasOlder,
    measuredRows,
    requestNewer,
    requestOlder,
  ]);

  const showTop = hasOlder || !atTop;
  const showBottom = hasNewer || !atBottom || newCount > 0;
  return (
    <TooltipProvider>
      <div className="transcript-shell">
        <div
          id="transcript-scroll"
          className="transcript"
          ref={parent}
          onScroll={handleScroll}
          onWheel={(event) => {
            if (event.deltaY < 0) releaseBottomPin();
          }}
          onTouchMove={releaseBottomPin}
          onPointerDown={(event) => {
            if (event.target === event.currentTarget) releaseBottomPin();
          }}
          onKeyDown={(event) => {
            if (["ArrowUp", "Home", "PageUp"].includes(event.key))
              releaseBottomPin();
          }}
        >
          <div
            className="transcript-inner"
            ref={transcriptInner}
            style={{ height: virtual.getTotalSize() }}
          >
            {rows.map((row) => {
              const entry = entries[row.index];
              return (
                <div
                  className="entry-wrap"
                  data-index={row.index}
                  ref={virtual.measureElement}
                  key={row.key}
                  style={{ transform: `translateY(${row.start}px)` }}
                >
                  <TranscriptEntryView
                    entry={entry}
                    previous={entries[row.index - 1]}
                    highlighted={entry.id === around}
                    locale={i18n.language}
                    onInspect={onInspect}
                  />
                </div>
              );
            })}
          </div>
        </div>
        <div
          className="transcript-nav"
          aria-label={t("conversationNavigation")}
        >
          {showTop && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  data-transcript-jump="top"
                  className="transcript-nav-button"
                  variant="outline"
                  size="icon"
                  aria-label={t("jumpTop")}
                  onClick={() => void onJumpTop?.()}
                >
                  <ArrowUpToLine size={18} />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t("jumpTop")}</TooltipContent>
            </Tooltip>
          )}
          {showBottom && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  data-transcript-jump="bottom"
                  className="transcript-nav-button"
                  variant="outline"
                  size="icon"
                  aria-label={
                    newCount > 0
                      ? t("jumpBottomNew", { count: newCount })
                      : t("jumpBottom")
                  }
                  onClick={() => void onJumpBottom?.()}
                >
                  <ArrowDownToLine size={18} />
                  {newCount > 0 && (
                    <span className="new-count" aria-hidden="true">
                      {newCount > 99 ? "99+" : newCount}
                    </span>
                  )}
                </Button>
              </TooltipTrigger>
              <TooltipContent>
                {newCount > 0
                  ? t("jumpBottomNew", { count: newCount })
                  : t("jumpBottom")}
              </TooltipContent>
            </Tooltip>
          )}
        </div>
        {newCount > 0 && (
          <span className="sr-only" role="status" aria-live="polite">
            {t("newContent", { count: newCount })}
          </span>
        )}
      </div>
    </TooltipProvider>
  );
}
