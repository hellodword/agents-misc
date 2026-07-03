---
id: toolchain.ai-visual-review
kind: toolchain
triggers:
  - 'AI visual review'
  - 'screenshot review'
  - 'visual QA'
  - 'design critique'
  - 'image editing'
---

# AI Visual Review Rules

## Trigger

AI visual review is high cost.

Use it only when the user explicitly asks for:

- AI visual review;
- visual review;
- screenshot review;
- visual QA;
- design critique from screenshots;
- image-based UI review;
- AI image editing;
- mockup generation from screenshots.

Do not run AI visual review automatically as part of ordinary E2E.

## Separation from main context

Do not perform screenshot-heavy visual review directly in the main coding context.

The main agent is the orchestrator:

1. capture screenshots;
2. write a manifest;
3. write a shared rubric;
4. launch one-shot sub-agent tasks through the available tool adapter;
5. collect structured findings;
6. synthesize conflicts;
7. decide a single implementation plan;
8. apply code changes only after synthesis.

## Directory layout

Use a run directory:

    tmp/visual-review/<run-id>/
      manifest.jsonl
      rubric.md
      screenshots/
      findings/
      synthesis/
      image-edits/
      final-report.md

Do not commit visual review artifacts by default.

## Screenshot manifest

Every screenshot entry must include:

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

## Review batching

Do not send all screenshots to one model context.

Batch by page family:

- same route across viewport/locales/themes;
- same component across states;
- same user flow across steps.

Default maximum batch:

- 1 page family;
- up to 4 screenshots;
- one rubric;
- one compact product context.

If a batch exceeds context or image budget, split further but keep the same rubric and issue taxonomy.

## Required shared rubric

Every sub-agent review must receive the same `rubric.md`.

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

## Output format

Sub-agent output must be structured.

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

To avoid inconsistent review results across separate screenshot batches:

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

## Tool adapters

### Codex

Use `codex exec` for single-shot visual review tasks when available.

Example review command shape:

    codex exec \
      --sandbox read-only \
      --output-schema .agents/templates/visual-review-finding.schema.json \
      -o tmp/visual-review/<run-id>/findings/<batch-id>.json \
      "Review the screenshots listed in <batch-manifest> using <rubric>. Return only structured findings."

Example synthesis command shape:

    codex exec \
      --sandbox read-only \
      --output-schema .agents/templates/visual-review-synthesis.schema.json \
      -o tmp/visual-review/<run-id>/synthesis/final.json \
      "Merge visual review findings, deduplicate, resolve conflicts, and produce one approved implementation plan."

### OpenCode

Use `opencode run` for single-shot visual review tasks when available.

Example review command shape:

    opencode run \
      "Review the screenshots listed in <batch-manifest> using <rubric>. Return only JSON that matches .agents/templates/visual-review-finding.schema.json." \
      > tmp/visual-review/<run-id>/findings/<batch-id>.json

Example synthesis command shape:

    opencode run \
      "Merge visual review findings, deduplicate, resolve conflicts, and return only JSON that matches .agents/templates/visual-review-synthesis.schema.json." \
      > tmp/visual-review/<run-id>/synthesis/final.json

### Generic fallback

If the current agent cannot spawn subagents or enforce output schemas, perform the review in the main context with smaller batches, manually validate the JSON shape against the template, and report the limitation.

Use read-only execution for review-only tasks.

Use workspace-write only for image editing or generated mockups that intentionally write under `tmp/`.

## AI image editing

Use AI image editing only when the user explicitly asks for visual alternatives, mockups, or image edits.

Do not use image editing as the default way to fix UI.

Image editing output belongs under:

    tmp/visual-review/<run-id>/image-edits/

Use edited images as design references, not as source of truth.

Before implementing, translate visual suggestions into design tokens, layout rules, copy changes, or component changes.

## Final report

The main agent should produce:

- screenshots reviewed;
- batches run;
- approved findings;
- rejected or duplicate findings;
- conflicts needing user decision;
- implementation plan;
- files changed;
- validation performed.
