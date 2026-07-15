---
name: ai-visual-review
description: Review screenshots in consistent batches, synthesize evidence-based visual findings, and optionally create review mockups. Use only when the user explicitly asks for AI visual review, screenshot QA, design critique, or screenshot-based mockups; do not trigger for ordinary screenshots, browser tests, or implementation review.
---

# AI Visual Review

1. Confirm that the user explicitly requested screenshot-based visual review, QA, critique, or a review mockup. Keep a review-only request non-mutating.
2. Create temporary output under a confirmed ignored `tmp/agent/visual-review/<run-id>/` path. Do not commit captures, batches, findings, traces, or edited images.
3. Copy and complete [the shared rubric](assets/rubric.md) once for the run. Keep its severity scale and category taxonomy identical across batches.
4. Record a stable screenshot ID, route/component, viewport, locale, theme, UI state, file path, image hash, capture command, timestamp, source revision, and fixture notes for every capture.
5. Batch by page family, component family, or user flow. Default to no more than one family and four screenshots per batch.
6. When platform and task policy allow native delegation, delegate independent batches with the same rubric and schema. Otherwise review the batches sequentially in the main context. Never invent adapter commands or vendor-specific flags.
7. Validate each batch against [the finding schema](assets/finding.schema.json). Tie every finding to screenshot evidence and keep subjective preference out unless the product context supports it.
8. Merge duplicates and conflicts across batches, then validate the result against [the synthesis schema](assets/synthesis.schema.json).
9. Keep contradictory evidence as a human decision unless synthesis resolves it from the shared rubric and captures.
10. Do not implement per-batch findings directly. For review-only work, report proposals and stop. Implement only when the user also requested changes or later authorizes the exact findings.
11. Use image editing only for an explicitly requested review mockup or exploratory screenshot edit. Keep it in the temporary run directory and translate accepted ideas into code/design-system changes before implementation.
12. Report screenshots reviewed, batches, proposed findings, duplicates/rejections, human decisions, proposed implementation, files changed, and validation.
