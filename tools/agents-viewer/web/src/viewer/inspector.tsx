import { Copy, X } from "lucide-react";
import { useEffect, useState } from "react";
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
