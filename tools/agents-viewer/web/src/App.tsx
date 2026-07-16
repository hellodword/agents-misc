import { useVirtualizer } from "@tanstack/react-virtual";
import {
  ArrowDownToLine,
  ArrowUpToLine,
  Check,
  Copy,
  Filter,
  Menu,
  PanelRight,
  Search,
  X,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown from "react-markdown";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
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
import i18n, { setLanguage } from "@/lib/i18n";

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
  const [showTechnical, setShowTechnical] = useState(
    () => localStorage.getItem("agents-viewer-show-technical") === "true",
  );
  const [forcedTechnical, setForcedTechnical] = useState(false);
  const [theme, setTheme] = useState<"light" | "dark" | "system">(
    (localStorage.getItem("agents-viewer-theme") ?? "system") as
      | "light"
      | "dark"
      | "system",
  );
  const [selectedEntry, setSelectedEntry] = useState<{
    sessionId: string;
    entryId: string;
  }>();
  const [compactInspector, setCompactInspector] = useState(
    () => matchMedia("(max-width:1199px)").matches,
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
    setInspectorOpen(false);
    setSelectedEntry(undefined);
    setForcedTechnical(false);
  }, [location.pathname]);
  useEffect(() => {
    const keys: string[] = [];
    const handler = (event: KeyboardEvent) => {
      const input =
        event.target instanceof HTMLInputElement ||
        event.target instanceof HTMLTextAreaElement;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
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
  }, [closeSearch, openSearch, searchOpen]);
  const changeTheme = (value: string) => {
    setTheme(value as "light" | "dark" | "system");
    localStorage.setItem("agents-viewer-theme", value);
    document.documentElement.classList.toggle(
      "dark",
      value === "dark" ||
        (value === "system" &&
          matchMedia("(prefers-color-scheme:dark)").matches),
    );
  };
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
  const applyFilters = useCallback(
    (next: {
      archived: "exclude" | "include" | "only";
      source: string;
      cwd: string;
      showTechnical: boolean;
    }) => {
      setArchived(next.archived);
      setSource(next.source);
      setCwd(next.cwd);
      setShowTechnical(next.showTechnical);
      setForcedTechnical(false);
      localStorage.setItem(
        "agents-viewer-show-technical",
        String(next.showTechnical),
      );
    },
    [],
  );
  const effectiveTechnical = showTechnical || forcedTechnical;
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
          <span className="brand">{t("appName")}</span>
          <span className="top-spacer" />
          {status && (
            <div
              className="index-live"
              role="status"
              aria-live="polite"
              aria-atomic="true"
            >
              <Tooltip>
                <TooltipTrigger asChild>
                  <span tabIndex={0}>
                    <Badge
                      variant={
                        status.phase === "degraded" ? "destructive" : "outline"
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
          <FilterControl
            archived={archived}
            source={source}
            cwd={cwd}
            showTechnical={showTechnical}
            forcedTechnical={forcedTechnical}
            onApply={applyFilters}
          />
          <Separator orientation="vertical" className="h-5" />
          <div className="settings">
            <label className="sr-only" htmlFor="language">
              {t("language")}
            </label>
            <select
              id="language"
              className="select"
              value={i18n.language.startsWith("zh") ? "zh-CN" : "en"}
              onChange={(event) =>
                setLanguage(event.target.value as "en" | "zh-CN")
              }
            >
              <option value="en">{t("english")}</option>
              <option value="zh-CN">{t("chinese")}</option>
            </select>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm" aria-label={t("theme")}>
                  {t(theme)}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuLabel>{t("theme")}</DropdownMenuLabel>
                <DropdownMenuRadioGroup
                  value={theme}
                  onValueChange={changeTheme}
                >
                  <DropdownMenuRadioItem value="system">
                    {t("system")}
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="light">
                    {t("light")}
                  </DropdownMenuRadioItem>
                  <DropdownMenuRadioItem value="dark">
                    {t("dark")}
                  </DropdownMenuRadioItem>
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="inspector-button"
            aria-label={
              inspectorOpen ? t("closeInspector") : t("openInspector")
            }
            aria-expanded={inspectorOpen}
            aria-controls="entry-inspector"
            onClick={() => (inspectorOpen ? closeInspector() : openInspector())}
          >
            <PanelRight size={17} />
            <span className="desktop-only">{t("inspector")}</span>
          </Button>
        </header>
        {status &&
          (status.phase === "discovering" || status.phase === "indexing") && (
            <Progress
              aria-label={indexStatusLabel(status, t)}
              value={indexPercent(status)}
              className={`index-progress ${status.phase === "discovering" ? "indeterminate" : ""}`}
            />
          )}
        <ResizablePanelGroup orientation="horizontal" className="layout">
          <ResizablePanel
            defaultSize="300px"
            minSize="240px"
            maxSize="480px"
            className="sidebar"
          >
            <ScrollArea className="h-full">
              <aside aria-label={t("sessions")}>{sidebar}</aside>
            </ScrollArea>
          </ResizablePanel>
          <ResizableHandle withHandle className="sidebar-handle" />
          <ResizablePanel minSize="480px" className="main-panel">
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
                      showTechnical={effectiveTechnical}
                      onForceTechnical={setForcedTechnical}
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
          {inspectorOpen && !compactInspector && (
            <>
              <ResizableHandle withHandle className="inspector-handle" />
              <ResizablePanel
                defaultSize="360px"
                minSize="300px"
                maxSize="600px"
                className="inspector"
              >
                <ScrollArea className="h-full">
                  <aside id="entry-inspector" aria-label={t("inspector")}>
                    <Inspector
                      selected={selectedEntry}
                      onClose={closeInspector}
                    />
                  </aside>
                </ScrollArea>
              </ResizablePanel>
            </>
          )}
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
  showTechnical: boolean;
};

function FilterControl(
  props: FilterValues & {
    forcedTechnical: boolean;
    onApply: (values: FilterValues) => void;
  },
) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<FilterValues>({
    ...props,
    showTechnical: props.showTechnical || props.forcedTechnical,
  });
  const activeCount =
    Number(Boolean(props.source)) +
    Number(Boolean(props.cwd)) +
    Number(props.archived !== "exclude") +
    Number(props.showTechnical || props.forcedTechnical);
  const changeOpen = (next: boolean) => {
    if (next)
      setDraft({
        archived: props.archived,
        source: props.source,
        cwd: props.cwd,
        showTechnical: props.showTechnical || props.forcedTechnical,
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
                  ? t("filterActive", { count: activeCount })
                  : t("filter")
              }
            >
              <Filter size={15} />
              <span className="desktop-only">{t("filter")}</span>
              {activeCount > 0 && (
                <span className="filter-count" aria-hidden="true">
                  {activeCount}
                </span>
              )}
            </Button>
          </DialogTrigger>
        </TooltipTrigger>
        <TooltipContent>
          {activeCount
            ? t("filterActive", { count: activeCount })
            : t("filter")}
        </TooltipContent>
      </Tooltip>
      <DialogContent className="filter-dialog">
        <DialogHeader>
          <DialogTitle>{t("filter")}</DialogTitle>
          <DialogDescription>{t("filterHelp")}</DialogDescription>
        </DialogHeader>
        <form className="filter-form" onSubmit={apply}>
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
            <p className="filter-help">{t("archiveHelp")}</p>
          </fieldset>
          <fieldset>
            <legend>{t("conversationDisplay")}</legend>
            <label className="technical-filter" htmlFor="technical-filter">
              <input
                id="technical-filter"
                type="checkbox"
                checked={draft.showTechnical}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    showTechnical: event.target.checked,
                  }))
                }
              />
              <span>
                <strong>{t("showTechnical")}</strong>
                <small>
                  {props.forcedTechnical
                    ? t("showTechnicalForced")
                    : t("showTechnicalHelp")}
                </small>
              </span>
            </label>
          </fieldset>
          <DialogFooter className="filter-actions">
            <Button
              type="button"
              variant="ghost"
              onClick={() =>
                setDraft({
                  archived: "exclude",
                  source: "",
                  cwd: "",
                  showTechnical: false,
                })
              }
            >
              {t("reset")}
            </Button>
            <span className="filter-action-spacer" />
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

function Conversation({
  onInspect,
  signals,
  resyncSequence,
  showTechnical,
  onForceTechnical,
}: {
  onInspect: (s: string, e: string) => void;
  signals: Record<string, number>;
  resyncSequence: number;
  showTechnical: boolean;
  onForceTechnical: (value: boolean) => void;
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
  const [scrollTarget, setScrollTarget] = useState<ScrollTarget>();
  const viewport = useRef<ViewportState>({ atBottom: true });
  const requestSequence = useRef(0);
  const targetSequence = useRef(0);
  const loadingCursors = useRef(new Set<string>());
  const handledSignal = useRef(0);
  const refreshTimer = useRef<number | undefined>(undefined);

  useEffect(() => {
    const controller = new AbortController();
    setEntries([]);
    setPreviousCursor(undefined);
    setNextCursor(undefined);
    onForceTechnical(false);
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
        onForceTechnical(!isDefaultVisible(detail.item));
        setVisibilityReady(true);
      })
      .catch((f) => {
        if (!(f instanceof DOMException)) {
          setError(message(f));
          setVisibilityReady(true);
        }
      });
    return () => controller.abort();
  }, [around, onForceTechnical, sessionId]);

  const replacePage = useCallback(
    async (
      kind: "top" | "bottom" | "around",
      id?: string,
      signal?: AbortSignal,
    ) => {
      const request = ++requestSequence.current;
      const options =
        kind === "around" && id
          ? { limit: 100, aroundEntryId: id, includeTechnical: showTechnical }
          : {
              limit: 100,
              direction: kind === "top" ? "forward" : "backward",
              includeTechnical: showTechnical,
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
    [sessionId, showTechnical],
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
        includeTechnical: showTechnical,
      });
      setEntries((current) => mergeEntries(page.data, current));
      setPreviousCursor(page.previousCursor);
      setError("");
    } catch (f) {
      setError(message(f));
    } finally {
      loadingCursors.current.delete(cursor);
    }
  }, [previousCursor, sessionId, showTechnical]);

  const loadNewer = useCallback(async () => {
    const cursor = nextCursor;
    if (!cursor || loadingCursors.current.has(cursor)) return;
    loadingCursors.current.add(cursor);
    try {
      const page = await api.entries(sessionId, {
        cursor,
        limit: 100,
        includeTechnical: showTechnical,
      });
      setEntries((current) => mergeEntries(current, page.data));
      setNextCursor(page.nextCursor);
      setError("");
    } catch (f) {
      setError(message(f));
    } finally {
      loadingCursors.current.delete(cursor);
    }
  }, [nextCursor, sessionId, showTechnical]);

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
  const initialized = useRef(false);
  const atBottomRef = useRef(!around);
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
    const remaining = Math.max(
      0,
      element.scrollHeight - element.scrollTop - element.clientHeight,
    );
    const first = virtual
      .getVirtualItems()
      .find((row) => row.end >= element.scrollTop);
    const trueTop = element.scrollTop <= 80 && !hasOlder;
    const trueBottom = remaining <= 80 && !hasNewer;
    atBottomRef.current = trueBottom;
    setAtTop(trueTop);
    setAtBottom(trueBottom);
    onViewportChange?.({
      atBottom: trueBottom,
      anchorId: first ? entries[first.index]?.id : entries[0]?.id,
    });
  }, [entries, hasNewer, hasOlder, onViewportChange, virtual]);

  useEffect(() => {
    if (!scrollTarget || entries.length === 0) return;
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
    initialized.current = false;
    virtual.scrollToIndex(index, { align });
    let settleFrame = 0;
    const correctionFrame = requestAnimationFrame(() => {
      virtual.scrollToIndex(index, { align });
      settleFrame = requestAnimationFrame(() => {
        initialized.current = true;
        reportViewport();
      });
    });
    return () => {
      cancelAnimationFrame(correctionFrame);
      cancelAnimationFrame(settleFrame);
    };
  }, [entries, reportViewport, scrollTarget, virtual]);

  useEffect(() => {
    const anchor = restoreAnchor.current;
    if (!anchor) return;
    const index = entries.findIndex((entry) => entry.id === anchor.id);
    if (index < 0) {
      restoreAnchor.current = undefined;
      return;
    }
    virtual.scrollToIndex(index, { align: "start" });
    requestAnimationFrame(() => {
      const row = virtual
        .getVirtualItems()
        .find((item) => item.index === index);
      if (row && parent.current)
        parent.current.scrollTop = row.start - anchor.offset;
      restoreAnchor.current = undefined;
      reportViewport();
    });
  }, [entries, reportViewport, virtual]);

  const requestOlder = useCallback(async () => {
    if (!onLoadOlder || loadingOlder.current) return;
    const element = parent.current;
    const first = virtual.getVirtualItems()[0];
    if (element && first)
      restoreAnchor.current = {
        id: entries[first.index].id,
        offset: first.start - element.scrollTop,
      };
    loadingOlder.current = true;
    try {
      await onLoadOlder();
    } finally {
      loadingOlder.current = false;
    }
  }, [entries, onLoadOlder, virtual]);

  const requestNewer = useCallback(async () => {
    if (!onLoadNewer || loadingNewer.current) return;
    loadingNewer.current = true;
    try {
      await onLoadNewer();
    } finally {
      loadingNewer.current = false;
    }
  }, [onLoadNewer]);

  const handleScroll = useCallback(() => {
    reportViewport();
    const element = parent.current;
    if (!element || !initialized.current) return;
    const remaining = Math.max(
      0,
      element.scrollHeight - element.scrollTop - element.clientHeight,
    );
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
        >
          <div
            className="transcript-inner"
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
            <span className="sr-only">
              {entry.presentation === "user" ? t("user") : t("assistant")}:{" "}
            </span>
            <SafeMarkdown text={entry.primaryPreview} />
            <footer className="message-meta">
              <CopyMessageButton entry={entry} />
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

function CopyMessageButton({ entry }: { entry: EntryListItem }) {
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
      await navigator.clipboard.writeText(await fullPrimaryText(entry));
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

async function fullPrimaryText(entry: EntryListItem) {
  if (entry.primaryComplete) return entry.primaryPreview;
  let offset = 0;
  let text = "";
  for (;;) {
    const chunk = await api.content(
      entry.sessionId,
      entry.id,
      "primary",
      offset,
    );
    text += chunk.text;
    if (chunk.nextOffset === undefined) return text;
    if (chunk.nextOffset <= offset)
      throw new Error("content pagination did not advance");
    offset = chunk.nextOffset;
  }
}

export function SafeMarkdown({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <div className="markdown-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSanitize]}
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
