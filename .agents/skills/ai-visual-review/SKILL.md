---
name: ai-visual-review
description: Use this only when the user explicitly asks for AI visual review, screenshot review, visual QA, design critique from screenshots, or AI image editing. This is high cost and must not run automatically.
---

# AI Visual Review Workflow

## Purpose

Run high-cost screenshot-based review without overloading the main context and without producing inconsistent per-screenshot recommendations.

## Preconditions

- The user explicitly requested visual review, screenshot review, visual QA, design critique, visual alternatives, mockups, or image edits.
- The relevant UI/frontend rule has been loaded.
- `.agents/rules/toolchain/agent-tool-adapters.md` has been loaded before any one-shot review task is launched.
- Temporary output goes under `tmp/visual-review/<run-id>/`.

## Capture artifacts

Create this structure when screenshots are captured:

    tmp/visual-review/<run-id>/
      manifest.jsonl
      rubric.md
      screenshots/
      batches/
      findings/
      synthesis.json
      approved-issues.md
      final-report.md

Do not commit these artifacts by default.

Every screenshot manifest entry must include:

- stable screenshot id;
- route/page;
- component or flow name;
- viewport width/height;
- device class;
- locale;
- theme;
- UI state;
- screenshot path;
- image hash;
- capture command;
- timestamp;
- commit hash or working tree marker;
- notes about mocks/data.

## Batching

Do not send all screenshots to one model context.

Batch by page family, component family, or user flow:

- same route across viewport/locales/themes;
- same component across states;
- same user flow across steps.

Default maximum batch:

- one page family;
- up to four screenshots;
- one rubric;
- one compact product context.

If a batch exceeds context or image budget, split further but keep the same rubric and issue taxonomy.

## Rubric

Use one shared `rubric.md` for every batch.

The rubric must define:

- product goal;
- audience;
- design system constraints;
- severity scale;
- issue taxonomy;
- locales under review;
- viewport classes;
- known acceptable trade-offs;
- output schema.

## Structured findings

Sub-agent output must be structured.

Top-level JSON outputs must include `schema_version` with value `1`.

Each finding must include:

- finding id;
- screenshot id;
- page/component;
- severity;
- category;
- evidence;
- recommendation;
- affected viewport/locale/theme/state;
- duplicate candidate id if applicable;
- confidence;
- whether it needs human decision.

Use exactly these category values:

- `layout`
- `hierarchy`
- `spacing`
- `alignment`
- `typography`
- `color-contrast`
- `responsive-behavior`
- `i18n-copy`
- `accessibility`
- `state-handling`
- `consistency`
- `visual-polish`
- `possible-bug`

## Consistency controls

1. Use one shared rubric for every batch.
2. Use stable screenshot ids and metadata.
3. Use a fixed severity scale.
4. Use a fixed taxonomy.
5. Require evidence tied to screenshot ids.
6. Review page families together when possible.
7. Add a synthesis step that merges duplicates and resolves conflicts.
8. Do not implement fixes from per-screenshot findings directly.
9. Prefer issues reproduced across multiple viewports/locales over one-off subjective impressions.
10. If two findings conflict, keep both as `needs-human-decision` unless synthesis can resolve with evidence.
11. Maintain a final approved issue list.
12. Apply code changes only from the approved issue list.

## Adapter use

Before using adapter-specific commands or flags:

1. Probe the intended CLI with `command -v`.
2. Check the available help output when possible.
3. Use only flags shown by the current environment.
4. If schema enforcement is unavailable, write output under `tmp/visual-review/<run-id>/` and validate it separately.

Review-only tasks must stay non-mutating.

Workspace-write is allowed only for image editing or generated mockups that intentionally write under `tmp/visual-review/<run-id>/`.

Command shapes for Codex, OpenCode, and generic fallback workflows live in `.agents/references/agent-tool-adapter-examples.md`.

## AI image editing

Use AI image editing only when the user explicitly asks for visual alternatives, mockups, or image edits.

Do not use image editing as the default way to fix UI.

Image editing output belongs under:

    tmp/visual-review/<run-id>/image-edits/

Use edited images as design references, not as source of truth.

Before implementing, translate visual suggestions into design tokens, layout rules, copy changes, or component changes.

## Final report

Report:

- screenshots reviewed;
- batches run;
- approved findings;
- duplicate or rejected findings;
- conflicts needing user decision;
- implementation plan;
- files changed;
- validation performed.
