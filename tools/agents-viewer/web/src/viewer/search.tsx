import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router-dom";
import {
  CommandDialog,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Input } from "@/components/ui/input";
import type { SearchHit } from "@/generated/api";
import { api } from "@/lib/api";
import { localizedTitle, message } from "@/viewer/format";

export function SearchDialog({
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

export function SearchPage() {
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
export function useSearchAllTypes() {
  const [allTypes, setAllTypesState] = useState(
    () => localStorage.getItem("agents-viewer-search-all-types") === "true",
  );
  const setAllTypes = useCallback((value: boolean) => {
    setAllTypesState(value);
    localStorage.setItem("agents-viewer-search-all-types", String(value));
  }, []);
  return [allTypes, setAllTypes] as const;
}
