import type { EntryListItem } from "@/generated/api";

export type ThemeValue = "light" | "dark" | "system";

export type ConversationDisplayType =
  | "received"
  | "sent"
  | "requestUserInput"
  | "reasoning"
  | "exec"
  | "plan"
  | "patch"
  | "mcp"
  | "webSearch"
  | "function"
  | "dynamic"
  | "terminal"
  | "viewImage"
  | "otherTool"
  | "warning"
  | "error"
  | "context"
  | "marker"
  | "technicalMessage"
  | "internalMessage"
  | "unknown";

export const CONVERSATION_DISPLAY_OPTIONS: readonly {
  value: ConversationDisplayType;
  labelKey: string;
}[] = [
  { value: "received", labelKey: "displayReceived" },
  { value: "sent", labelKey: "displaySent" },
  { value: "requestUserInput", labelKey: "displayRequestUserInput" },
  { value: "reasoning", labelKey: "displayReasoning" },
  { value: "exec", labelKey: "displayExec" },
  { value: "plan", labelKey: "displayPlan" },
  { value: "patch", labelKey: "displayPatch" },
  { value: "mcp", labelKey: "displayMcp" },
  { value: "webSearch", labelKey: "displayWebSearch" },
  { value: "function", labelKey: "displayFunction" },
  { value: "dynamic", labelKey: "displayDynamic" },
  { value: "terminal", labelKey: "displayTerminal" },
  { value: "viewImage", labelKey: "displayViewImage" },
  { value: "otherTool", labelKey: "displayOtherTool" },
  { value: "warning", labelKey: "displayWarning" },
  { value: "error", labelKey: "displayError" },
  { value: "context", labelKey: "displayContext" },
  { value: "marker", labelKey: "displayMarker" },
  { value: "technicalMessage", labelKey: "displayTechnicalMessage" },
  { value: "internalMessage", labelKey: "displayInternalMessage" },
  { value: "unknown", labelKey: "displayUnknown" },
];
export const REQUIRED_CONVERSATION_DISPLAY_TYPES: readonly ConversationDisplayType[] =
  ["received", "sent", "requestUserInput", "plan"];
export const DEFAULT_CONVERSATION_DISPLAY_TYPES: readonly ConversationDisplayType[] =
  [...REQUIRED_CONVERSATION_DISPLAY_TYPES, "reasoning", "exec"];
export const CONVERSATION_DISPLAY_STORAGE_KEY =
  "agents-viewer-conversation-display-types";
export const conversationDisplayTypeSet = new Set(
  CONVERSATION_DISPLAY_OPTIONS.map(({ value }) => value),
);
export const requiredConversationDisplayTypeSet = new Set(
  REQUIRED_CONVERSATION_DISPLAY_TYPES,
);

export function canonicalConversationDisplayTypes(
  values: readonly ConversationDisplayType[],
) {
  const selected = new Set<ConversationDisplayType>([
    ...REQUIRED_CONVERSATION_DISPLAY_TYPES,
    ...values,
  ]);
  return CONVERSATION_DISPLAY_OPTIONS.map(({ value }) => value).filter(
    (value) => selected.has(value),
  );
}
export function storedConversationDisplayTypes() {
  const stored = localStorage.getItem(CONVERSATION_DISPLAY_STORAGE_KEY);
  if (stored === null) return [...DEFAULT_CONVERSATION_DISPLAY_TYPES];
  try {
    const values = JSON.parse(stored) as unknown;
    if (
      !Array.isArray(values) ||
      !values.every(
        (value): value is ConversationDisplayType =>
          typeof value === "string" &&
          conversationDisplayTypeSet.has(value as ConversationDisplayType),
      )
    )
      return [...DEFAULT_CONVERSATION_DISPLAY_TYPES];
    return canonicalConversationDisplayTypes(values);
  } catch {
    return [...DEFAULT_CONVERSATION_DISPLAY_TYPES];
  }
}

export function sameConversationDisplayTypes(
  left: readonly ConversationDisplayType[],
  right: readonly ConversationDisplayType[],
) {
  const canonicalLeft = canonicalConversationDisplayTypes(left);
  const canonicalRight = canonicalConversationDisplayTypes(right);
  return (
    canonicalLeft.length === canonicalRight.length &&
    canonicalLeft.every((value, index) => value === canonicalRight[index])
  );
}

export function withConversationDisplayType(
  values: readonly ConversationDisplayType[],
  value?: ConversationDisplayType,
) {
  return canonicalConversationDisplayTypes(value ? [...values, value] : values);
}

export const SIDEBAR_DEFAULT_WIDTH = 300;
export const SIDEBAR_MIN_WIDTH = 240;
export const SIDEBAR_MAX_WIDTH = 480;
export const INSPECTOR_DEFAULT_WIDTH = 360;
export const INSPECTOR_MIN_WIDTH = 300;
export const INSPECTOR_MAX_WIDTH = 600;

export function storedTheme(): ThemeValue {
  const value = localStorage.getItem("agents-viewer-theme");
  return value === "light" || value === "dark" || value === "system"
    ? value
    : "system";
}

export function storedSidebarWidth() {
  const value = Number(localStorage.getItem("agents-viewer-sidebar-width"));
  return Number.isFinite(value) && value >= SIDEBAR_MIN_WIDTH
    ? Math.min(SIDEBAR_MAX_WIDTH, value)
    : SIDEBAR_DEFAULT_WIDTH;
}

export function conversationDisplayType(
  entry: EntryListItem,
): ConversationDisplayType {
  if (entry.kind === "message") {
    if (entry.presentation === "user") return "sent";
    if (entry.presentation === "response") return "received";
    if (entry.presentation === "internal") return "internalMessage";
    return "technicalMessage";
  }
  if (entry.kind === "tool") {
    switch (entry.toolKind) {
      case "requestUserInput":
        return "requestUserInput";
      case "command":
        return "exec";
      case "patch":
        return "patch";
      case "mcp":
        return "mcp";
      case "webSearch":
        return "webSearch";
      case "function":
        return "function";
      case "dynamic":
        return "dynamic";
      case "terminal":
        return "terminal";
      case "viewImage":
        return "viewImage";
      default:
        return "otherTool";
    }
  }
  switch (entry.kind) {
    case "reasoning":
      return "reasoning";
    case "plan":
      return "plan";
    case "warning":
      return "warning";
    case "error":
      return "error";
    case "context":
      return "context";
    case "marker":
      return "marker";
    default:
      return "unknown";
  }
}
export function isDefaultVisible(entry: EntryListItem) {
  return (
    entry.kind === "reasoning" ||
    entry.kind === "plan" ||
    entry.kind === "warning" ||
    entry.kind === "error" ||
    (entry.kind === "message" &&
      (entry.presentation === "user" || entry.presentation === "response")) ||
    (entry.kind === "tool" &&
      (entry.toolKind === "command" || entry.toolKind === "requestUserInput"))
  );
}
