import { PanelRight } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { EntryListItem } from "@/generated/api";
import { CopyMessageButton, fullPrimaryText } from "@/viewer/clipboard";
import {
  activityParts,
  entryDateLabel,
  firstActivityLine,
} from "@/viewer/entry-format";
import { EntryTime } from "@/viewer/entry-time";
import { SafeMarkdown } from "@/viewer/markdown";
import {
  RequestUserInputMessages,
  requestUserInputDetails,
} from "@/viewer/request-user-input";

export function TranscriptEntryView({
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
    entry.kind === "plan" ||
    (entry.kind === "message" &&
      (entry.presentation === "user" || entry.presentation === "response"));
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

export function MessageBubbleContent({
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
      <MessageAttachmentBadges metadata={entry.metadata} />
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

export function MessageAttachmentBadges({
  metadata,
}: {
  metadata: Record<string, unknown>;
}) {
  const { t } = useTranslation();
  const imageCount = attachmentCount(metadata, "imageAttachmentCount");
  const audioCount = attachmentCount(metadata, "audioAttachmentCount");
  const totalCount = attachmentCount(metadata, "attachmentCount");
  const otherCount = Math.max(0, totalCount - imageCount - audioCount);
  if (imageCount === 0 && audioCount === 0 && otherCount === 0) return null;

  return (
    <div className="message-attachments" role="list" aria-label={t("attachments")}>
      {imageCount > 0 && (
        <span className="message-attachment-badge" role="listitem">
          {t("imageAttachments", { count: imageCount })}
        </span>
      )}
      {audioCount > 0 && (
        <span className="message-attachment-badge" role="listitem">
          {t("audioAttachments", { count: audioCount })}
        </span>
      )}
      {otherCount > 0 && (
        <span className="message-attachment-badge" role="listitem">
          {t("otherAttachments", { count: otherCount })}
        </span>
      )}
    </div>
  );
}

export function attachmentCount(metadata: Record<string, unknown>, key: string) {
  const value = metadata[key];
  return typeof value === "number" &&
    Number.isSafeInteger(value) &&
    value > 0
    ? value
    : 0;
}
