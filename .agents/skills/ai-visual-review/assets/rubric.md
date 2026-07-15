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

- Use `visual-review-finding/v1` for batch output and `visual-review-synthesis/v1` for synthesis.
- Cite stable screenshot IDs and visible evidence.
- Avoid subjective preferences without product evidence.
- Prefer issues reproduced across viewports, locales, themes, or states.
- Mark unresolved trade-offs as needing human decision.
- Treat synthesis as proposed findings; review does not authorize implementation.
