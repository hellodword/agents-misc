import { Copy, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import type { ContentChunk, RawRecord, TranscriptEntry } from "@/generated/api";
import { api } from "@/lib/api";
import { Empty, message } from "@/viewer/format";

export function Inspector({
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
  const [rawLoading, setRawLoading] = useState(false);
  const [error, setError] = useState("");
  const rawController = useRef<AbortController | undefined>(undefined);
  useEffect(() => {
    rawController.current?.abort();
    rawController.current = undefined;
    setDetail(undefined);
    setPrimary(undefined);
    setSecondary(undefined);
    setRaw(undefined);
    setRawLoading(false);
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
    return () => {
      controller.abort();
      rawController.current?.abort();
    };
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
  const loadRaw = async (rawId: string, offset?: number) => {
    if (!selected) return;
    rawController.current?.abort();
    const controller = new AbortController();
    rawController.current = controller;
    const current = offset === undefined ? undefined : raw;
    if (!current) setRaw(undefined);
    setRawLoading(true);
    setError("");
    try {
      const next = await api.raw(
        selected.sessionId,
        rawId,
        offset ?? 0,
        controller.signal,
      );
      if (rawController.current !== controller) return;
      if (current && offset !== undefined) {
        setRaw({
          summary: next.summary,
          chunk: {
            ...next.chunk,
            byteOffset: current.chunk.byteOffset,
            text: current.chunk.text + next.chunk.text,
            complete:
              current.chunk.byteOffset === 0 &&
              next.chunk.nextOffset === undefined,
          },
        });
      } else {
        setRaw(next);
      }
    } catch (failure) {
      if (
        rawController.current === controller &&
        !(failure instanceof DOMException && failure.name === "AbortError")
      )
        setError(message(failure));
    } finally {
      if (rawController.current === controller) {
        rawController.current = undefined;
        setRawLoading(false);
      }
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
            onClick={() => void loadRaw(ref.id)}
          >
            #{ref.line} {ref.envelopeType}
          </button>
        ))}
        {error && <p className="error">{error}</p>}
        {rawLoading && <p role="status">{t("loading")}</p>}
        {raw && <pre className="raw-content">{raw.chunk.text}</pre>}
        {raw?.chunk.nextOffset !== undefined && (
          <Button
            variant="outline"
            size="sm"
            disabled={rawLoading}
            onClick={() => void loadRaw(raw.summary.id, raw.chunk.nextOffset)}
          >
            {t("loadMore")}
          </Button>
        )}
      </div>
    </>
  );
}
