---
name: ai-visual-review
description: Review screenshots with a consistent rubric and synthesize evidence-based visual findings. Use only when the user explicitly asks for AI visual review, screenshot QA, design critique, or screenshot-based mockups; do not trigger for ordinary screenshots, browser tests, or implementation review.
---

# AI Visual Review

1. Establish the requested review scope and whether implementation is also authorized. Keep a review-only request non-mutating with respect to the product.
2. Use the task's temporary workspace for review captures and mockups. Distinguish these outputs from intentionally maintained visual test baselines.
3. Read [the shared rubric](assets/rubric.md) and apply its severity scale and categories consistently. Fill in product context relevant to the findings; a small review does not require a copied report template.
4. Give each screenshot a stable reference and record enough context to locate or reproduce an issue, such as route, viewport, theme, or UI state. Add revision, capture command, fixtures, or hashes when provenance matters.
5. Group larger reviews by user flow or related views, with batches small enough to inspect reliably. Compare related screenshots together and merge duplicate findings while preserving references.
6. Report visible evidence, severity, and a concrete recommendation. Distinguish subjective preferences and uncertainties; keep unresolved conflicts visible when the captures cannot resolve them.
7. Implement findings only within existing user authorization. Use image editing for requested mockups.
8. Summarize the views reviewed, actionable findings, and material gaps. Report changes and validation when implementation was included.
