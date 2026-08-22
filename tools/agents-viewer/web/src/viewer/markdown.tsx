import { Check, Copy } from "lucide-react";
import { createContext, useContext, type ComponentProps } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type ExtraProps } from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import rehypeSanitize from "rehype-sanitize";
import remarkGfm from "remark-gfm";
import { Button } from "@/components/ui/button";
import {
  copyStateLabel,
  reactNodeText,
  useClipboardCopy,
} from "@/viewer/clipboard";

export const MarkdownCodeBlockContext = createContext(false);

export function MarkdownCodeBlock({
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
