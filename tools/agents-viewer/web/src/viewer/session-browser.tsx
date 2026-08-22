import { useTranslation } from "react-i18next";
import { Link, useLocation } from "react-router-dom";
import { Skeleton } from "@/components/ui/skeleton";
import type { SessionGroup, SessionTreeNode } from "@/generated/api";
import {
  Empty,
  friendlySessionTime,
  localizedTitle,
  sourceAvatar,
  sourceLabel,
} from "@/viewer/format";

export function SessionSidebar(props: {
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
export function SessionTreeItem({
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
