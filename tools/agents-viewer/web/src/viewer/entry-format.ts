import type { EntryListItem } from "@/generated/api";
import type { Translate } from "@/viewer/format";

export function localDateKey(value: Date) {
  return `${value.getFullYear()}-${value.getMonth()}-${value.getDate()}`;
}
export function entryDateLabel(
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
export function activityParts(entry: EntryListItem, t: Translate) {
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
export function firstActivityLine(value: string) {
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
export function commandValue(value: unknown, depth = 0): string | undefined {
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
