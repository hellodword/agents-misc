---
id: toolchain.ai-visual-review
kind: toolchain
triggers:
  - "AI visual review"
  - "screenshot review"
  - "visual QA"
  - "design critique"
  - "review mockup"
  - "visual review image edit"
summary: Gate explicit screenshot-based AI visual review and point to the workflow skill.
companions:
  required_rules:
    - toolchain.agent-tool-adapters
  skills:
    - id: ai-visual-review
      when: the user explicitly requests AI visual review, screenshot review, visual QA, design critique, or a screenshot-based review mockup or edit
  templates:
    - id: visual-review-finding.schema
      when: producing structured visual review findings
    - id: visual-review-synthesis.schema
      when: producing visual review synthesis
    - id: visual-review-rubric
      when: producing the shared review rubric
---

# AI Visual Review Rules

AI visual review is high cost and must not run automatically as part of ordinary UI work or E2E validation.

Use it only when the user explicitly asks for screenshot review, visual QA, visual critique, image-based UI review, or a mockup or exploratory edit tied to screenshots under review.

## Boundary

The main agent orchestrates; the workflow details live in `.agents/skills/ai-visual-review/SKILL.md`.

Keep visual review artifacts under:

```text
tmp/visual-review/<run-id>/
```

Do not commit screenshots, review artifacts, review mockups, or exploratory review edits by default.

Review-only sub-tasks must stay non-mutating. Workspace writes are allowed only for intentional artifacts under the visual review run directory.

## Consistency requirements

- Use a manifest with stable screenshot ids.
- Use one shared rubric across batches.
- Batch by page family, component family, or user flow.
- Require structured findings tied to screenshot ids.
- Synthesize findings into proposed findings. A review-only request does not authorize code changes.
- Implement only when the user requested review and implementation together, or later confirms the exact proposed findings to implement.
- Treat edited/generated images as design references, not source of truth.
