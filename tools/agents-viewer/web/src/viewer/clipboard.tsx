import { Check, Copy } from "lucide-react";
import {
  isValidElement,
  useCallback,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import type { EntryListItem } from "@/generated/api";
import { api } from "@/lib/api";

export function CopyMessageButton({
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

export async function fullPrimaryText(entry: EntryListItem, signal?: AbortSignal) {
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

export type ClipboardCopyState = "idle" | "copying" | "copied" | "failed";

export function useClipboardCopy() {
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

export function copyStateLabel(
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

export function reactNodeText(node: ReactNode): string {
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
