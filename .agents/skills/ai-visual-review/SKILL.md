---
name: ai-visual-review
description: Use this only when the user explicitly asks for AI visual review, screenshot review, visual QA, design critique from screenshots, or AI image editing. This is high cost and must not run automatically.
---

# AI Visual Review Workflow

## Purpose

Run high-cost screenshot-based review without overloading the main context and without producing inconsistent per-screenshot recommendations.

## Workflow

1. Confirm the user explicitly requested visual review or image editing.
2. Capture screenshots into `tmp/visual-review/<run-id>/screenshots/`.
3. Create `manifest.jsonl` with screenshot id, route, viewport, locale, theme, state, path, hash, and capture command.
4. Create one shared `rubric.md`.
5. Batch screenshots by page family, component family, or user flow.
6. Use one-shot sub-agent tasks such as `codex exec` for each batch.
7. Require structured findings that reference screenshot ids.
8. Do not implement per-batch recommendations directly.
9. Run a synthesis sub-agent to merge duplicates, resolve conflicts, and produce an approved issue list.
10. Apply code changes only from the approved issue list.
11. Use AI image editing only when explicitly requested; store outputs under `tmp/visual-review/<run-id>/image-edits/`.
12. Treat edited images as references, not source of truth.

## Consistency controls

- One shared rubric.
- Stable screenshot ids.
- Fixed severity scale.
- Fixed issue taxonomy.
- Evidence tied to screenshot ids.
- Page-family review where possible.
- Synthesis before implementation.
- Conflicts marked `needs-human-decision`.

## Output

Produce a final report with:

- screenshots reviewed;
- batches run;
- approved findings;
- duplicate/rejected findings;
- conflicts needing user decision;
- implementation plan;
- validation performed.
