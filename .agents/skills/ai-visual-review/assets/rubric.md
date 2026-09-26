# Visual Review Rubric

## Product context

- Product:
- Audience:
- Primary task:
- Design-system constraints:
- Known acceptable trade-offs:

## Scope

- Routes/pages:
- Components:
- Locales:
- Themes:
- Viewports:
- States:

## Severity

- P0: Blocks the user task or makes the UI unusable.
- P1: Serious visual, accessibility, or responsive defect.
- P2: Noticeable quality defect that should be fixed.
- P3: Minor polish suggestion.

## Categories

Use exactly: `layout`, `hierarchy`, `spacing`, `alignment`, `typography`, `color-contrast`, `responsive-behavior`, `i18n-copy`, `accessibility`, `state-handling`, `consistency`, `visual-polish`, or `possible-bug`.

## Review constraints

- Cite stable screenshot IDs and visible evidence.
- Avoid subjective preferences without product evidence.
- Prefer issues reproduced across viewports, locales, themes, or states.
- Mark unresolved trade-offs as needing human decision.
- Treat synthesis as proposed findings; review does not authorize implementation.

## Finding template

Use a short Markdown entry for each finding:

- **Screenshot / location:** Screenshot ID and affected area.
- **Severity:** P0, P1, P2, or P3 from the shared scale.
- **Evidence:** The visible issue and relevant viewport, state, or other context.
- **Recommendation:** A concrete improvement supported by that evidence.

For the combined report, merge duplicates while retaining their screenshot references. List unresolved conflicts and rejected findings separately when present.
