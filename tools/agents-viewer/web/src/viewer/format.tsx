import type {
  EntryListItem,
  SessionSummary,
  SourceKind,
  Status,
} from "@/generated/api";
import { ApiClientError } from "@/lib/api";
import i18n from "@/lib/i18n";

export function Empty({ text }: { text: string }) {
  return (
    <div className="empty" role="status">
      {text}
    </div>
  );
}
export function message(failure: unknown) {
  return failure instanceof ApiClientError
    ? failure.message
    : failure instanceof Error
      ? failure.message
      : i18n.t("unknownError");
}
export function localizedTitle(session: SessionSummary) {
  return session.title.startsWith("Untitled ·")
    ? `${i18n.t("untitled")} · ${new Date(session.createdAt).toLocaleString()}`
    : session.title;
}
export type Translate = (
  key: string,
  options?: Record<string, unknown>,
) => string;
export function sourceLabel(source: SourceKind, t: Translate) {
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
export function sourceHelp(source: SourceKind, t: Translate) {
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
export function sourceAvatar(source: SourceKind) {
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
export function friendlySessionTime(
  value: string,
  locale: string,
  t: Translate,
) {
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
export function indexStatusLabel(status: Status, t: Translate) {
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
export function indexPercent(status: Status) {
  if (status.phase === "discovering") return 35;
  if (status.progress.totalBytes > 0)
    return (status.progress.processedBytes / status.progress.totalBytes) * 100;
  if (status.progress.totalFiles > 0)
    return (status.progress.processedFiles / status.progress.totalFiles) * 100;
  return 100;
}
export function indexWindowLabel(
  status: Status,
  t: (key: string, options?: Record<string, unknown>) => string,
) {
  return status.initialIndexDays === -1
    ? t("allHistory")
    : status.initialIndexDays === 0
      ? t("newOnly")
      : t("dayWindow", { count: status.initialIndexDays });
}
export function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}
export function mergeEntries(first: EntryListItem[], second: EntryListItem[]) {
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
