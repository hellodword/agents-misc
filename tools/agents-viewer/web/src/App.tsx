import { useVirtualizer } from "@tanstack/react-virtual";
import {
  ArrowDownToLine,
  ArrowUpToLine,
  Check,
  Copy,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRight,
  Search,
  Settings as SettingsIcon,
  X,
} from "lucide-react";
import {
  createContext,
  isValidElement,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ComponentProps,
  type FormEvent,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type ExtraProps } from "react-markdown";
import {
  Link,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import rehypeHighlight from "rehype-highlight";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  CommandDialog,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
  usePanelRef,
} from "@/components/ui/resizable";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type {
  ContentChunk,
  EntryListItem,
  RawRecord,
  SearchHit,
  SessionGroup,
  SessionSummary,
  SessionTreeNode,
  SourceKind,
  Status,
  TranscriptEntry,
} from "@/generated/api";
import { api, ApiClientError, subscribeEvents } from "@/lib/api";
import i18n, {
  preferredLanguage,
  setLanguage,
  type SupportedLanguage,
} from "@/lib/i18n";

type ThemeValue = "light" | "dark" | "system";

export type ConversationDisplayType =
  | "received"
  | "sent"
  | "requestUserInput"
  | "reasoning"
  | "exec"
  | "plan"
  | "patch"
  | "mcp"
  | "webSearch"
  | "function"
  | "dynamic"
  | "terminal"
  | "viewImage"
  | "otherTool"
  | "warning"
  | "error"
  | "context"
  | "marker"
  | "technicalMessage"
  | "internalMessage"
  | "unknown";

const CONVERSATION_DISPLAY_OPTIONS: readonly {
  value: ConversationDisplayType;
  labelKey: string;
}[] = [
  { value: "received", labelKey: "displayReceived" },
  { value: "sent", labelKey: "displaySent" },
  { value: "requestUserInput", labelKey: "displayRequestUserInput" },
  { value: "reasoning", labelKey: "displayReasoning" },
  { value: "exec", labelKey: "displayExec" },
  { value: "plan", labelKey: "displayPlan" },
  { value: "patch", labelKey: "displayPatch" },
  { value: "mcp", labelKey: "displayMcp" },
  { value: "webSearch", labelKey: "displayWebSearch" },
  { value: "function", labelKey: "displayFunction" },
  { value: "dynamic", labelKey: "displayDynamic" },
  { value: "terminal", labelKey: "displayTerminal" },
  { value: "viewImage", labelKey: "displayViewImage" },
  { value: "otherTool", labelKey: "displayOtherTool" },
  { value: "warning", labelKey: "displayWarning" },
  { value: "error", labelKey: "displayError" },
  { value: "context", labelKey: "displayContext" },
  { value: "marker", labelKey: "displayMarker" },
  { value: "technicalMessage", labelKey: "displayTechnicalMessage" },
  { value: "internalMessage", labelKey: "displayInternalMessage" },
  { value: "unknown", labelKey: "displayUnknown" },
];
const REQUIRED_CONVERSATION_DISPLAY_TYPES: readonly ConversationDisplayType[] =
  ["received", "sent", "requestUserInput"];
export const DEFAULT_CONVERSATION_DISPLAY_TYPES: readonly ConversationDisplayType[] =
  [...REQUIRED_CONVERSATION_DISPLAY_TYPES, "reasoning", "exec"];
const CONVERSATION_DISPLAY_STORAGE_KEY =
  "agents-viewer-conversation-display-types";
const conversationDisplayTypeSet = new Set(
  CONVERSATION_DISPLAY_OPTIONS.map(({ value }) => value),
);
const requiredConversationDisplayTypeSet = new Set(
  REQUIRED_CONVERSATION_DISPLAY_TYPES,
);

function canonicalConversationDisplayTypes(
  values: readonly ConversationDisplayType[],
) {
  const selected = new Set<ConversationDisplayType>([
    ...REQUIRED_CONVERSATION_DISPLAY_TYPES,
    ...values,
  ]);
  return CONVERSATION_DISPLAY_OPTIONS.map(({ value }) => value).filter(
    (value) => selected.has(value),
  );
}

function storedConversationDisplayTypes() {
  const stored = localStorage.getItem(CONVERSATION_DISPLAY_STORAGE_KEY);
  if (stored === null) return [...DEFAULT_CONVERSATION_DISPLAY_TYPES];
  try {
    const values = JSON.parse(stored) as unknown;
    if (
      !Array.isArray(values) ||
      !values.every(
        (value): value is ConversationDisplayType =>
          typeof value === "string" &&
          conversationDisplayTypeSet.has(value as ConversationDisplayType),
      )
    )
      return [...DEFAULT_CONVERSATION_DISPLAY_TYPES];
    return canonicalConversationDisplayTypes(values);
  } catch {
    return [...DEFAULT_CONVERSATION_DISPLAY_TYPES];
  }
}

function sameConversationDisplayTypes(
  left: readonly ConversationDisplayType[],
  right: readonly ConversationDisplayType[],
) {
  const canonicalLeft = canonicalConversationDisplayTypes(left);
  const canonicalRight = canonicalConversationDisplayTypes(right);
  return (
    canonicalLeft.length === canonicalRight.length &&
    canonicalLeft.every((value, index) => value === canonicalRight[index])
  );
}

function withConversationDisplayType(
  values: readonly ConversationDisplayType[],
  value?: ConversationDisplayType,
) {
  return canonicalConversationDisplayTypes(value ? [...values, value] : values);
}

const SIDEBAR_DEFAULT_WIDTH = 300;
const SIDEBAR_MIN_WIDTH = 240;
const SIDEBAR_MAX_WIDTH = 480;
const INSPECTOR_DEFAULT_WIDTH = 360;
const INSPECTOR_MIN_WIDTH = 300;
const INSPECTOR_MAX_WIDTH = 600;

function storedTheme(): ThemeValue {
  const value = localStorage.getItem("agents-viewer-theme");
  return value === "light" || value === "dark" || value === "system"
    ? value
    : "system";
}

function storedSidebarWidth() {
  const value = Number(localStorage.getItem("agents-viewer-sidebar-width"));
  return Number.isFinite(value) && value >= SIDEBAR_MIN_WIDTH
    ? Math.min(SIDEBAR_MAX_WIDTH, value)
    : SIDEBAR_DEFAULT_WIDTH;
}

export function App() {
  const { t, i18n } = useTranslation();
  const [sessionGroups, setSessionGroups] = useState<SessionGroup[]>([]);
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
  const [resyncSequence, setResyncSequence] = useState(0);
  const searchReturnFocus = useRef<HTMLElement | null>(null);
  const inspectorReturnFocus = useRef<HTMLElement | null>(null);
  const sessionRequest = useRef(0);
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
        const page = await api.sessionGroups(
          {
            archived,
            source: source || undefined,
            cwd: cwd || undefined,
            limit: 200,
          },
          signal,
        );
        if (request === sessionRequest.current) {
          setSessionGroups(page.data);
          setError("");
        }
      } catch (failure) {
        if (
          request === sessionRequest.current &&
          !(failure instanceof DOMException)
        )
          setError(message(failure));
      } finally {
        if (request === sessionRequest.current) setLoading(false);
      }
    },
    [archived, cwd, source],
  );
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
            event.type === "indexProgress" &&
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
          if (event.type === "sessionUpdated") {
            scheduleSessionRefresh();
            return;
          }
          if (event.type === "entryUpdated" && event.data.sessionId) {
            const sequence = ++liveSequence.current;
            setConversationSignals((current) => ({
              ...current,
              [event.data.sessionId!]: sequence,
            }));
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
      if (
        searchCtrlShiftF &&
        event.ctrlKey &&
        event.shiftKey &&
        !event.metaKey &&
        event.key.toLowerCase() === "f"
      ) {
        event.preventDefault();
        openSearch();
      } else if (
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === "k"
      ) {
        event.preventDefault();
        openSearch();
      } else if (event.key === "/" && !input) {
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
  const sidebar = (
    <SessionSidebar
      groups={sessionGroups}
      loading={loading}
      error={error}
      onNavigate={() => setNavOpen(false)}
    />
  );
  return (
    <TooltipProvider>
      <div className="app">
        <a className="skip" href="#main-content">
          {t("skip")}
        </a>
        <header className="topbar">
          <Button
            variant="outline"
            size="icon"
            className="mobile-only"
            aria-label={t("openNavigation")}
            onClick={() => setNavOpen(true)}
          >
            <Menu size={17} />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="desktop-sidebar-toggle"
            aria-label={
              sidebarCollapsed ? t("expandNavigation") : t("collapseNavigation")
            }
            aria-expanded={!sidebarCollapsed}
            aria-controls="sessions-panel"
            onClick={toggleSidebar}
          >
            {sidebarCollapsed ? (
              <PanelLeftOpen size={17} />
            ) : (
              <PanelLeftClose size={17} />
            )}
          </Button>
          <span className="brand">{t("appName")}</span>
          <span className="top-spacer" />
          {status && (
            <>
              <span
                className="sr-only"
                role="status"
                aria-live="polite"
                aria-atomic="true"
              >
                {indexStatusLabel(status, t)}
              </span>
              <div className="index-live">
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span tabIndex={0}>
                      <Badge
                        variant={
                          status.phase === "degraded"
                            ? "destructive"
                            : "outline"
                        }
                      >
                        {indexStatusLabel(status, t)}
                      </Badge>
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>
                    {indexWindowLabel(status, t)} ·{" "}
                    {t("indexCutoff", {
                      cutoff: status.initialIndexCutoff
                        ? new Date(status.initialIndexCutoff).toLocaleString()
                        : t("none"),
                      bytes: formatBytes(status.progress.excludedBytes),
                    })}
                  </TooltipContent>
                </Tooltip>
              </div>
            </>
          )}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="outline"
                size="sm"
                aria-label={t("search")}
                onClick={openSearch}
              >
                <Search size={15} />{" "}
                <span className="desktop-only">{t("search")}</span>
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t("search")}</TooltipContent>
          </Tooltip>
          <SettingsControl
            archived={archived}
            source={source}
            cwd={cwd}
            conversationDisplayTypes={conversationDisplayTypes}
            forcedConversationDisplayType={forcedConversationDisplayType}
            theme={theme}
            language={i18n.language.startsWith("zh") ? "zh-CN" : "en"}
            searchCtrlShiftF={searchCtrlShiftF}
            onApply={applySettings}
          />
        </header>
        {status &&
          (status.phase === "discovering" || status.phase === "indexing") && (
            <Progress
              aria-label={indexStatusLabel(status, t)}
              value={indexPercent(status)}
              className={`index-progress ${status.phase === "discovering" ? "indeterminate" : ""}`}
            />
          )}
        <ResizablePanelGroup
          id="viewer-layout"
          orientation="horizontal"
          className="layout"
        >
          <ResizablePanel
            id="sessions-panel"
            panelRef={sidebarPanelRef}
            defaultSize={sidebarCollapsed ? "0px" : `${sidebarWidth}px`}
            minSize={`${SIDEBAR_MIN_WIDTH}px`}
            maxSize={`${SIDEBAR_MAX_WIDTH}px`}
            collapsedSize="0px"
            collapsible
            groupResizeBehavior="preserve-pixel-size"
            onResize={(size) => {
              if (size.inPixels >= SIDEBAR_MIN_WIDTH) {
                const width = Math.round(size.inPixels);
                sidebarWidthRef.current = width;
                if (!compactNavigation) {
                  sidebarCollapsedRef.current = false;
                  setSidebarCollapsed(false);
                  localStorage.setItem(
                    "agents-viewer-sidebar-width",
                    String(width),
                  );
                  localStorage.setItem(
                    "agents-viewer-sidebar-collapsed",
                    "false",
                  );
                }
              } else if (!compactNavigation && !sidebarCollapsedRef.current) {
                requestAnimationFrame(() => {
                  if (!sidebarCollapsedRef.current)
                    sidebarPanelRef.current?.resize(
                      `${sidebarWidthRef.current}px`,
                    );
                });
              }
            }}
            className="sidebar"
          >
            <ScrollArea className="h-full">
              <aside aria-label={t("sessions")}>{sidebar}</aside>
            </ScrollArea>
          </ResizablePanel>
          <ResizableHandle
            withHandle
            disabled={compactNavigation || sidebarCollapsed}
            className={`sidebar-handle ${compactNavigation || sidebarCollapsed ? "panel-handle-hidden" : ""}`}
          />
          <ResizablePanel
            id="conversation-panel"
            minSize="480px"
            className="main-panel"
          >
            <main id="main-content" className="main">
              <Routes>
                <Route
                  path="/"
                  element={
                    loading ? (
                      <Empty text={t("loading")} />
                    ) : sessionGroups[0] ? (
                      <Navigate
                        replace
                        to={`/sessions/${sessionGroups[0].latestSessionId}`}
                      />
                    ) : (
                      <Empty text={t("noSessions")} />
                    )
                  }
                />
                <Route
                  path="/sessions/:sessionId"
                  element={
                    <Conversation
                      signals={conversationSignals}
                      resyncSequence={resyncSequence}
                      conversationDisplayTypes={conversationDisplayTypes}
                      onForceConversationDisplayType={
                        setForcedConversationDisplayType
                      }
                      onInspect={(sessionId, entryId) =>
                        openInspector({ sessionId, entryId })
                      }
                    />
                  }
                />
                <Route path="/search" element={<SearchPage />} />
                <Route path="*" element={<Navigate replace to="/" />} />
              </Routes>
            </main>
          </ResizablePanel>
          <ResizableHandle
            withHandle
            disabled={!inspectorOpen || compactInspector}
            className={`inspector-handle ${!inspectorOpen || compactInspector ? "panel-handle-hidden" : ""}`}
          />
          <ResizablePanel
            id="inspector-panel"
            panelRef={inspectorPanelRef}
            defaultSize="0px"
            minSize={`${INSPECTOR_MIN_WIDTH}px`}
            maxSize={`${INSPECTOR_MAX_WIDTH}px`}
            collapsedSize="0px"
            collapsible
            groupResizeBehavior="preserve-pixel-size"
            onResize={(size) => {
              if (size.inPixels >= INSPECTOR_MIN_WIDTH) {
                inspectorWidthRef.current = Math.round(size.inPixels);
              } else if (inspectorOpen && !compactInspector) {
                requestAnimationFrame(() => {
                  if (inspectorOpen && !compactInspector)
                    inspectorPanelRef.current?.resize(
                      `${inspectorWidthRef.current}px`,
                    );
                });
              }
            }}
            className="inspector"
          >
            {inspectorOpen && !compactInspector && (
              <ScrollArea className="h-full">
                <aside id="entry-inspector" aria-label={t("inspector")}>
                  <Inspector
                    selected={selectedEntry}
                    onClose={closeInspector}
                  />
                </aside>
              </ScrollArea>
            )}
          </ResizablePanel>
        </ResizablePanelGroup>
        <Sheet open={navOpen} onOpenChange={setNavOpen}>
          <SheetContent side="left" className="mobile-sheet">
            <SheetTitle className="sr-only">{t("sessions")}</SheetTitle>
            <SheetDescription className="sr-only">
              {t("openNavigation")}
            </SheetDescription>
            {sidebar}
          </SheetContent>
        </Sheet>
        <Sheet
          open={inspectorOpen && compactInspector}
          onOpenChange={(open) =>
            open ? setInspectorOpen(true) : closeInspector()
          }
        >
          <SheetContent
            id="entry-inspector"
            side="right"
            className="mobile-sheet"
          >
            <SheetTitle className="sr-only">{t("inspector")}</SheetTitle>
            <SheetDescription className="sr-only">
              {t("openInspector")}
            </SheetDescription>
            <Inspector selected={selectedEntry} />
          </SheetContent>
        </Sheet>
        {searchOpen && (
          <SearchDialog
            onClose={closeSearch}
            onOpen={(hit) => {
              closeSearch();
              navigate(
                `/sessions/${hit.session.id}?entry=${encodeURIComponent(hit.entryId)}`,
              );
            }}
          />
        )}
      </div>
    </TooltipProvider>
  );
}

const sourceValues: SourceKind[] = [
  "cli",
  "vscode",
  "exec",
  "review",
  "subagent",
  "appServer",
  "unknown",
];

type FilterValues = {
  archived: "exclude" | "include" | "only";
  source: string;
  cwd: string;
  conversationDisplayTypes: ConversationDisplayType[];
};

type SettingsValues = FilterValues & {
  theme: ThemeValue;
  language: SupportedLanguage;
  searchCtrlShiftF: boolean;
};

function SettingsControl(
  props: SettingsValues & {
    forcedConversationDisplayType?: ConversationDisplayType;
    onApply: (values: SettingsValues) => void;
  },
) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<SettingsValues>({
    ...props,
    conversationDisplayTypes: [...props.conversationDisplayTypes],
  });
  const activeCount =
    Number(Boolean(props.source)) +
    Number(Boolean(props.cwd)) +
    Number(props.archived !== "exclude") +
    Number(
      !sameConversationDisplayTypes(
        props.conversationDisplayTypes,
        DEFAULT_CONVERSATION_DISPLAY_TYPES,
      ),
    );
  const changeOpen = (next: boolean) => {
    if (next)
      setDraft({
        archived: props.archived,
        source: props.source,
        cwd: props.cwd,
        conversationDisplayTypes: [...props.conversationDisplayTypes],
        theme: props.theme,
        language: props.language,
        searchCtrlShiftF: props.searchCtrlShiftF,
      });
    setOpen(next);
  };
  const apply = (event: FormEvent) => {
    event.preventDefault();
    props.onApply(draft);
    setOpen(false);
  };
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <Tooltip>
        <TooltipTrigger asChild>
          <DialogTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              aria-label={
                activeCount
                  ? t("settingsActive", { count: activeCount })
                  : t("settings")
              }
            >
              <SettingsIcon size={15} />
              <span className="desktop-only">{t("settings")}</span>
              {activeCount > 0 && (
                <span className="settings-count" aria-hidden="true">
                  {activeCount}
                </span>
              )}
            </Button>
          </DialogTrigger>
        </TooltipTrigger>
        <TooltipContent>
          {activeCount
            ? t("settingsActive", { count: activeCount })
            : t("settings")}
        </TooltipContent>
      </Tooltip>
      <DialogContent className="settings-dialog">
        <DialogHeader>
          <DialogTitle>{t("settings")}</DialogTitle>
          <DialogDescription>{t("settingsHelp")}</DialogDescription>
        </DialogHeader>
        <form className="settings-form" onSubmit={apply}>
          <fieldset>
            <legend>{t("sessionFilters")}</legend>
            <label htmlFor="source-filter">{t("source")}</label>
            <select
              id="source-filter"
              className="select"
              value={draft.source}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  source: event.target.value,
                }))
              }
            >
              <option value="">{t("allSources")}</option>
              {sourceValues.map((value) => (
                <option key={value} value={value}>
                  {sourceLabel(value, t)}
                </option>
              ))}
            </select>
            <details className="source-help">
              <summary>{t("sourceHelp")}</summary>
              <dl>
                {sourceValues.map((value) => (
                  <div key={value}>
                    <dt>{sourceLabel(value, t)}</dt>
                    <dd>{sourceHelp(value, t)}</dd>
                  </div>
                ))}
              </dl>
            </details>
            <label htmlFor="cwd-filter">{t("cwd")}</label>
            <Input
              id="cwd-filter"
              value={draft.cwd}
              onChange={(event) =>
                setDraft((current) => ({ ...current, cwd: event.target.value }))
              }
              placeholder={t("cwdPlaceholder")}
            />
            <label htmlFor="archive-filter">{t("archived")}</label>
            <select
              id="archive-filter"
              className="select"
              value={draft.archived}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  archived: event.target.value as FilterValues["archived"],
                }))
              }
            >
              <option value="exclude">{t("archiveActive")}</option>
              <option value="include">{t("archiveInclude")}</option>
              <option value="only">{t("archiveOnly")}</option>
            </select>
            <p className="settings-help">{t("archiveHelp")}</p>
          </fieldset>
          <fieldset>
            <legend>{t("conversationDisplay")}</legend>
            <p className="settings-help">{t("conversationDisplayHelp")}</p>
            <div className="conversation-display-types">
              {CONVERSATION_DISPLAY_OPTIONS.map(({ value, labelKey }) => {
                const required = requiredConversationDisplayTypeSet.has(value);
                return (
                  <label
                    className={`conversation-display-type ${required ? "conversation-display-type-required" : ""}`}
                    key={value}
                  >
                    <input
                      type="checkbox"
                      checked={draft.conversationDisplayTypes.includes(value)}
                      disabled={required}
                      onChange={(event) =>
                        setDraft((current) => ({
                          ...current,
                          conversationDisplayTypes:
                            canonicalConversationDisplayTypes(
                              event.target.checked
                                ? [...current.conversationDisplayTypes, value]
                                : current.conversationDisplayTypes.filter(
                                    (candidate) => candidate !== value,
                                  ),
                            ),
                        }))
                      }
                    />
                    <span>{t(labelKey)}</span>
                  </label>
                );
              })}
            </div>
            <p className="settings-help">{t("requiredDisplayTypesHelp")}</p>
            {props.forcedConversationDisplayType && (
              <p className="settings-help forced-display-type" role="status">
                {t("displayTypeForced", {
                  type: t(
                    CONVERSATION_DISPLAY_OPTIONS.find(
                      ({ value }) =>
                        value === props.forcedConversationDisplayType,
                    )?.labelKey ?? "displayUnknown",
                  ),
                })}
              </p>
            )}
          </fieldset>
          <fieldset>
            <legend>{t("appearance")}</legend>
            <label htmlFor="language-setting">{t("language")}</label>
            <select
              id="language-setting"
              className="select"
              value={draft.language}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  language: event.target.value as SupportedLanguage,
                }))
              }
            >
              <option value="en">{t("english")}</option>
              <option value="zh-CN">{t("chinese")}</option>
            </select>
            <label htmlFor="theme-setting">{t("theme")}</label>
            <select
              id="theme-setting"
              className="select"
              value={draft.theme}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  theme: event.target.value as ThemeValue,
                }))
              }
            >
              <option value="system">{t("system")}</option>
              <option value="light">{t("light")}</option>
              <option value="dark">{t("dark")}</option>
            </select>
          </fieldset>
          <fieldset>
            <legend>{t("keyboard")}</legend>
            <label className="technical-filter" htmlFor="search-shortcut">
              <input
                id="search-shortcut"
                type="checkbox"
                checked={draft.searchCtrlShiftF}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    searchCtrlShiftF: event.target.checked,
                  }))
                }
              />
              <span>
                <strong>{t("searchShortcut")}</strong>
                <small>{t("searchShortcutHelp")}</small>
              </span>
            </label>
          </fieldset>
          <DialogFooter className="settings-actions">
            <Button
              type="button"
              variant="ghost"
              onClick={() =>
                setDraft({
                  archived: "exclude",
                  source: "",
                  cwd: "",
                  conversationDisplayTypes: [
                    ...DEFAULT_CONVERSATION_DISPLAY_TYPES,
                  ],
                  theme: "system",
                  language: preferredLanguage(),
                  searchCtrlShiftF: false,
                })
              }
            >
              {t("reset")}
            </Button>
            <span className="settings-action-spacer" />
            <Button
              type="button"
              variant="outline"
              onClick={() => setOpen(false)}
            >
              {t("cancel")}
            </Button>
            <Button type="submit">{t("apply")}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function SessionSidebar(props: {
  groups: SessionGroup[];
  loading: boolean;
  error: string;
  onNavigate: () => void;
}) {
  const { t, i18n } = useTranslation();
  const location = useLocation();
  if (props.loading)
    return (
      <div className="skeleton-list" aria-label={t("loading")}>
        {[0, 1, 2, 3, 4].map((item) => (
          <Skeleton className="h-16" key={item} />
        ))}
      </div>
    );
  if (props.error) return <Empty text={props.error} />;
  if (props.groups.length === 0) return <Empty text={t("noSessions")} />;
  return (
    <nav className="session-list" aria-label={t("sessions")}>
      <ul className="session-tree-list">
        {props.groups.map((group) => (
          <SessionTreeItem
            key={group.root.session.id}
            node={group.root}
            locationPath={location.pathname}
            language={i18n.language}
            onNavigate={props.onNavigate}
          />
        ))}
      </ul>
    </nav>
  );
}

function SessionTreeItem({
  node,
  parentTitle,
  locationPath,
  language,
  onNavigate,
}: {
  node: SessionTreeNode;
  parentTitle?: string;
  locationPath: string;
  language: string;
  onNavigate: () => void;
}) {
  const { t } = useTranslation();
  const session = node.session;
  const source = sourceLabel(session.source, t);
  const ownTitle = localizedTitle(session);
  const displayTitle =
    session.parentRelation === "planHandoff"
      ? parentTitle
        ? t("implementTitle", { title: parentTitle })
        : t("implementPlan")
      : ownTitle;
  const active = locationPath === `/sessions/${session.id}`;
  return (
    <li className="session-tree-node">
      <Link
        onClick={onNavigate}
        className={`session-item ${active ? "active" : ""}`}
        aria-current={active ? "page" : undefined}
        to={`/sessions/${session.id}`}
      >
        <span
          className={`session-avatar source-${session.source}`}
          title={source}
          aria-hidden="true"
        >
          {sourceAvatar(session.source)}
        </span>
        <span className="session-copy">
          <span className="session-heading">
            <strong className="session-title">{displayTitle}</strong>
            <time dateTime={session.updatedAt}>
              {friendlySessionTime(session.updatedAt, language, t)}
            </time>
          </span>
          <span className="session-preview">
            {session.preview || t("noPreview")}
          </span>
          {session.cwd && (
            <span className="session-cwd" title={session.cwd}>
              {session.cwd}
            </span>
          )}
          <span className="sr-only">{source}</span>
        </span>
      </Link>
      {node.children.length > 0 && (
        <ul className="session-children">
          {node.children.map((child) => (
            <SessionTreeItem
              key={child.session.id}
              node={child}
              parentTitle={ownTitle}
              locationPath={locationPath}
              language={language}
              onNavigate={onNavigate}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

type ScrollTarget = {
  kind: "top" | "bottom" | "around";
  id?: string;
  token: number;
};
type ViewportState = { atBottom: boolean; anchorId?: string };

const TRANSCRIPT_BOTTOM_THRESHOLD = 80;
const TRANSCRIPT_EXACT_BOTTOM_TOLERANCE = 1;

function transcriptBottomDistance(element: HTMLElement) {
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

function Conversation({
  onInspect,
  signals,
  resyncSequence,
  conversationDisplayTypes,
  onForceConversationDisplayType,
}: {
  onInspect: (s: string, e: string) => void;
  signals: Record<string, number>;
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
  const [entries, setEntries] = useState<EntryListItem[]>([]);
  const [previousCursor, setPreviousCursor] = useState<string>();
  const [nextCursor, setNextCursor] = useState<string>();
  const [error, setError] = useState("");
  const [newCount, setNewCount] = useState(0);
  const [visibilityReady, setVisibilityReady] = useState(false);
  const [deepLinkDisplayType, setDeepLinkDisplayType] =
    useState<ConversationDisplayType>();
  const [scrollTarget, setScrollTarget] = useState<ScrollTarget>();
  const viewport = useRef<ViewportState>({ atBottom: true });
  const requestSequence = useRef(0);
  const targetSequence = useRef(0);
  const loadingCursors = useRef(new Set<string>());
  const handledSignal = useRef(0);
  const refreshTimer = useRef<number | undefined>(undefined);
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
    const controller = new AbortController();
    setEntries([]);
    setPreviousCursor(undefined);
    setNextCursor(undefined);
    setDeepLinkDisplayType(undefined);
    onForceConversationDisplayType(undefined);
    setNewCount(0);
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
        setSession(detail.summary);
        setEntries(page.data);
        setPreviousCursor(page.previousCursor);
        setNextCursor(page.nextCursor);
        setScrollTarget({ kind, id, token: ++targetSequence.current });
        if (kind === "bottom") setNewCount(0);
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
    const anchor =
      around ??
      (!viewport.current.atBottom ? viewport.current.anchorId : undefined);
    void replacePage(anchor ? "around" : "bottom", anchor, controller.signal);
    return () => controller.abort();
  }, [around, replacePage, visibilityReady]);

  useEffect(() => {
    handledSignal.current = Math.max(signals[sessionId] ?? 0, resyncSequence);
  }, [sessionId]);
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
      else setNewCount((value) => value + 1);
    }, 100);
    return () => {
      if (refreshTimer.current !== undefined) {
        window.clearTimeout(refreshTimer.current);
        refreshTimer.current = undefined;
      }
    };
  }, [eventSequence, replacePage, resyncSequence]);

  const loadOlder = useCallback(async () => {
    const cursor = previousCursor;
    if (!cursor || loadingCursors.current.has(cursor)) return;
    loadingCursors.current.add(cursor);
    try {
      const page = await api.entries(sessionId, {
        cursor,
        limit: 100,
        displayTypes: serializedConversationDisplayTypes,
      });
      setEntries((current) => mergeEntries(page.data, current));
      setPreviousCursor(page.previousCursor);
      setError("");
    } catch (f) {
      setError(message(f));
    } finally {
      loadingCursors.current.delete(cursor);
    }
  }, [previousCursor, serializedConversationDisplayTypes, sessionId]);

  const loadNewer = useCallback(async () => {
    const cursor = nextCursor;
    if (!cursor || loadingCursors.current.has(cursor)) return;
    loadingCursors.current.add(cursor);
    try {
      const page = await api.entries(sessionId, {
        cursor,
        limit: 100,
        displayTypes: serializedConversationDisplayTypes,
      });
      setEntries((current) => mergeEntries(current, page.data));
      setNextCursor(page.nextCursor);
      setError("");
    } catch (f) {
      setError(message(f));
    } finally {
      loadingCursors.current.delete(cursor);
    }
  }, [nextCursor, serializedConversationDisplayTypes, sessionId]);

  const updateViewport = useCallback((next: ViewportState) => {
    viewport.current = next;
  }, []);
  return (
    <>
      {session && (
        <div className="conversation-head">
          <h1>{localizedTitle(session)}</h1>
          {session.cwd && (
            <div className="conversation-cwd" title={session.cwd}>
              {session.cwd}
            </div>
          )}
          <div className="muted">
            {sourceLabel(session.source, t)} ·{" "}
            {t("entryCount", { count: session.entryCount })} ·{" "}
            {session.completeness}
          </div>
        </div>
      )}
      {error ? (
        <Empty text={error} />
      ) : entries.length === 0 ? (
        <Empty text={t("noEntries")} />
      ) : (
        <VirtualTranscript
          entries={entries}
          around={around}
          hasOlder={Boolean(previousCursor)}
          hasNewer={Boolean(nextCursor)}
          newCount={newCount}
          scrollTarget={scrollTarget}
          onInspect={(id) => onInspect(sessionId, id)}
          onLoadOlder={loadOlder}
          onLoadNewer={loadNewer}
          onJumpTop={() => replacePage("top")}
          onJumpBottom={() => replacePage("bottom")}
          onViewportChange={updateViewport}
        />
      )}
    </>
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
    estimateSize: (index) => (entries[index]?.kind === "message" ? 96 : 36),
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

function TranscriptEntryView({
  entry,
  previous,
  highlighted,
  locale,
  onInspect,
}: {
  entry: EntryListItem;
  previous?: EntryListItem;
  highlighted: boolean;
  locale: string;
  onInspect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const dateLabel = entryDateLabel(entry, previous, locale, t);
  const timestamp = entry.timestamp ? new Date(entry.timestamp) : undefined;
  const bubble =
    entry.kind === "message" &&
    (entry.presentation === "user" || entry.presentation === "response");
  const requestUserInput = requestUserInputDetails(entry);
  const activity = activityParts(entry, t);
  const activityPreview = firstActivityLine(activity.body);
  const activityNotice = (
    <button
      className={`activity-notice notice-${entry.kind}`}
      onClick={() => onInspect(entry.id)}
      aria-label={`${activity.label} ${activity.body} · ${t("openInspector")}`.trim()}
    >
      <span className="activity-label">{activity.label}</span>
      {activity.body && (
        <span className="activity-body">{activityPreview.text}</span>
      )}
    </button>
  );
  return (
    <>
      {dateLabel && (
        <div className="date-divider">
          <span>{dateLabel}</span>
        </div>
      )}
      {requestUserInput ? (
        <RequestUserInputMessages
          entryId={entry.id}
          details={requestUserInput}
          highlighted={highlighted}
          locale={locale}
          timestamp={timestamp}
          onInspect={onInspect}
        />
      ) : bubble ? (
        <article
          data-transcript-entry
          className={`message-row ${entry.presentation === "user" ? "message-user" : "message-assistant"}`}
          aria-current={highlighted || undefined}
        >
          <div className="message-bubble">
            <MessageBubbleContent
              entry={entry}
              locale={locale}
              timestamp={timestamp}
              onInspect={onInspect}
            />
          </div>
        </article>
      ) : (
        <div
          data-transcript-entry
          className="notice-row"
          aria-current={highlighted || undefined}
        >
          {activityPreview.truncated ? (
            <Tooltip>
              <TooltipTrigger asChild>{activityNotice}</TooltipTrigger>
              <TooltipContent
                className="activity-tooltip"
                side="top"
                align="start"
                sideOffset={6}
              >
                {activity.body}
              </TooltipContent>
            </Tooltip>
          ) : (
            activityNotice
          )}
        </div>
      )}
    </>
  );
}

function MessageBubbleContent({
  entry,
  locale,
  timestamp,
  onInspect,
}: {
  entry: EntryListItem;
  locale: string;
  timestamp?: Date;
  onInspect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [text, setText] = useState(entry.primaryPreview);
  const [loadState, setLoadState] = useState<"complete" | "loading" | "failed">(
    entry.primaryComplete ? "complete" : "loading",
  );
  const [attempt, setAttempt] = useState(0);
  const loadPromise = useRef<Promise<string> | undefined>(undefined);

  useEffect(() => {
    setText(entry.primaryPreview);
    if (entry.primaryComplete) {
      setLoadState("complete");
      loadPromise.current = undefined;
      return;
    }
    const controller = new AbortController();
    setLoadState("loading");
    const promise = fullPrimaryText(entry, controller.signal);
    loadPromise.current = promise;
    void promise
      .then((completeText) => {
        if (!controller.signal.aborted) {
          setText(completeText);
          setLoadState("complete");
        }
      })
      .catch((failure: unknown) => {
        if (
          !controller.signal.aborted &&
          !(failure instanceof DOMException && failure.name === "AbortError")
        )
          setLoadState("failed");
      })
      .finally(() => {
        if (loadPromise.current === promise) loadPromise.current = undefined;
      });
    return () => controller.abort();
  }, [
    attempt,
    entry.id,
    entry.primaryComplete,
    entry.primaryPreview,
    entry.sessionId,
  ]);

  const getFullText = useCallback(async () => {
    if (loadState === "complete") return text;
    if (loadPromise.current) return loadPromise.current;
    setLoadState("loading");
    const promise = fullPrimaryText(entry);
    loadPromise.current = promise;
    try {
      const completeText = await promise;
      setText(completeText);
      setLoadState("complete");
      return completeText;
    } catch (failure) {
      setLoadState("failed");
      throw failure;
    } finally {
      if (loadPromise.current === promise) loadPromise.current = undefined;
    }
  }, [entry, loadState, text]);

  return (
    <>
      <span className="sr-only">
        {entry.presentation === "user" ? t("user") : t("assistant")}:{" "}
      </span>
      <SafeMarkdown text={text} />
      {loadState === "loading" && (
        <p className="message-load-status" role="status">
          {t("loadingFullMessage")}
        </p>
      )}
      {loadState === "failed" && (
        <div className="message-load-error" role="alert">
          <span>{t("loadFullMessageFailed")}</span>
          <Button
            type="button"
            variant="outline"
            size="xs"
            onClick={() => setAttempt((current) => current + 1)}
          >
            {t("retry")}
          </Button>
        </div>
      )}
      <footer className="message-meta">
        <CopyMessageButton entry={entry} getText={getFullText} />
        <Button
          variant="ghost"
          size="icon-xs"
          className="message-action"
          aria-label={t("openInspector")}
          onClick={() => onInspect(entry.id)}
        >
          <PanelRight size={13} />
        </Button>
        {timestamp && <EntryTime value={timestamp} locale={locale} />}
      </footer>
    </>
  );
}

function RequestUserInputMessages({
  entryId,
  details,
  highlighted,
  locale,
  timestamp,
  onInspect,
}: {
  entryId: string;
  details: RequestUserInputDetails;
  highlighted: boolean;
  locale: string;
  timestamp?: Date;
  onInspect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const legacyNotes = legacyRequestUserInputNotes(details);
  return (
    <div className="request-user-input-message-group">
      {details.questions.map((question, questionIndex) => {
        const answer = visibleRequestUserInputAnswer(details, question);
        const options = requestUserInputOptions(question, answer.selections);
        const firstSelected = options.find((option) => option.selected)?.label;
        const note =
          answer.note ??
          (legacyNotes.targetId === question.id ? legacyNotes.note : undefined);
        const pollTitleId = `${entryId}-poll-${questionIndex}`;
        return (
          <article
            data-transcript-entry
            className="message-row message-assistant request-user-input-message"
            aria-current={highlighted || undefined}
            aria-labelledby={pollTitleId}
            key={`${question.id}-${questionIndex}`}
          >
            <div className="message-bubble request-user-input-poll">
              <div className="request-user-input-poll-title" id={pollTitleId}>
                {question.question}
              </div>
              <ul className="request-user-input-options">
                {options.map((option, optionIndex) => (
                  <li
                    className={`request-user-input-option${option.selected ? " is-selected" : ""}`}
                    key={`${option.label}-${optionIndex}`}
                  >
                    <span
                      className="request-user-input-radio"
                      aria-hidden="true"
                    >
                      {option.selected && <Check size={12} strokeWidth={3} />}
                    </span>
                    <span className="request-user-input-option-copy">
                      <span className="request-user-input-option-label">
                        {option.label}
                      </span>
                      {option.description && (
                        <>
                          <span aria-hidden="true"> — </span>
                          <span className="request-user-input-option-description">
                            {option.description}
                          </span>
                        </>
                      )}
                      {option.selected && (
                        <span className="sr-only"> {t("selected")}</span>
                      )}
                      {note && option.label === firstSelected && (
                        <span className="request-user-input-option-note">
                          {note}
                        </span>
                      )}
                    </span>
                  </li>
                ))}
              </ul>
              {note && firstSelected === undefined && (
                <div className="request-user-input-unassigned-note">{note}</div>
              )}
              {questionIndex === details.questions.length - 1 &&
                legacyNotes.footerNote && (
                  <div className="request-user-input-legacy-note">
                    <span>notes:</span> {legacyNotes.footerNote}
                  </div>
                )}
              <footer className="message-meta">
                <Button
                  variant="ghost"
                  size="icon-xs"
                  className="message-action"
                  aria-label={`${t("openInspector")}: ${question.question}`}
                  onClick={() => onInspect(entryId)}
                >
                  <PanelRight size={13} />
                </Button>
                {timestamp && <EntryTime value={timestamp} locale={locale} />}
              </footer>
            </div>
          </article>
        );
      })}
    </div>
  );
}

function EntryTime({ value, locale }: { value: Date; locale: string }) {
  return (
    <time
      className="entry-time"
      dateTime={value.toISOString()}
      title={new Intl.DateTimeFormat(locale, {
        dateStyle: "medium",
        timeStyle: "medium",
      }).format(value)}
    >
      {new Intl.DateTimeFormat(locale, {
        hour: "2-digit",
        minute: "2-digit",
        hourCycle: "h23",
      }).format(value)}
    </time>
  );
}

function CopyMessageButton({
  entry,
  getText,
}: {
  entry: EntryListItem;
  getText?: () => Promise<string>;
}) {
  const { t } = useTranslation();
  const [state, setState] = useState<"idle" | "copying" | "copied" | "failed">(
    "idle",
  );
  useEffect(() => {
    if (state !== "copied" && state !== "failed") return;
    const timer = window.setTimeout(() => setState("idle"), 1600);
    return () => window.clearTimeout(timer);
  }, [state]);
  const copy = async () => {
    setState("copying");
    try {
      await navigator.clipboard.writeText(
        await (getText ? getText() : fullPrimaryText(entry)),
      );
      setState("copied");
    } catch {
      setState("failed");
    }
  };
  const label =
    state === "copied"
      ? t("copied")
      : state === "failed"
        ? t("copyFailed")
        : state === "copying"
          ? t("copying")
          : t("copyMessage");
  return (
    <>
      <Button
        variant="ghost"
        size="icon-xs"
        className="message-action"
        disabled={state === "copying"}
        aria-label={label}
        title={label}
        onClick={() => void copy()}
      >
        {state === "copied" ? <Check size={13} /> : <Copy size={13} />}
      </Button>
      {state !== "idle" && (
        <span className="sr-only" role="status" aria-live="polite">
          {label}
        </span>
      )}
    </>
  );
}

async function fullPrimaryText(entry: EntryListItem, signal?: AbortSignal) {
  if (entry.primaryComplete) return entry.primaryPreview;
  let offset = 0;
  let text = "";
  for (;;) {
    const chunk = await api.content(
      entry.sessionId,
      entry.id,
      "primary",
      offset,
      signal,
    );
    text += chunk.text;
    if (chunk.nextOffset === undefined) return text;
    if (chunk.nextOffset <= offset)
      throw new Error("content pagination did not advance");
    offset = chunk.nextOffset;
  }
}

type ClipboardCopyState = "idle" | "copying" | "copied" | "failed";

function useClipboardCopy() {
  const [state, setState] = useState<ClipboardCopyState>("idle");
  useEffect(() => {
    if (state !== "copied" && state !== "failed") return;
    const timer = window.setTimeout(() => setState("idle"), 1600);
    return () => window.clearTimeout(timer);
  }, [state]);
  const copyText = useCallback(async (text: string) => {
    setState("copying");
    try {
      await navigator.clipboard.writeText(text);
      setState("copied");
    } catch {
      setState("failed");
    }
  }, []);
  return { copyText, state };
}

function copyStateLabel(
  state: ClipboardCopyState,
  idle: string,
  copying: string,
  copied: string,
  failed: string,
) {
  if (state === "copying") return copying;
  if (state === "copied") return copied;
  if (state === "failed") return failed;
  return idle;
}

function reactNodeText(node: ReactNode): string {
  if (
    typeof node === "string" ||
    typeof node === "number" ||
    typeof node === "bigint"
  )
    return String(node);
  if (Array.isArray(node)) return node.map(reactNodeText).join("");
  if (isValidElement<{ children?: ReactNode }>(node))
    return reactNodeText(node.props.children);
  return "";
}

const MarkdownCodeBlockContext = createContext(false);

function MarkdownCodeBlock({
  node: _node,
  children,
  ...props
}: ComponentProps<"pre"> & ExtraProps) {
  const { t } = useTranslation();
  const { copyText, state } = useClipboardCopy();
  const text = reactNodeText(children).replace(/\n$/, "");
  const label = copyStateLabel(
    state,
    t("copyCode"),
    t("copying"),
    t("copied"),
    t("copyFailed"),
  );
  return (
    <div className="markdown-code-block">
      <Button
        variant="ghost"
        size="icon-sm"
        className="markdown-code-copy"
        data-copy-state={state}
        disabled={state === "copying"}
        aria-label={label}
        title={label}
        onClick={() => void copyText(text)}
      >
        {state === "copied" ? <Check size={14} /> : <Copy size={14} />}
      </Button>
      <MarkdownCodeBlockContext.Provider value>
        <pre {...props}>{children}</pre>
      </MarkdownCodeBlockContext.Provider>
      {state !== "idle" && (
        <span className="sr-only" role="status" aria-live="polite">
          {label}
        </span>
      )}
    </div>
  );
}

function MarkdownCode({
  node: _node,
  children,
  className,
  ...props
}: ComponentProps<"code"> & ExtraProps) {
  const block = useContext(MarkdownCodeBlockContext);
  const { t } = useTranslation();
  const { copyText, state } = useClipboardCopy();
  if (block)
    return (
      <code className={className} {...props}>
        {children}
      </code>
    );

  const text = reactNodeText(children);
  const label = copyStateLabel(
    state,
    t("copyInlineCode", { code: text }),
    t("copying"),
    t("copied"),
    t("copyFailed"),
  );
  return (
    <>
      <button
        type="button"
        className="markdown-inline-code"
        data-copy-state={state}
        disabled={state === "copying"}
        aria-label={label}
        title={label}
        onClick={() => void copyText(text)}
      >
        <code className={className} {...props}>
          {children}
        </code>
      </button>
      {state !== "idle" && (
        <span className="sr-only" role="status" aria-live="polite">
          {label}
        </span>
      )}
    </>
  );
}

export function SafeMarkdown({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <div className="markdown-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSanitize, [rehypeHighlight, { detect: true }]]}
        skipHtml
        components={{
          img: ({ alt }) => (
            <span className="badge">
              {t("attachment")}: {alt || t("image")}
            </span>
          ),
          a: (props) => (
            <a {...props} target="_blank" rel="noreferrer noopener" />
          ),
          table: ({ children }) => (
            <div className="markdown-table">
              <table>{children}</table>
            </div>
          ),
          pre: MarkdownCodeBlock,
          code: MarkdownCode,
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

function Inspector({
  selected,
  onClose,
}: {
  selected?: { sessionId: string; entryId: string };
  onClose?: () => void;
}) {
  const { t } = useTranslation();
  const [detail, setDetail] = useState<TranscriptEntry>();
  const [primary, setPrimary] = useState<ContentChunk>();
  const [secondary, setSecondary] = useState<ContentChunk>();
  const [raw, setRaw] = useState<RawRecord>();
  const [error, setError] = useState("");
  useEffect(() => {
    setDetail(undefined);
    setPrimary(undefined);
    setSecondary(undefined);
    setRaw(undefined);
    setError("");
    if (!selected) return;
    const controller = new AbortController();
    api
      .entry(selected.sessionId, selected.entryId, controller.signal)
      .then(async (next) => {
        setDetail(next);
        const [input, output] = await Promise.all([
          api.content(
            selected.sessionId,
            selected.entryId,
            "primary",
            0,
            controller.signal,
          ),
          api.content(
            selected.sessionId,
            selected.entryId,
            "secondary",
            0,
            controller.signal,
          ),
        ]);
        setPrimary(input);
        setSecondary(output);
      })
      .catch((f) => {
        if (!(f instanceof DOMException)) setError(message(f));
      });
    return () => controller.abort();
  }, [selected]);
  const loadMore = async (field: "primary" | "secondary") => {
    if (!selected) return;
    const current = field === "primary" ? primary : secondary;
    if (current?.nextOffset === undefined) return;
    try {
      const chunk = await api.content(
        selected.sessionId,
        selected.entryId,
        field,
        current.nextOffset,
      );
      const merged = {
        ...chunk,
        byteOffset: 0,
        text: current.text + chunk.text,
      };
      if (field === "primary") setPrimary(merged);
      else setSecondary(merged);
    } catch (f) {
      setError(message(f));
    }
  };
  if (!selected || !detail)
    return (
      <div className="inspector-empty">
        <Empty text={error || t("inspectorEmpty")} />
        {onClose && (
          <Button variant="outline" size="sm" onClick={onClose}>
            {t("close")}
          </Button>
        )}
      </div>
    );
  return (
    <>
      <div className="pane-header inspector-head">
        <strong>{detail.item.title || t("details")}</strong>
        {onClose && (
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("closeInspector")}
            onClick={onClose}
          >
            <X size={16} />
          </Button>
        )}
      </div>
      <div className="inspector-body">
        <p className="muted">
          {detail.item.kind} · #{detail.item.sequence}
          {detail.item.toolStatus ? ` · ${detail.item.toolStatus}` : ""}
        </p>
        {primary && primary.totalBytes > 0 && (
          <section>
            <div className="inspector-section-head">
              <h3>{t("inputContent")}</h3>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => void navigator.clipboard.writeText(primary.text)}
              >
                <Copy size={14} /> {t("copy")}
              </Button>
            </div>
            <pre className="inspector-content">{primary.text}</pre>
            {primary.nextOffset !== undefined && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => void loadMore("primary")}
              >
                {t("loadMore")}
              </Button>
            )}
          </section>
        )}
        {secondary && secondary.totalBytes > 0 && (
          <section>
            <h3>{t("outputContent")}</h3>
            <pre className="inspector-content">{secondary.text}</pre>
            {secondary.nextOffset !== undefined && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => void loadMore("secondary")}
              >
                {t("loadMore")}
              </Button>
            )}
          </section>
        )}
        <h3>{t("raw")}</h3>
        {detail.rawRefs.map((ref) => (
          <button
            className="raw-item"
            key={ref.id}
            onClick={() =>
              api
                .raw(selected.sessionId, ref.id)
                .then(setRaw)
                .catch((f) => setError(message(f)))
            }
          >
            #{ref.line} {ref.envelopeType}
          </button>
        ))}
        {error && <p className="error">{error}</p>}
        {raw && <pre className="raw-content">{raw.chunk.text}</pre>}
      </div>
    </>
  );
}

function SearchDialog({
  onClose,
  onOpen,
}: {
  onClose: () => void;
  onOpen: (hit: SearchHit) => void;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [partial, setPartial] = useState(false);
  const [error, setError] = useState("");
  const [allTypes, setAllTypes] = useSearchAllTypes();
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => input.current?.focus(), []);
  useEffect(() => {
    if (!q.trim()) {
      setHits([]);
      setPartial(false);
      setError("");
      return;
    }
    const c = new AbortController();
    const timer = setTimeout(
      () =>
        api
          .search(q, { archived: "include", allTypes }, c.signal)
          .then((page) => {
            setHits(page.data);
            setPartial(page.partial);
            setError("");
          })
          .catch((f) => {
            if (!(f instanceof DOMException)) setError(message(f));
          }),
      150,
    );
    return () => {
      clearTimeout(timer);
      c.abort();
    };
  }, [allTypes, q]);
  return (
    <CommandDialog
      title={t("search")}
      description={t("searchHelp")}
      className="search-dialog"
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      showCloseButton={false}
    >
      <CommandInput
        ref={input}
        aria-label={t("search")}
        value={q}
        onValueChange={setQ}
        placeholder={t("searchPlaceholder")}
      />
      <label className="search-scope">
        <input
          type="checkbox"
          checked={allTypes}
          onChange={(event) => setAllTypes(event.target.checked)}
        />
        <span>
          <strong>{t("searchAllTypes")}</strong>
          <small>{t("searchAllTypesHelp")}</small>
        </span>
      </label>
      <CommandList className="search-list">
        {partial && <p className="search-feedback muted">{t("partial")}</p>}
        {error && (
          <p className="search-feedback error" role="alert">
            {error}
          </p>
        )}
        {q && <CommandEmpty>{t("emptySearch")}</CommandEmpty>}
        {hits.map((hit) => (
          <CommandItem
            className="search-result"
            value={`${hit.session.title} ${hit.snippet}`}
            key={`${hit.entryId}-${hit.field}`}
            onSelect={() => onOpen(hit)}
          >
            <span className="search-result-copy">
              <strong>{localizedTitle(hit.session)}</strong>
              <span>{hit.snippet}</span>
              <small className="muted">
                {hit.kind} · {hit.field}
              </small>
            </span>
          </CommandItem>
        ))}
      </CommandList>
    </CommandDialog>
  );
}

function SearchPage() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [error, setError] = useState("");
  const [allTypes, setAllTypes] = useSearchAllTypes();
  useEffect(() => {
    if (!q.trim()) {
      setHits([]);
      setError("");
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(
      () =>
        api
          .search(q, { archived: "include", allTypes }, controller.signal)
          .then((page) => {
            setHits(page.data);
            setError("");
          })
          .catch((f) => {
            if (!(f instanceof DOMException)) setError(message(f));
          }),
      150,
    );
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [allTypes, q]);
  return (
    <div className="search-page">
      <div className="conversation-head search-page-head">
        <h1>{t("search")}</h1>
        <Input
          aria-label={t("search")}
          value={q}
          onChange={(e) => setParams({ q: e.target.value })}
          placeholder={t("searchPlaceholder")}
        />
        <label className="search-scope">
          <input
            type="checkbox"
            checked={allTypes}
            onChange={(event) => setAllTypes(event.target.checked)}
          />
          <span>
            <strong>{t("searchAllTypes")}</strong>
            <small>{t("searchAllTypesHelp")}</small>
          </span>
        </label>
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {hits.map((hit) => (
        <Link
          className="search-result"
          key={`${hit.entryId}-${hit.field}`}
          to={`/sessions/${hit.session.id}?entry=${hit.entryId}`}
        >
          <span className="search-result-copy">
            <strong>{localizedTitle(hit.session)}</strong>
            <span>{hit.snippet}</span>
            <small className="muted">
              {hit.kind} · {hit.field}
            </small>
          </span>
        </Link>
      ))}
    </div>
  );
}
function useSearchAllTypes() {
  const [allTypes, setAllTypesState] = useState(
    () => localStorage.getItem("agents-viewer-search-all-types") === "true",
  );
  const setAllTypes = useCallback((value: boolean) => {
    setAllTypesState(value);
    localStorage.setItem("agents-viewer-search-all-types", String(value));
  }, []);
  return [allTypes, setAllTypes] as const;
}
function Empty({ text }: { text: string }) {
  return (
    <div className="empty" role="status">
      {text}
    </div>
  );
}
function message(failure: unknown) {
  return failure instanceof ApiClientError
    ? failure.message
    : failure instanceof Error
      ? failure.message
      : i18n.t("unknownError");
}
function localizedTitle(session: SessionSummary) {
  return session.title.startsWith("Untitled ·")
    ? `${i18n.t("untitled")} · ${new Date(session.createdAt).toLocaleString()}`
    : session.title;
}
type Translate = (key: string, options?: Record<string, unknown>) => string;
function sourceLabel(source: SourceKind, t: Translate) {
  return t(
    (
      {
        cli: "sourceCli",
        vscode: "sourceVscode",
        exec: "sourceExec",
        review: "sourceReview",
        subagent: "sourceSubagent",
        appServer: "sourceAppServer",
        unknown: "sourceUnknown",
      } as const
    )[source],
  );
}
function sourceHelp(source: SourceKind, t: Translate) {
  return t(
    (
      {
        cli: "sourceCliHelp",
        vscode: "sourceVscodeHelp",
        exec: "sourceExecHelp",
        review: "sourceReviewHelp",
        subagent: "sourceSubagentHelp",
        appServer: "sourceAppServerHelp",
        unknown: "sourceUnknownHelp",
      } as const
    )[source],
  );
}
function sourceAvatar(source: SourceKind) {
  return (
    {
      cli: "C",
      vscode: "V",
      exec: ">_",
      review: "R",
      subagent: "S",
      appServer: "A",
      unknown: "?",
    } as const
  )[source];
}
function friendlySessionTime(value: string, locale: string, t: Translate) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  const difference = Math.floor((today.getTime() - day.getTime()) / 86400000);
  if (difference === 0)
    return new Intl.DateTimeFormat(locale, {
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    }).format(date);
  if (difference === 1) return t("yesterday");
  if (difference > 1 && difference < 7)
    return new Intl.DateTimeFormat(locale, { weekday: "short" }).format(date);
  return new Intl.DateTimeFormat(
    locale,
    date.getFullYear() === now.getFullYear()
      ? { month: "short", day: "numeric" }
      : { year: "numeric", month: "short", day: "numeric" },
  ).format(date);
}
function indexStatusLabel(status: Status, t: Translate) {
  switch (status.phase) {
    case "discovering":
      return t("indexDiscovering");
    case "indexing":
      return t("indexIndexing", {
        processed: status.progress.processedFiles,
        total: status.progress.totalFiles,
      });
    case "degraded":
      return t("indexDegraded", { count: status.progress.failedFiles });
    case "ready":
      return t("indexReady");
    case "starting":
      return t("loading");
    case "shuttingDown":
      return t("close");
  }
}
function indexPercent(status: Status) {
  if (status.phase === "discovering") return 35;
  if (status.progress.totalBytes > 0)
    return (status.progress.processedBytes / status.progress.totalBytes) * 100;
  if (status.progress.totalFiles > 0)
    return (status.progress.processedFiles / status.progress.totalFiles) * 100;
  return 100;
}
function indexWindowLabel(
  status: Status,
  t: (key: string, options?: Record<string, unknown>) => string,
) {
  return status.initialIndexDays === -1
    ? t("allHistory")
    : status.initialIndexDays === 0
      ? t("newOnly")
      : t("dayWindow", { count: status.initialIndexDays });
}
function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}
function mergeEntries(first: EntryListItem[], second: EntryListItem[]) {
  const seen = new Set<string>();
  return [...first, ...second]
    .filter((entry) => {
      if (seen.has(entry.id)) return false;
      seen.add(entry.id);
      return true;
    })
    .sort(
      (left, right) =>
        left.sequence - right.sequence || left.id.localeCompare(right.id),
    );
}
export function conversationDisplayType(
  entry: EntryListItem,
): ConversationDisplayType {
  if (entry.kind === "message") {
    if (entry.presentation === "user") return "sent";
    if (entry.presentation === "response") return "received";
    if (entry.presentation === "internal") return "internalMessage";
    return "technicalMessage";
  }
  if (entry.kind === "tool") {
    switch (entry.toolKind) {
      case "requestUserInput":
        return "requestUserInput";
      case "command":
        return "exec";
      case "patch":
        return "patch";
      case "mcp":
        return "mcp";
      case "webSearch":
        return "webSearch";
      case "function":
        return "function";
      case "dynamic":
        return "dynamic";
      case "terminal":
        return "terminal";
      case "viewImage":
        return "viewImage";
      default:
        return "otherTool";
    }
  }
  switch (entry.kind) {
    case "reasoning":
      return "reasoning";
    case "plan":
      return "plan";
    case "warning":
      return "warning";
    case "error":
      return "error";
    case "context":
      return "context";
    case "marker":
      return "marker";
    default:
      return "unknown";
  }
}
export function isDefaultVisible(entry: EntryListItem) {
  return (
    entry.kind === "reasoning" ||
    entry.kind === "warning" ||
    entry.kind === "error" ||
    (entry.kind === "message" &&
      (entry.presentation === "user" || entry.presentation === "response")) ||
    (entry.kind === "tool" &&
      (entry.toolKind === "command" || entry.toolKind === "requestUserInput"))
  );
}

type RequestUserInputOption = {
  label: string;
  description: string;
};
type RequestUserInputQuestion = {
  id: string;
  question: string;
  isSecret: boolean;
  options: RequestUserInputOption[];
};
type RequestUserInputAnswer = {
  selections: string[];
  note?: string;
};
type RequestUserInputDetails = {
  questions: RequestUserInputQuestion[];
  answers: Map<string, RequestUserInputAnswer>;
  legacyNotes?: string;
};
function requestUserInputDetails(
  entry: EntryListItem,
): RequestUserInputDetails | undefined {
  if (entry.kind !== "tool" || entry.toolKind !== "requestUserInput")
    return undefined;
  const rawQuestions = entry.metadata.requestUserInputQuestions;
  if (!Array.isArray(rawQuestions)) return undefined;
  const questions = rawQuestions.flatMap(
    (value): RequestUserInputQuestion[] => {
      const question = recordValue(value);
      if (
        !question ||
        typeof question.id !== "string" ||
        typeof question.question !== "string"
      )
        return [];
      const options = Array.isArray(question.options)
        ? question.options.flatMap((candidate): RequestUserInputOption[] => {
            const option = recordValue(candidate);
            if (
              !option ||
              typeof option.label !== "string" ||
              typeof option.description !== "string"
            )
              return [];
            return [{ label: option.label, description: option.description }];
          })
        : [];
      return [
        {
          id: question.id,
          question: question.question,
          isSecret: question.isSecret === true,
          options,
        },
      ];
    },
  );
  if (questions.length === 0) return undefined;
  const answers = new Map<string, RequestUserInputAnswer>();
  const rawAnswers = recordValue(entry.metadata.requestUserInputAnswers);
  for (const question of questions) {
    const answer = recordValue(rawAnswers?.[question.id]);
    if (!Array.isArray(answer?.answers)) continue;
    const selections: string[] = [];
    let note: string | undefined;
    for (const value of answer.answers) {
      if (typeof value !== "string") continue;
      const noteText = value.startsWith("user_note: ")
        ? value.slice("user_note: ".length).trim()
        : undefined;
      if (noteText !== undefined) {
        if (noteText) note = noteText;
      } else {
        selections.push(value);
      }
    }
    answers.set(question.id, { selections, note });
  }
  const legacyNotes = entry.metadata.requestUserInputNotes;
  return {
    questions,
    answers,
    legacyNotes: typeof legacyNotes === "string" ? legacyNotes : undefined,
  };
}
function recordValue(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}
function visibleRequestUserInputAnswer(
  details: RequestUserInputDetails,
  question: RequestUserInputQuestion,
): RequestUserInputAnswer {
  if (question.isSecret) return { selections: [] };
  return details.answers.get(question.id) ?? { selections: [] };
}
function requestUserInputOptions(
  question: RequestUserInputQuestion,
  selections: string[],
) {
  const selected = new Set(selections);
  const known = new Set(question.options.map((option) => option.label));
  const resultOnly = selections
    .filter(
      (label, index) =>
        !known.has(label) && selections.indexOf(label) === index,
    )
    .map((label) => ({ label, description: "" }));
  return [...question.options, ...resultOnly].map((option) => ({
    ...option,
    selected: selected.has(option.label),
  }));
}
function legacyRequestUserInputNotes(details: RequestUserInputDetails): {
  targetId?: string;
  note?: string;
  footerNote?: string;
} {
  const note = details.legacyNotes?.trim();
  if (
    !note ||
    details.questions.some((question) => question.isSecret) ||
    [...details.answers.values()].some((answer) => answer.note)
  )
    return {};
  const answered = details.questions.filter(
    (question) =>
      (details.answers.get(question.id)?.selections.length ?? 0) > 0,
  );
  const target =
    details.questions.length === 1
      ? details.questions[0]
      : answered.length === 1
        ? answered[0]
        : undefined;
  return target ? { targetId: target.id, note } : { footerNote: note };
}
function localDateKey(value: Date) {
  return `${value.getFullYear()}-${value.getMonth()}-${value.getDate()}`;
}
function entryDateLabel(
  entry: EntryListItem,
  previous: EntryListItem | undefined,
  locale: string,
  t: Translate,
) {
  if (!entry.timestamp) return undefined;
  const current = new Date(entry.timestamp);
  if (Number.isNaN(current.getTime())) return undefined;
  if (previous?.timestamp) {
    const before = new Date(previous.timestamp);
    if (
      !Number.isNaN(before.getTime()) &&
      localDateKey(before) === localDateKey(current)
    )
      return undefined;
  }
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(
    current.getFullYear(),
    current.getMonth(),
    current.getDate(),
  );
  const difference = Math.round((today.getTime() - day.getTime()) / 86400000);
  if (difference === 0) return t("today");
  if (difference === 1) return t("yesterday");
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(current);
}
function activityParts(entry: EntryListItem, t: Translate) {
  const primary = entry.primaryPreview.trim();
  if (entry.kind === "reasoning") return { label: "Reasoning:", body: primary };
  if (entry.kind === "tool" && entry.toolKind === "command")
    return {
      label: "Executing:",
      body: executedContent(primary) || t("commandUnavailable"),
    };
  if (entry.kind === "warning")
    return { label: `${t("warning")}:`, body: primary };
  if (entry.kind === "error")
    return { label: `${t("errorLabel")}:`, body: primary };
  return { label: primary ? `${entry.title}:` : entry.title, body: primary };
}
function firstActivityLine(value: string) {
  const lineBreak = value.search(/\r\n?|\n/);
  if (lineBreak < 0) return { text: value, truncated: false };
  const firstLine = value.slice(0, lineBreak);
  return {
    text: firstLine.endsWith("…") ? firstLine : `${firstLine}…`,
    truncated: true,
  };
}
export function executedContent(value: string) {
  if (!value) return "";
  try {
    return commandValue(JSON.parse(value)) || value;
  } catch {
    return value;
  }
}
function commandValue(value: unknown, depth = 0): string | undefined {
  if (depth > 4) return undefined;
  if (typeof value === "string") {
    try {
      return commandValue(JSON.parse(value), depth + 1) || value;
    } catch {
      return value;
    }
  }
  if (Array.isArray(value) && value.every((item) => typeof item === "string"))
    return value.join(" ");
  if (!value || typeof value !== "object") return undefined;
  const record = value as Record<string, unknown>;
  for (const key of ["cmd", "command"]) {
    const command = commandValue(record[key], depth + 1);
    if (command) return command;
  }
  for (const key of ["action", "input", "arguments"]) {
    const command = commandValue(record[key], depth + 1);
    if (command) return command;
  }
  return undefined;
}
