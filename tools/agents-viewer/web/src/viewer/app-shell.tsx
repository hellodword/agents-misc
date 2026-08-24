import { Menu, PanelLeftClose, PanelLeftOpen, Search } from "lucide-react";
import { Navigate, Route, Routes } from "react-router-dom";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { Conversation } from "@/viewer/conversation";
import { useViewerController } from "@/viewer/controller";
import {
  Empty,
  formatBytes,
  indexPercent,
  indexStatusLabel,
  indexWindowLabel,
} from "@/viewer/format";
import { Inspector } from "@/viewer/inspector";
import {
  INSPECTOR_MAX_WIDTH,
  INSPECTOR_MIN_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
} from "@/viewer/preferences";
import { SearchDialog, SearchPage } from "@/viewer/search";
import { SessionSidebar } from "@/viewer/session-browser";
import { SettingsControl } from "@/viewer/settings";

export function AppShell() {
  const {
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
    sessionSyncSignals,
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
  } = useViewerController();
  const sidebar = (
    <SessionSidebar
      groups={sessionGroups}
      loading={loading}
      error={error}
      hasMore={Boolean(sessionNextCursor)}
      loadingMore={loadingMoreSessions}
      onLoadMore={() => void loadMoreSessions()}
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
                      syncSignals={sessionSyncSignals}
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
