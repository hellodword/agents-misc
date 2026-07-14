---
id: core.ui-ux-i18n-a11y
kind: core
triggers:
  - "UI"
  - "UX"
  - "i18n"
  - "a11y"
  - "accessibility"
  - "responsive"
summary: Keep applicable UI states accessible, localized, responsive, and consistent with project conventions.
companions: {}
---

# UI/UX, i18n, and Accessibility Rules

## Product states

Start from the user task and main path. Add empty, loading, error, disabled, permission-denied, offline/unavailable, validation, and responsive behavior only when the product can actually enter that state. Do not invent fake permission, offline, or network behavior to satisfy a checklist.

Prefer existing components, tokens, navigation patterns, and concise product terminology. Do not add a UI framework without a stack decision that requires it.

## Accessibility

For ordinary web UI, implement the relevant WCAG 2.2 Level A and AA success criteria for the changed behavior. Do not claim WCAG conformance without a conformance audit covering the declared scope.

As applicable:

- use semantic HTML and programmatic names/labels;
- preserve visible keyboard focus and logical focus order;
- make functionality keyboard-operable without focus traps;
- do not use color as the only signal;
- meet applicable text, non-text, focus-indicator, and UI-component contrast criteria;
- provide actionable validation messages that assistive technology can discover;
- meet WCAG 2.2 target-size requirements or document the applicable exception;
- respect reduced-motion preferences and avoid harmful or unnecessary motion;
- provide text alternatives for meaningful images;
- expose dialog name, role, state, initial focus, and focus return correctly.

## Internationalization

Preserve an existing project's locales, message library, fallback rules, and formatting conventions.

For greenfield user-facing UI, create a locale-message structure for `en` and `zh-CN` from the start. Do not inline bilingual durable UI copy as a substitute for locale resources.

- In greenfield Vue work, use `vue-i18n` unless the user selects another approach.
- In greenfield React SPA work, use `react-i18next` unless the user selects another approach.
- In greenfield Next.js work, use `next-intl` unless the user selects another approach.
- Avoid concatenating translated fragments whose order can vary.
- Use interpolation and locale-aware date, time, number, currency, and plural formatting.
- Define fallback and missing-key behavior and test it when the application exposes locale switching.

## Copy

Use short, action-oriented labels. Error text should say what failed and how to recover. Keep terminology consistent across locales and do not expose internal exception names.
