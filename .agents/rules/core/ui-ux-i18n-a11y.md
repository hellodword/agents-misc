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
summary: Keep UI changes accessible, localized, responsive, and state-aware.
companions: []
---

# UI/UX, i18n, and Accessibility Rules

## UX defaults

- Start from the user task, not from component inventory.
- Design the main happy path first.
- Then cover:
  - empty state;
  - loading state;
  - error state;
  - disabled state;
  - permission-denied state;
  - offline/unavailable state;
  - validation state.
- Prefer simple layouts and predictable navigation.
- Default to responsive layouts.
- Keep copy concise and specific.
- Prefer existing project components and design tokens.
- Do not add a UI framework by default unless the chosen stack requires it.

## Accessibility defaults

Target WCAG 2.2 AA-style discipline for ordinary web app UI unless the project specifies another target.

Default checklist:

- use semantic HTML where possible;
- associate labels with form controls;
- preserve visible focus states;
- keep keyboard navigation usable;
- ensure logical focus order;
- do not rely on color alone;
- provide actionable validation messages;
- keep touch targets reasonable;
- avoid focus traps;
- avoid excessive animation;
- provide text alternatives for meaningful images;
- ensure dialogs expose name, role, and state;
- ensure error messages are discoverable by assistive technologies.

## i18n defaults

Default locales:

- English: `en`
- Simplified Chinese: `zh-CN`

For Vue projects:

- use `vue-i18n` by default;
- keep user-facing strings in locale message files for durable apps;
- avoid concatenating translated fragments when sentence order may differ;
- use interpolation for variables;
- keep date, time, number, currency, and pluralization behavior explicit;
- document fallback locale behavior;
- test missing-key behavior;
- keep locale switching visible or clearly documented when user-facing.

For React SPA projects:

- prefer `react-i18next`.

For Next.js projects:

- prefer `next-intl`.

For small demos:

- bilingual copy may be inline when no durable i18n system exists;
- once the UI grows beyond a small demo, migrate user-facing strings into the i18n structure.

## Copy rules

- Prefer short labels.
- Prefer action-oriented button text.
- Error messages should say what failed and how to fix it.
- Use consistent terminology across English and Chinese.
- Do not expose internal exception names to users.
