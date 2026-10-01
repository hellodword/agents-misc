---
name: long-task-plan
description: Plan autonomous long-running, unattended, resumable, or Codex Goal execution when the user explicitly requests that planning. Produce a self-contained plan that can adapt to discoveries while preserving completion criteria and authorization. Do not trigger for ordinary implementation, task complexity alone, or merely starting or resuming a Goal.
---

# Long Task Plan

Prepare a plan that lets an executor make useful decisions as evidence changes. Planning alone does not authorize implementation. Respect the executor's lifecycle, budgets, and available tools; do not create a Goal or invent a budget merely because the plan mentions one.

## Plan contents

Inspect enough of the repository to ground the approach and identify material unknowns. Make the plan self-contained for a later executor, with these elements at a level of detail proportionate to the task:

- **Goal and completion evidence:** requested user outcome, scope, exclusions, and observable evidence that would establish completion. Separate required results from preferred implementation choices.
- **Authorization and hard boundaries:** permission already granted, protected data or contracts, external actions needing approval, and applicable project constraints. Do not turn every current assumption into a hard constraint.
- **Current approach and milestones:** concrete next steps, later milestone outcomes, relevant entrypoints and validation commands, and important unknowns. For unresolved technical questions, include an investigation and decision criteria rather than pretending the answer is known.
- **Adjustment and blocking policy:** what the executor may change autonomously, which decisions need user input, and how to proceed when the current approach fails.
- **Resumable status:** completed work with evidence, material discoveries, approaches ruled out and why, unresolved issues, and the next useful action. Keep this concise enough to update during execution.

Return plan text by default. Save it when the user requests persistence or an existing task carrier requires it; use that carrier instead of inventing another state file. No fixed template, checkpoint count, per-step commit schedule, or exhaustive file list is required.

## Rules for execution under the plan

- Adapt implementation details, tools, and ordering when evidence supports a better route within authorization. Update the plan and record material reasons. Preserve the meaning of the goal and its completion criteria; weakening those needs user agreement.
- Treat missing development tools as ordinary environment work. Use the project development shell. Continue independent authorized work while a particular decision or external dependency is blocked.
- When attempts produce no new information, revisit the hypothesis, reduce to a focused experiment, or choose another approach. Avoid repeated retries and accumulating workarounds that do not advance the outcome. There is no universal failure-count gate; obey any executor-specific lifecycle rules.
- Evaluate a workaround by demonstrated behavior, compatibility, and maintenance cost. Keep a justified solution and record remaining trade-offs. For rejected approaches, preserve conclusions and references to evidence needed for reproduction or recovery.
- Ask the user when proceeding requires a change to the requested outcome, new authorization, or information/access that cannot be obtained within scope. Present the evidence, viable options, and a recommendation. Do not ask merely because an implementation choice was absent from the original plan.
- Validate the final state against the promised evidence. Distinguish implemented, unverified, blocked, budget-exhausted, and complete states. Commands attempted, time spent, or partial success do not establish completion. If execution stops early, preserve a truthful continuation record instead of claiming success.
