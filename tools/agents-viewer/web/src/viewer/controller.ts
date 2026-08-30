import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";
import { usePanelRef } from "@/components/ui/resizable";
import type { SessionGroup, SessionSyncState, Status } from "@/generated/api";
import { api, subscribeEvents } from "@/lib/api";
import { setLanguage } from "@/lib/i18n";
import { message } from "@/viewer/format";
import {
  CONVERSATION_DISPLAY_STORAGE_KEY,
  INSPECTOR_DEFAULT_WIDTH,
  SIDEBAR_MIN_WIDTH,
  canonicalConversationDisplayTypes,
  sameConversationDisplayTypes,
  storedConversationDisplayTypes,
  storedSidebarWidth,
  storedTheme,
  type ConversationDisplayType,
  type ThemeValue,
} from "@/viewer/preferences";
import type { SettingsValues } from "@/viewer/settings";

export function shouldOpenSearch(
  event: Pick<KeyboardEvent, "ctrlKey" | "shiftKey" | "metaKey" | "key">,
  ctrlShiftFEnabled: boolean,
  inputFocused: boolean,
) {
  return (
    (ctrlShiftFEnabled &&
      event.ctrlKey &&
      event.shiftKey &&
      !event.metaKey &&
      event.key.toLowerCase() === "f") ||
    ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") ||
    (event.key === "/" && !inputFocused)
  );
}

export function useViewerController() {
  const { t, i18n } = useTranslation();
  const [sessionGroups, setSessionGroups] = useState<SessionGroup[]>([]);
  const [sessionNextCursor, setSessionNextCursor] = useState<string>();
  const [loadingMoreSessions, setLoadingMoreSessions] = useState(false);
  const [status, setStatus] = useState<Status>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [archived, setArchived] = useState<"exclude" | "include" | "only">(
    "exclude",
  );
  const [source, setSource] = useState("");
  const [cwd, setCwd] = useState("");
  const [navOpen, setNavOpen] = useState(false);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [conversationDisplayTypes, setConversationDisplayTypes] = useState(
    storedConversationDisplayTypes,
  );
  const [forcedConversationDisplayType, setForcedConversationDisplayType] =
    useState<ConversationDisplayType>();
  const [theme, setTheme] = useState<ThemeValue>(storedTheme);
  const [searchCtrlShiftF, setSearchCtrlShiftF] = useState(
    () => localStorage.getItem("agents-viewer-search-ctrl-shift-f") === "true",
  );
  const [sidebarWidth] = useState(storedSidebarWidth);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(
    () => localStorage.getItem("agents-viewer-sidebar-collapsed") === "true",
  );
  const [selectedEntry, setSelectedEntry] = useState<{
    sessionId: string;
    entryId: string;
  }>();
  const [compactInspector, setCompactInspector] = useState(
    () => matchMedia("(max-width:1199px)").matches,
  );
  const [compactNavigation, setCompactNavigation] = useState(
    () => matchMedia("(max-width:767px)").matches,
  );
  const [conversationSignals, setConversationSignals] = useState<
    Record<string, number>
  >({});
  const [liveStateSignals, setLiveStateSignals] = useState<
    Record<string, { sequence: number; state?: SessionSyncState }>
  >({});
  const [resyncSequence, setResyncSequence] = useState(0);
  const searchReturnFocus = useRef<HTMLElement | null>(null);
  const inspectorReturnFocus = useRef<HTMLElement | null>(null);
  const sessionRequest = useRef(0);
  const loadedSessionRootCount = useRef(200);
  const loadSessionsRef = useRef<(signal?: AbortSignal) => Promise<void>>(
    async () => {},
  );
  const sessionRefreshTimer = useRef<number | undefined>(undefined);
  const liveSequence = useRef(0);
  const sidebarPanelRef = usePanelRef();
  const inspectorPanelRef = usePanelRef();
  const sidebarWidthRef = useRef(sidebarWidth);
  const sidebarCollapsedRef = useRef(sidebarCollapsed);
  const inspectorWidthRef = useRef(INSPECTOR_DEFAULT_WIDTH);
  const navigate = useNavigate();
  const location = useLocation();
  const openSearch = useCallback(() => {
    searchReturnFocus.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    setSearchOpen(true);
  }, []);
  const closeSearch = useCallback(() => {
    setSearchOpen(false);
    requestAnimationFrame(() => searchReturnFocus.current?.focus());
  }, []);
  const loadSessions = useCallback(
    async (signal?: AbortSignal) => {
      const request = ++sessionRequest.current;
      try {
        const target = Math.max(200, loadedSessionRootCount.current);
        const groups: SessionGroup[] = [];
        let cursor: string | undefined;
        do {
          const page = await api.sessionGroups(
            {
              archived,
              source: source || undefined,
              cwd: cwd || undefined,
              cursor,
              limit: Math.min(200, target - groups.length),
            },
            signal,
          );
          groups.push(...page.data);
          cursor = page.nextCursor;
          if (page.data.length === 0) break;
        } while (cursor && groups.length < target);
        if (request === sessionRequest.current) {
          loadedSessionRootCount.current = groups.length;
          setSessionGroups(groups);
          setSessionNextCursor(cursor);
          setError("");
        }
      } catch (failure) {
        if (
          request === sessionRequest.current &&
          !(failure instanceof DOMException)
        )
          setError(message(failure));
      } finally {
        if (request === sessionRequest.current) {
          setLoading(false);
          setLoadingMoreSessions(false);
        }
      }
    },
    [archived, cwd, source],
  );
  const loadMoreSessions = useCallback(async () => {
    if (!sessionNextCursor || loadingMoreSessions) return;
    const request = ++sessionRequest.current;
    setLoadingMoreSessions(true);
    try {
      const page = await api.sessionGroups({
        archived,
        source: source || undefined,
        cwd: cwd || undefined,
        cursor: sessionNextCursor,
        limit: 200,
      });
      if (request === sessionRequest.current) {
        setSessionGroups((current) => {
          const seen = new Set(current.map((group) => group.root.session.id));
          const merged = [
            ...current,
            ...page.data.filter((group) => !seen.has(group.root.session.id)),
          ];
          loadedSessionRootCount.current = merged.length;
          return merged;
        });
        setSessionNextCursor(page.nextCursor);
        setError("");
      }
    } catch (failure) {
      if (
        request === sessionRequest.current &&
        !(failure instanceof DOMException)
      )
        setError(message(failure));
    } finally {
      if (request === sessionRequest.current) setLoadingMoreSessions(false);
    }
  }, [archived, cwd, loadingMoreSessions, sessionNextCursor, source]);
  useEffect(() => {
    loadSessionsRef.current = loadSessions;
  }, [loadSessions]);
  const scheduleSessionRefresh = useCallback(() => {
    if (sessionRefreshTimer.current !== undefined) return;
    sessionRefreshTimer.current = window.setTimeout(() => {
      sessionRefreshTimer.current = undefined;
      void loadSessionsRef.current();
    }, 100);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    loadedSessionRootCount.current = 200;
    setLoading(true);
    void loadSessions(controller.signal);
    return () => controller.abort();
  }, [loadSessions]);
  useEffect(() => {
    const controller = new AbortController();
    api
      .status(controller.signal)
      .then(setStatus)
      .catch(() => {});
    return () => controller.abort();
  }, []);
  useEffect(
    () => () => {
      if (sessionRefreshTimer.current !== undefined)
        window.clearTimeout(sessionRefreshTimer.current);
    },
    [],
  );
  useEffect(
    () =>
      subscribeEvents(
        (event) => {
          if (
            event.type === "catalogProgress" &&
            event.data.phase &&
            event.data.progress
          ) {
            setStatus((current) =>
              current
                ? {
                    ...current,
                    generation: event.data.generation,
                    phase: event.data.phase!,
                    progress: event.data.progress!,
                  }
                : current,
            );
            return;
          }
          if (event.type === "catalogUpdated") {
            scheduleSessionRefresh();
            return;
          }
          if (event.type === "snapshotUpdated" && event.data.sessionId) {
            const sequence = ++liveSequence.current;
            setConversationSignals((current) => ({
              ...current,
              [event.data.sessionId!]: sequence,
            }));
            scheduleSessionRefresh();
            return;
          }
          if (
            event.type === "liveSyncStateChanged" &&
            event.data.sessionId
          ) {
            const sequence = ++liveSequence.current;
            setLiveStateSignals((current) => ({
              ...current,
              [event.data.sessionId!]: {
                sequence,
                state: event.data.syncState,
              },
            }));
            scheduleSessionRefresh();
          }
        },
        () => {
          void api.status().then(setStatus);
          scheduleSessionRefresh();
          setResyncSequence(++liveSequence.current);
        },
      ),
    [scheduleSessionRefresh],
  );
  useEffect(() => {
    const media = matchMedia("(max-width:1199px)");
    const update = () => setCompactInspector(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    const media = matchMedia("(max-width:767px)");
    const update = () => setCompactNavigation(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    if (compactNavigation) return;
    sidebarCollapsedRef.current = sidebarCollapsed;
    const frame = requestAnimationFrame(() => {
      const panel = sidebarPanelRef.current;
      if (!panel) return;
      if (sidebarCollapsed) panel.collapse();
      else panel.resize(`${sidebarWidthRef.current}px`);
    });
    return () => cancelAnimationFrame(frame);
  }, [compactNavigation, sidebarCollapsed, sidebarPanelRef]);
  useEffect(() => {
    const panel = inspectorPanelRef.current;
    if (!panel) return;
    if (!inspectorOpen || compactInspector) panel.collapse();
    else panel.resize(`${inspectorWidthRef.current}px`);
  }, [compactInspector, inspectorOpen, inspectorPanelRef]);
  useEffect(() => {
    setInspectorOpen(false);
    setSelectedEntry(undefined);
    setForcedConversationDisplayType(undefined);
  }, [location.pathname]);
  useEffect(() => {
    const keys: string[] = [];
    const handler = (event: KeyboardEvent) => {
      const input =
        event.target instanceof HTMLInputElement ||
        event.target instanceof HTMLTextAreaElement;
      if (shouldOpenSearch(event, searchCtrlShiftF, input)) {
        event.preventDefault();
        openSearch();
      } else if (event.key === "Escape") {
        if (searchOpen) closeSearch();
        setNavOpen(false);
        setInspectorOpen(false);
      }
      keys.push(event.key);
      if (keys.length > 2) keys.shift();
      if (keys.join(" ") === "g g")
        document
          .querySelector<HTMLButtonElement>('[data-transcript-jump="top"]')
          ?.click();
      if (event.key === "G" && !input)
        document
          .querySelector<HTMLButtonElement>('[data-transcript-jump="bottom"]')
          ?.click();
      if ((event.key === "j" || event.key === "k") && !input) {
        const items = [
          ...document.querySelectorAll<HTMLElement>("[data-transcript-entry]"),
        ];
        const current = document.activeElement?.closest<HTMLElement>(
          "[data-transcript-entry]",
        );
        const index = current ? items.indexOf(current) : -1;
        const next =
          event.key === "j"
            ? Math.min(items.length - 1, index + 1)
            : Math.max(0, index < 0 ? 0 : index - 1);
        items[next]?.querySelector<HTMLElement>("button")?.focus();
      }
    };
    addEventListener("keydown", handler);
    return () => removeEventListener("keydown", handler);
  }, [closeSearch, openSearch, searchCtrlShiftF, searchOpen]);
  const changeTheme = useCallback((value: ThemeValue) => {
    setTheme(value);
    localStorage.setItem("agents-viewer-theme", value);
    document.documentElement.classList.toggle(
      "dark",
      value === "dark" ||
        (value === "system" &&
          matchMedia("(prefers-color-scheme:dark)").matches),
    );
  }, []);
  const closeInspector = useCallback(() => {
    setInspectorOpen(false);
    requestAnimationFrame(() => inspectorReturnFocus.current?.focus());
  }, []);
  const openInspector = useCallback(
    (selection?: { sessionId: string; entryId: string }) => {
      inspectorReturnFocus.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      if (selection) setSelectedEntry(selection);
      setInspectorOpen(true);
    },
    [],
  );
  const applySettings = useCallback(
    (next: SettingsValues) => {
      setArchived(next.archived);
      setSource(next.source);
      setCwd(next.cwd);
      const nextDisplayTypes = canonicalConversationDisplayTypes(
        next.conversationDisplayTypes,
      );
      setConversationDisplayTypes((current) =>
        sameConversationDisplayTypes(current, nextDisplayTypes)
          ? current
          : nextDisplayTypes,
      );
      localStorage.setItem(
        CONVERSATION_DISPLAY_STORAGE_KEY,
        JSON.stringify(nextDisplayTypes),
      );
      setSearchCtrlShiftF(next.searchCtrlShiftF);
      localStorage.setItem(
        "agents-viewer-search-ctrl-shift-f",
        String(next.searchCtrlShiftF),
      );
      changeTheme(next.theme);
      setLanguage(next.language);
    },
    [changeTheme],
  );
  const toggleSidebar = useCallback(() => {
    const panel = sidebarPanelRef.current;
    if (!panel) return;
    if (panel.isCollapsed()) {
      sidebarCollapsedRef.current = false;
      setSidebarCollapsed(false);
      localStorage.setItem("agents-viewer-sidebar-collapsed", "false");
      panel.resize(`${sidebarWidthRef.current}px`);
    } else {
      const width = panel.getSize().inPixels;
      if (width >= SIDEBAR_MIN_WIDTH) {
        sidebarWidthRef.current = width;
        localStorage.setItem("agents-viewer-sidebar-width", String(width));
      }
      sidebarCollapsedRef.current = true;
      panel.collapse();
      setSidebarCollapsed(true);
      localStorage.setItem("agents-viewer-sidebar-collapsed", "true");
    }
  }, [sidebarPanelRef]);
  return {
    t,
    i18n,
    sessionGroups,
    sessionNextCursor,
    loadingMoreSessions,
    status,
    loading,
    error,
    archived,
    source,
    cwd,
    navOpen,
    setNavOpen,
    inspectorOpen,
    setInspectorOpen,
    searchOpen,
    conversationDisplayTypes,
    forcedConversationDisplayType,
    setForcedConversationDisplayType,
    theme,
    searchCtrlShiftF,
    sidebarWidth,
    sidebarCollapsed,
    setSidebarCollapsed,
    selectedEntry,
    compactInspector,
    compactNavigation,
    conversationSignals,
    liveStateSignals,
    resyncSequence,
    sidebarPanelRef,
    inspectorPanelRef,
    sidebarWidthRef,
    sidebarCollapsedRef,
    inspectorWidthRef,
    navigate,
    openSearch,
    closeSearch,
    closeInspector,
    openInspector,
    applySettings,
    toggleSidebar,
    loadMoreSessions,
  };
}
