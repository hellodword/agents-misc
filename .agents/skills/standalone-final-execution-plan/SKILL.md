---
name: standalone-final-execution-plan
description: Produce a complete, self-contained, copyable execution plan with evidence, steps, validation, commit strategy, and recovery. Use only when the user explicitly asks for a final complete, standalone, unattended, no-hidden-context, or reusable plan; do not use for implementation work or routine summaries.
---

# Standalone Final Execution Plan

1. Confirm the user explicitly requested a complete standalone or copyable plan rather than implementation.
2. Read and use [the plan template](assets/plan-template.md), omitting only sections that truly do not apply.
3. Make the answer understandable without conversation history, hidden reasoning, or unstated preferences.
4. Separate observed evidence from assumptions. Ask before deciding a missing public-contract, persistent-data, dependency, security, external-effect, or long-term stack choice.
5. Define a target behavior and the smallest implementation design that meets it.
6. Split steps only into independently implementable and verifiable semantic units. Include files, actions, exact validation, acceptance criteria, and staging notes for each.
7. Add checkpoints at natural risk boundaries or after every three normal steps.
8. State commit mode. Auto-commit only when the user, task, or project policy authorizes it; otherwise give exact deferred paths and suggested messages.
9. Include recovery when data, migration, public contracts, or hard-to-reverse operations are involved.
10. Use the user's language while preserving conventional file paths, commands, APIs, and identifiers.
11. Before answering, verify that evidence, assumptions, validation, acceptance, commit policy, risks, and non-goals are explicit.
