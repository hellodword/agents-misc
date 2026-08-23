import { Settings as SettingsIcon } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { SourceKind } from "@/generated/api";
import { preferredLanguage, type SupportedLanguage } from "@/lib/i18n";
import { sourceHelp, sourceLabel } from "@/viewer/format";
import {
  CONVERSATION_DISPLAY_OPTIONS,
  DEFAULT_CONVERSATION_DISPLAY_TYPES,
  canonicalConversationDisplayTypes,
  requiredConversationDisplayTypeSet,
  sameConversationDisplayTypes,
  type ConversationDisplayType,
  type ThemeValue,
} from "@/viewer/preferences";

export const sourceValues: SourceKind[] = [
  "cli",
  "vscode",
  "exec",
  "review",
  "subagent",
  "appServer",
  "unknown",
];

export type FilterValues = {
  archived: "exclude" | "include" | "only";
  source: string;
  cwd: string;
  conversationDisplayTypes: ConversationDisplayType[];
};

export type SettingsValues = FilterValues & {
  theme: ThemeValue;
  language: SupportedLanguage;
  searchCtrlShiftF: boolean;
};

export function SettingsControl(
  props: SettingsValues & {
    forcedConversationDisplayType?: ConversationDisplayType;
    onApply: (values: SettingsValues) => void;
  },
) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<SettingsValues>({
    ...props,
    conversationDisplayTypes: [...props.conversationDisplayTypes],
  });
  const activeCount =
    Number(Boolean(props.source)) +
    Number(Boolean(props.cwd)) +
    Number(props.archived !== "exclude") +
    Number(
      !sameConversationDisplayTypes(
        props.conversationDisplayTypes,
        DEFAULT_CONVERSATION_DISPLAY_TYPES,
      ),
    );
  const changeOpen = (next: boolean) => {
    if (next)
      setDraft({
        archived: props.archived,
        source: props.source,
        cwd: props.cwd,
        conversationDisplayTypes: [...props.conversationDisplayTypes],
        theme: props.theme,
        language: props.language,
        searchCtrlShiftF: props.searchCtrlShiftF,
      });
    setOpen(next);
  };
  const apply = (event: FormEvent) => {
    event.preventDefault();
    props.onApply(draft);
    setOpen(false);
  };
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <Tooltip>
        <TooltipTrigger asChild>
          <DialogTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              aria-label={
                activeCount
                  ? t("settingsActive", { count: activeCount })
                  : t("settings")
              }
            >
              <SettingsIcon size={15} />
              <span className="desktop-only">{t("settings")}</span>
              {activeCount > 0 && (
                <span className="settings-count" aria-hidden="true">
                  {activeCount}
                </span>
              )}
            </Button>
          </DialogTrigger>
        </TooltipTrigger>
        <TooltipContent>
          {activeCount
            ? t("settingsActive", { count: activeCount })
            : t("settings")}
        </TooltipContent>
      </Tooltip>
      <DialogContent className="settings-dialog">
        <DialogHeader>
          <DialogTitle>{t("settings")}</DialogTitle>
          <DialogDescription>{t("settingsHelp")}</DialogDescription>
        </DialogHeader>
        <form className="settings-form" onSubmit={apply}>
          <fieldset>
            <legend>{t("sessionFilters")}</legend>
            <label htmlFor="source-filter">{t("source")}</label>
            <select
              id="source-filter"
              className="select"
              value={draft.source}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  source: event.target.value,
                }))
              }
            >
              <option value="">{t("allSources")}</option>
              {sourceValues.map((value) => (
                <option key={value} value={value}>
                  {sourceLabel(value, t)}
                </option>
              ))}
            </select>
            <details className="source-help">
              <summary>{t("sourceHelp")}</summary>
              <dl>
                {sourceValues.map((value) => (
                  <div key={value}>
                    <dt>{sourceLabel(value, t)}</dt>
                    <dd>{sourceHelp(value, t)}</dd>
                  </div>
                ))}
              </dl>
            </details>
            <label htmlFor="cwd-filter">{t("cwd")}</label>
            <Input
              id="cwd-filter"
              value={draft.cwd}
              onChange={(event) =>
                setDraft((current) => ({ ...current, cwd: event.target.value }))
              }
              placeholder={t("cwdPlaceholder")}
            />
            <label htmlFor="archive-filter">{t("archived")}</label>
            <select
              id="archive-filter"
              className="select"
              value={draft.archived}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  archived: event.target.value as FilterValues["archived"],
                }))
              }
            >
              <option value="exclude">{t("archiveActive")}</option>
              <option value="include">{t("archiveInclude")}</option>
              <option value="only">{t("archiveOnly")}</option>
            </select>
            <p className="settings-help">{t("archiveHelp")}</p>
          </fieldset>
          <fieldset>
            <legend>{t("conversationDisplay")}</legend>
            <p className="settings-help">{t("conversationDisplayHelp")}</p>
            <div className="conversation-display-types">
              {CONVERSATION_DISPLAY_OPTIONS.map(({ value, labelKey }) => {
                const required = requiredConversationDisplayTypeSet.has(value);
                return (
                  <label
                    className={`conversation-display-type ${required ? "conversation-display-type-required" : ""}`}
                    key={value}
                  >
                    <input
                      type="checkbox"
                      checked={draft.conversationDisplayTypes.includes(value)}
                      disabled={required}
                      onChange={(event) =>
                        setDraft((current) => ({
                          ...current,
                          conversationDisplayTypes:
                            canonicalConversationDisplayTypes(
                              event.target.checked
                                ? [...current.conversationDisplayTypes, value]
                                : current.conversationDisplayTypes.filter(
                                    (candidate) => candidate !== value,
                                  ),
                            ),
                        }))
                      }
                    />
                    <span>{t(labelKey)}</span>
                  </label>
                );
              })}
            </div>
            <p className="settings-help">{t("requiredDisplayTypesHelp")}</p>
            {props.forcedConversationDisplayType && (
              <p className="settings-help forced-display-type" role="status">
                {t("displayTypeForced", {
                  type: t(
                    CONVERSATION_DISPLAY_OPTIONS.find(
                      ({ value }) =>
                        value === props.forcedConversationDisplayType,
                    )?.labelKey ?? "displayUnknown",
                  ),
                })}
              </p>
            )}
          </fieldset>
          <fieldset>
            <legend>{t("appearance")}</legend>
            <label htmlFor="language-setting">{t("language")}</label>
            <select
              id="language-setting"
              className="select"
              value={draft.language}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  language: event.target.value as SupportedLanguage,
                }))
              }
            >
              <option value="en">{t("english")}</option>
              <option value="zh-CN">{t("chinese")}</option>
            </select>
            <label htmlFor="theme-setting">{t("theme")}</label>
            <select
              id="theme-setting"
              className="select"
              value={draft.theme}
              onChange={(event) =>
                setDraft((current) => ({
                  ...current,
                  theme: event.target.value as ThemeValue,
                }))
              }
            >
              <option value="system">{t("system")}</option>
              <option value="light">{t("light")}</option>
              <option value="dark">{t("dark")}</option>
            </select>
          </fieldset>
          <fieldset>
            <legend>{t("keyboard")}</legend>
            <label className="technical-filter" htmlFor="search-shortcut">
              <input
                id="search-shortcut"
                type="checkbox"
                checked={draft.searchCtrlShiftF}
                onChange={(event) =>
                  setDraft((current) => ({
                    ...current,
                    searchCtrlShiftF: event.target.checked,
                  }))
                }
              />
              <span>
                <strong>{t("searchShortcut")}</strong>
                <small>{t("searchShortcutHelp")}</small>
              </span>
            </label>
          </fieldset>
          <DialogFooter className="settings-actions">
            <Button
              type="button"
              variant="ghost"
              onClick={() =>
                setDraft({
                  archived: "exclude",
                  source: "",
                  cwd: "",
                  conversationDisplayTypes: [
                    ...DEFAULT_CONVERSATION_DISPLAY_TYPES,
                  ],
                  theme: "system",
                  language: preferredLanguage(),
                  searchCtrlShiftF: false,
                })
              }
            >
              {t("reset")}
            </Button>
            <span className="settings-action-spacer" />
            <Button
              type="button"
              variant="outline"
              onClick={() => setOpen(false)}
            >
              {t("cancel")}
            </Button>
            <Button type="submit">{t("apply")}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
