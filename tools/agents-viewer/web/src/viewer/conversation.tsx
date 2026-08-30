import { useCallback, useEffect, useRef, useState } from "react";
import { Radio, Square } from "lucide-react";
import { useParams, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type {
  EntryListItem,
  LiveSyncState,
  SessionSummary,
  SessionSyncState,
} from "@/generated/api";
import { api } from "@/lib/api";
import {
  Empty,
  localizedTitle,
  mergeEntries,
  message,
  sourceLabel,
} from "@/viewer/format";
import {
  canonicalConversationDisplayTypes,
  conversationDisplayType,
  withConversationDisplayType,
  type ConversationDisplayType,
} from "@/viewer/preferences";
import {
  VirtualTranscript,
  type ScrollTarget,
  type ViewportState,
} from "@/viewer/virtual-transcript";

export function conversationPageTarget(
  around: string | undefined,
  viewport: ViewportState,
): { kind: "around" | "bottom"; id?: string } {
  const id = around ?? (!viewport.atBottom ? viewport.anchorId : undefined);
  return id ? { kind: "around", id } : { kind: "bottom" };
}

export function liveStateFromSyncState(
  state: SessionSyncState | undefined,
): LiveSyncState {
  switch (state) {
    case "checking":
    case "queued":
      return "starting";
    case "indexing":
      return "catchingUp";
    case "current":
      return "following";
    case "failed":
    case "sourceMissing":
    case "notFound":
      return "failed";
    default:
      return "inactive";
  }
}

export function Conversation({
  onInspect,
  signals,
  liveStateSignals,
  resyncSequence,
  conversationDisplayTypes,
  onForceConversationDisplayType,
}: {
  onInspect: (s: string, e: string) => void;
  signals: Record<string, number>;
  liveStateSignals: Record<
    string,
    { sequence: number; state?: SessionSyncState }
  >;
  resyncSequence: number;
  conversationDisplayTypes: ConversationDisplayType[];
  onForceConversationDisplayType: (
    value: ConversationDisplayType | undefined,
  ) => void;
}) {
  const { sessionId = "" } = useParams();
  const { t } = useTranslation();
  const [params] = useSearchParams();
  const around = params.get("entry") ?? undefined;
  const [session, setSession] = useState<SessionSummary>();
  const [liveConnection, setLiveConnection] = useState<
    "inactive" | "starting" | "active" | "failed"
  >("inactive");
  const [liveError, setLiveError] = useState("");
  const [entries, setEntries] = useState<EntryListItem[]>([]);
  const [previousCursor, setPreviousCursor] = useState<string>();
  const [nextCursor, setNextCursor] = useState<string>();
  const [error, setError] = useState("");
  const [newCount, setNewCount] = useState(0);
  const [pendingTailSequence, setPendingTailSequence] = useState<number>();
  const [visibilityReady, setVisibilityReady] = useState(false);
  const [deepLinkDisplayType, setDeepLinkDisplayType] =
    useState<ConversationDisplayType>();
  const [scrollTarget, setScrollTarget] = useState<ScrollTarget>();
  const viewport = useRef<ViewportState>({ atBottom: true });
  const requestSequence = useRef(0);
  const pageEpoch = useRef(0);
  const targetSequence = useRef(0);
  const loadingPages = useRef(
    new Map<string, { token: symbol; promise: Promise<void> }>(),
  );
  const pendingTailSequenceRef = useRef<number | undefined>(undefined);
  const handledSignal = useRef(0);
  const handledLiveStateSignal = useRef(0);
  const refreshTimer = useRef<number | undefined>(undefined);
  const liveController = useRef<AbortController | undefined>(undefined);
  const selectedConversationDisplayTypes = canonicalConversationDisplayTypes(
    conversationDisplayTypes,
  );
  const serializedSelectedConversationDisplayTypes =
    selectedConversationDisplayTypes.join(",");
  const effectiveConversationDisplayTypes = withConversationDisplayType(
    selectedConversationDisplayTypes,
    deepLinkDisplayType,
  );
  const serializedConversationDisplayTypes =
    effectiveConversationDisplayTypes.join(",");

  useEffect(() => {
    liveController.current?.abort();
    liveController.current = undefined;
    setSession(undefined);
    setLiveConnection("inactive");
    setLiveError("");
    setError("");
    return () => {
      liveController.current?.abort();
      liveController.current = undefined;
    };
  }, [sessionId]);

  useEffect(() => {
    const controller = new AbortController();
    pageEpoch.current += 1;
    loadingPages.current.clear();
    setEntries([]);
    setPreviousCursor(undefined);
    setNextCursor(undefined);
    setDeepLinkDisplayType(undefined);
    onForceConversationDisplayType(undefined);
    setNewCount(0);
    pendingTailSequenceRef.current = undefined;
    setPendingTailSequence(undefined);
    viewport.current = { atBottom: !around };
    if (!around) {
      setVisibilityReady(true);
      return () => controller.abort();
    }
    setVisibilityReady(false);
    api
      .entry(sessionId, around, controller.signal)
      .then((detail) => {
        const displayType = conversationDisplayType(detail.item);
        const forced = selectedConversationDisplayTypes.includes(displayType)
          ? undefined
          : displayType;
        setDeepLinkDisplayType(forced);
        onForceConversationDisplayType(forced);
        setVisibilityReady(true);
      })
      .catch((f) => {
        if (!(f instanceof DOMException)) {
          setError(message(f));
          setVisibilityReady(true);
        }
      });
    return () => controller.abort();
  }, [
    around,
    onForceConversationDisplayType,
    serializedSelectedConversationDisplayTypes,
    sessionId,
  ]);

  const replacePage = useCallback(
    async (
      kind: "top" | "bottom" | "around",
      id?: string,
      signal?: AbortSignal,
    ) => {
      const request = ++requestSequence.current;
      pageEpoch.current += 1;
      const pendingAtStart = pendingTailSequenceRef.current;
      const options =
        kind === "around" && id
          ? {
              limit: 100,
              aroundEntryId: id,
              displayTypes: serializedConversationDisplayTypes,
            }
          : {
              limit: 100,
              direction: kind === "top" ? "forward" : "backward",
              displayTypes: serializedConversationDisplayTypes,
            };
      try {
        const [detail, page] = await Promise.all([
          api.session(sessionId, signal),
          api.entries(sessionId, options, signal),
        ]);
        if (request !== requestSequence.current) return;
        pageEpoch.current += 1;
        setSession(detail.summary);
        setEntries(page.data);
        setPreviousCursor(page.previousCursor);
        setNextCursor(page.nextCursor);
        setScrollTarget({ kind, id, token: ++targetSequence.current });
        if (
          kind === "bottom" &&
          pendingTailSequenceRef.current === pendingAtStart
        ) {
          pendingTailSequenceRef.current = undefined;
          setPendingTailSequence(undefined);
          setNewCount(0);
        }
        setError("");
      } catch (f) {
        if (request === requestSequence.current && !(f instanceof DOMException))
          setError(message(f));
      }
    },
    [serializedConversationDisplayTypes, sessionId],
  );

  useEffect(() => {
    if (!visibilityReady) return;
    const controller = new AbortController();
    const target = conversationPageTarget(around, viewport.current);
    void replacePage(target.kind, target.id, controller.signal);
    return () => controller.abort();
  }, [around, replacePage, visibilityReady]);

  useEffect(() => {
    handledSignal.current = Math.max(signals[sessionId] ?? 0, resyncSequence);
    handledLiveStateSignal.current =
      liveStateSignals[sessionId]?.sequence ?? 0;
  }, [sessionId]);
  const liveStateEvent = liveStateSignals[sessionId];
  const liveStateEventSequence = liveStateEvent?.sequence ?? 0;
  useEffect(() => {
    if (
      liveStateEventSequence === 0 ||
      liveStateEventSequence <= handledLiveStateSignal.current
    )
      return;
    handledLiveStateSignal.current = liveStateEventSequence;
    setSession((current) => {
      if (!current) return current;
      const sourceMissing = liveStateEvent?.state === "sourceMissing";
      return {
        ...current,
        ...(sourceMissing ? { freshness: "sourceMissing" as const } : {}),
        contentStatus: {
          ...current.contentStatus,
          ...(sourceMissing ? { freshness: "sourceMissing" as const } : {}),
          liveState: liveStateFromSyncState(liveStateEvent?.state),
        },
      };
    });
  }, [liveStateEvent, liveStateEventSequence]);
  const eventSequence = Math.max(signals[sessionId] ?? 0, resyncSequence);
  useEffect(() => {
    if (eventSequence === 0 || eventSequence <= handledSignal.current) return;
    handledSignal.current = eventSequence;
    if (refreshTimer.current !== undefined)
      window.clearTimeout(refreshTimer.current);
    refreshTimer.current = window.setTimeout(() => {
      refreshTimer.current = undefined;
      const resync = resyncSequence === eventSequence;
      if (viewport.current.atBottom) void replacePage("bottom");
      else if (resync && viewport.current.anchorId)
        void replacePage("around", viewport.current.anchorId);
      else {
        setNewCount((value) => value + 1);
        pendingTailSequenceRef.current = eventSequence;
        setPendingTailSequence(eventSequence);
      }
    }, 100);
    return () => {
      if (refreshTimer.current !== undefined) {
        window.clearTimeout(refreshTimer.current);
        refreshTimer.current = undefined;
      }
    };
  }, [eventSequence, replacePage, resyncSequence]);

  const stopLiveSync = useCallback(() => {
    const controller = liveController.current;
    liveController.current = undefined;
    controller?.abort();
    setLiveConnection("inactive");
    setLiveError("");
  }, []);

  const startLiveSync = useCallback(() => {
    if (liveController.current) return;
    const controller = new AbortController();
    liveController.current = controller;
    setLiveConnection("starting");
    setLiveError("");
    void api
      .liveSync(sessionId, controller.signal, () => {
        if (liveController.current === controller)
          setLiveConnection("active");
      })
      .then(() => {
        if (
          liveController.current === controller &&
          !controller.signal.aborted
        ) {
          liveController.current = undefined;
          setLiveConnection("failed");
          setLiveError(t("liveSyncEnded"));
        }
      })
      .catch((failure) => {
        if (
          liveController.current === controller &&
          !controller.signal.aborted
        ) {
          liveController.current = undefined;
          setLiveConnection("failed");
          setLiveError(message(failure));
        }
      });
  }, [sessionId, t]);

  const loadOlder = useCallback(() => {
    const cursor = previousCursor;
    if (!cursor) return;
    const key = `older:${cursor}`;
    const existing = loadingPages.current.get(key);
    if (existing) return existing.promise;
    const token = Symbol(key);
    const epoch = pageEpoch.current;
    const promise = (async () => {
      try {
        const page = await api.entries(sessionId, {
          cursor,
          limit: 100,
          displayTypes: serializedConversationDisplayTypes,
        });
        if (epoch !== pageEpoch.current) return;
        setEntries((current) => mergeEntries(page.data, current));
        setPreviousCursor(page.previousCursor);
        setError("");
      } catch (f) {
        if (epoch === pageEpoch.current) setError(message(f));
      } finally {
        if (loadingPages.current.get(key)?.token === token)
          loadingPages.current.delete(key);
      }
    })();
    loadingPages.current.set(key, { token, promise });
    return promise;
  }, [previousCursor, serializedConversationDisplayTypes, sessionId]);

  const loadNewer = useCallback(
    (background = false) => {
      const cursor = nextCursor;
      const anchor = entries.at(-1)?.id;
      const pendingAtStart = pendingTailSequence;
      if (!cursor && (pendingAtStart === undefined || !anchor)) return;
      const key = cursor ? `newer:${cursor}` : `live-tail:${anchor}`;
      const existing = loadingPages.current.get(key);
      if (existing) return existing.promise;
      const token = Symbol(key);
      const epoch = pageEpoch.current;
      const promise = (async () => {
        try {
          const page = await api.entries(sessionId, {
            ...(cursor ? { cursor } : { aroundEntryId: anchor }),
            limit: 100,
            displayTypes: serializedConversationDisplayTypes,
          });
          if (epoch !== pageEpoch.current) return;
          setEntries((current) => mergeEntries(page.data, current));
          setNextCursor(page.nextCursor);
          if (
            pendingAtStart !== undefined &&
            pendingTailSequenceRef.current === pendingAtStart
          ) {
            pendingTailSequenceRef.current = undefined;
            setPendingTailSequence((current) =>
              current === pendingAtStart ? undefined : current,
            );
          }
          setError("");
        } catch (f) {
          if (!background && epoch === pageEpoch.current) setError(message(f));
        } finally {
          if (loadingPages.current.get(key)?.token === token)
            loadingPages.current.delete(key);
        }
      })();
      loadingPages.current.set(key, { token, promise });
      return promise;
    },
    [
      entries,
      nextCursor,
      pendingTailSequence,
      serializedConversationDisplayTypes,
      sessionId,
    ],
  );

  useEffect(() => {
    if (pendingTailSequence === undefined) return;
    void loadNewer(true);
  }, [loadNewer, pendingTailSequence]);

  const updateViewport = useCallback((next: ViewportState) => {
    viewport.current = next;
    if (next.atBottom) setNewCount(0);
  }, []);
  const ownsLiveSync =
    liveConnection === "starting" || liveConnection === "active";
  return (
    <>
      {session && (
        <div className="conversation-head">
          <div className="conversation-title-row">
            <h1>{localizedTitle(session)}</h1>
            <Button
              type="button"
              size="sm"
              variant={ownsLiveSync ? "default" : "outline"}
              aria-pressed={ownsLiveSync}
              disabled={
                session.contentStatus.freshness === "sourceMissing" &&
                !ownsLiveSync
              }
              onClick={ownsLiveSync ? stopLiveSync : startLiveSync}
            >
              {ownsLiveSync ? <Square size={14} /> : <Radio size={15} />}
              {t(ownsLiveSync ? "stopLiveSync" : "startLiveSync")}
            </Button>
          </div>
          {session.cwd && (
            <div className="conversation-cwd" title={session.cwd}>
              {session.cwd}
            </div>
          )}
          <div
            className="conversation-source-path"
            title={session.sourceLocation.relativePath}
          >
            {t(`root_${session.sourceLocation.rootKind}`)} ·{" "}
            {session.sourceLocation.relativePath}
          </div>
          <div className="muted">
            {sourceLabel(session.source, t)} ·{" "}
            {t("entryCount", { count: session.entryCount })} ·{" "}
            {session.completeness}
            {session.contentStatus.freshness !== "current" && (
              <Badge variant="outline" className="session-freshness">
                {t(`content_${session.contentStatus.freshness}`)}
              </Badge>
            )}
            {session.contentStatus.liveState !== "inactive" && (
              <Badge variant="outline" className="session-freshness">
                {t(`live_${session.contentStatus.liveState}`)}
              </Badge>
            )}
          </div>
          {liveError && (
            <div className="conversation-live-error" role="alert">
              {liveError}
            </div>
          )}
        </div>
      )}
      {error ? (
        <Empty text={error} />
      ) : entries.length === 0 ? (
        session && !session.contentStatus.hasSnapshot ? (
          <div className="catalog-only-empty" role="status">
            <p>{t("snapshotNotLoaded")}</p>
            {session.firstUserMessage && (
              <blockquote>{session.firstUserMessage.text}</blockquote>
            )}
            <p className="muted">{t("startLiveSyncHelp")}</p>
          </div>
        ) : (
          <Empty text={t("noEntries")} />
        )
      ) : (
        <VirtualTranscript
          entries={entries}
          around={around}
          hasOlder={Boolean(previousCursor)}
          hasNewer={Boolean(nextCursor) || pendingTailSequence !== undefined}
          newCount={newCount}
          scrollTarget={scrollTarget}
          onInspect={(id) => onInspect(sessionId, id)}
          onLoadOlder={loadOlder}
          onLoadNewer={() => loadNewer()}
          onJumpTop={() => replacePage("top")}
          onJumpBottom={() => replacePage("bottom")}
          onViewportChange={updateViewport}
        />
      )}
    </>
  );
}
