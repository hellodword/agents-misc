import { Check, PanelRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import type { EntryListItem } from "@/generated/api";
import { EntryTime } from "@/viewer/entry-time";

export function RequestUserInputMessages({
  entryId,
  details,
  highlighted,
  locale,
  timestamp,
  onInspect,
}: {
  entryId: string;
  details: RequestUserInputDetails;
  highlighted: boolean;
  locale: string;
  timestamp?: Date;
  onInspect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const legacyNotes = legacyRequestUserInputNotes(details);
  return (
    <div className="request-user-input-message-group">
      {details.questions.map((question, questionIndex) => {
        const answer = visibleRequestUserInputAnswer(details, question);
        const options = requestUserInputOptions(question, answer.selections);
        const firstSelected = options.find((option) => option.selected)?.label;
        const note =
          answer.note ??
          (legacyNotes.targetId === question.id ? legacyNotes.note : undefined);
        const pollTitleId = `${entryId}-poll-${questionIndex}`;
        return (
          <article
            data-transcript-entry
            className="message-row message-assistant request-user-input-message"
            aria-current={highlighted || undefined}
            aria-labelledby={pollTitleId}
            key={`${question.id}-${questionIndex}`}
          >
            <div className="message-bubble request-user-input-poll">
              <div className="request-user-input-poll-title" id={pollTitleId}>
                {question.question}
              </div>
              <ul className="request-user-input-options">
                {options.map((option, optionIndex) => (
                  <li
                    className={`request-user-input-option${option.selected ? " is-selected" : ""}`}
                    key={`${option.label}-${optionIndex}`}
                  >
                    <span
                      className="request-user-input-radio"
                      aria-hidden="true"
                    >
                      {option.selected && <Check size={12} strokeWidth={3} />}
                    </span>
                    <span className="request-user-input-option-copy">
                      <span className="request-user-input-option-label">
                        {option.label}
                      </span>
                      {option.description && (
                        <>
                          <span aria-hidden="true"> — </span>
                          <span className="request-user-input-option-description">
                            {option.description}
                          </span>
                        </>
                      )}
                      {option.selected && (
                        <span className="sr-only"> {t("selected")}</span>
                      )}
                      {note && option.label === firstSelected && (
                        <span className="request-user-input-option-note">
                          {note}
                        </span>
                      )}
                    </span>
                  </li>
                ))}
              </ul>
              {note && firstSelected === undefined && (
                <div className="request-user-input-unassigned-note">{note}</div>
              )}
              {questionIndex === details.questions.length - 1 &&
                legacyNotes.footerNote && (
                  <div className="request-user-input-legacy-note">
                    <span>notes:</span> {legacyNotes.footerNote}
                  </div>
                )}
              <footer className="message-meta">
                <Button
                  variant="ghost"
                  size="icon-xs"
                  className="message-action"
                  aria-label={`${t("openInspector")}: ${question.question}`}
                  onClick={() => onInspect(entryId)}
                >
                  <PanelRight size={13} />
                </Button>
                {timestamp && <EntryTime value={timestamp} locale={locale} />}
              </footer>
            </div>
          </article>
        );
      })}
    </div>
  );
}

export type RequestUserInputOption = {
  label: string;
  description: string;
};
export type RequestUserInputQuestion = {
  id: string;
  question: string;
  isSecret: boolean;
  options: RequestUserInputOption[];
};
export type RequestUserInputAnswer = {
  selections: string[];
  note?: string;
};
export type RequestUserInputDetails = {
  questions: RequestUserInputQuestion[];
  answers: Map<string, RequestUserInputAnswer>;
  legacyNotes?: string;
};
export function requestUserInputDetails(
  entry: EntryListItem,
): RequestUserInputDetails | undefined {
  if (entry.kind !== "tool" || entry.toolKind !== "requestUserInput")
    return undefined;
  const rawQuestions = entry.metadata.requestUserInputQuestions;
  if (!Array.isArray(rawQuestions)) return undefined;
  const questions = rawQuestions.flatMap(
    (value): RequestUserInputQuestion[] => {
      const question = recordValue(value);
      if (
        !question ||
        typeof question.id !== "string" ||
        typeof question.question !== "string"
      )
        return [];
      const options = Array.isArray(question.options)
        ? question.options.flatMap((candidate): RequestUserInputOption[] => {
            const option = recordValue(candidate);
            if (
              !option ||
              typeof option.label !== "string" ||
              typeof option.description !== "string"
            )
              return [];
            return [{ label: option.label, description: option.description }];
          })
        : [];
      return [
        {
          id: question.id,
          question: question.question,
          isSecret: question.isSecret === true,
          options,
        },
      ];
    },
  );
  if (questions.length === 0) return undefined;
  const answers = new Map<string, RequestUserInputAnswer>();
  const rawAnswers = recordValue(entry.metadata.requestUserInputAnswers);
  for (const question of questions) {
    const answer = recordValue(rawAnswers?.[question.id]);
    if (!Array.isArray(answer?.answers)) continue;
    const selections: string[] = [];
    let note: string | undefined;
    for (const value of answer.answers) {
      if (typeof value !== "string") continue;
      const noteText = value.startsWith("user_note: ")
        ? value.slice("user_note: ".length).trim()
        : undefined;
      if (noteText !== undefined) {
        if (noteText) note = noteText;
      } else {
        selections.push(value);
      }
    }
    answers.set(question.id, { selections, note });
  }
  const legacyNotes = entry.metadata.requestUserInputNotes;
  return {
    questions,
    answers,
    legacyNotes: typeof legacyNotes === "string" ? legacyNotes : undefined,
  };
}
export function recordValue(
  value: unknown,
): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}
export function visibleRequestUserInputAnswer(
  details: RequestUserInputDetails,
  question: RequestUserInputQuestion,
): RequestUserInputAnswer {
  if (question.isSecret) return { selections: [] };
  return details.answers.get(question.id) ?? { selections: [] };
}
export function requestUserInputOptions(
  question: RequestUserInputQuestion,
  selections: string[],
) {
  const selected = new Set(selections);
  const known = new Set(question.options.map((option) => option.label));
  const resultOnly = selections
    .filter(
      (label, index) =>
        !known.has(label) && selections.indexOf(label) === index,
    )
    .map((label) => ({ label, description: "" }));
  return [...question.options, ...resultOnly].map((option) => ({
    ...option,
    selected: selected.has(option.label),
  }));
}
export function legacyRequestUserInputNotes(details: RequestUserInputDetails): {
  targetId?: string;
  note?: string;
  footerNote?: string;
} {
  const note = details.legacyNotes?.trim();
  if (
    !note ||
    details.questions.some((question) => question.isSecret) ||
    [...details.answers.values()].some((answer) => answer.note)
  )
    return {};
  const answered = details.questions.filter(
    (question) =>
      (details.answers.get(question.id)?.selections.length ?? 0) > 0,
  );
  const target =
    details.questions.length === 1
      ? details.questions[0]
      : answered.length === 1
        ? answered[0]
        : undefined;
  return target ? { targetId: target.id, note } : { footerNote: note };
}
