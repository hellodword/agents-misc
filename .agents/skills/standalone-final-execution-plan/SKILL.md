---
name: standalone-final-execution-plan
description: Use only when the user explicitly asks for a final complete plan, standalone plan, unattended execution plan, copyable execution plan, or no-hidden-context answer. Do not use for ordinary coding tasks, routine final reports, or brief implementation summaries.
---

# Standalone Final Execution Plan

## Purpose

Use this skill only when the final deliverable is a complete, standalone execution plan that can be copied, reviewed, shared, or reused independently.

This skill controls answer structure. It does not decide the technical solution. The solution must still follow the user's request, repository rules, loaded task-specific rules, and available evidence.

Do not use this skill for normal code modification reports, routine summaries, or ordinary step-by-step implementation work unless the user explicitly asks for a standalone plan.

## Explicit trigger phrases

Load this skill when the user asks for one of:

- final complete plan;
- standalone plan;
- unattended execution plan;
- copyable execution plan;
- no-hidden-context answer;
- a plan that another agent or human can execute without the conversation.

Do not infer this skill merely from a complex task.

## Required companion files

Use `.agents/templates/final-execution-plan.md` as the output structure.

Use `.agents/references/final-execution-plan-examples.md` only when examples are needed.

## Standalone requirement

The final answer must be understandable after reading only that answer.

Do not rely on:

- previous conversation turns;
- hidden reasoning;
- unstated assumptions;
- implicit user preferences;
- phrases such as "as mentioned above" or "previously".

If missing information affects public behavior, persistent data, dependencies, security, external effects, or a long-term stack choice, ask the user before finalizing the plan. For reversible local implementation detail, use the smallest convention-aligned assumption and state it under `Default Assumptions`.

## Language

Use the language of the user's current request unless the user specifies another output language.

Keep file paths, commands, package names, API names, protocol names, and configuration keys in their conventional form.

## Tone

Use final-solution tone.

The answer should describe the goal, design, evidence, execution method, validation method, commit strategy, and acceptance criteria.

Do not write the final answer as a patch, diff, incremental change note, or conversation-dependent explanation.

## Required sections

Include these sections when relevant:

- Goal
- Scope
- Non-goals
- Default Assumptions
- Evidence
- Target Behavior
- Design
- Files and Directories
- Workload Estimate
- Step Splitting Decision
- Execution Steps
- Validation Plan
- Acceptance Criteria
- Commit Mode and Commit Strategy
- Rollback or Recovery
- Risks and Mitigations

Omit a section only when it is clearly irrelevant.

## Evidence rules

Separate facts from assumptions.

Use concrete evidence when available:

- file paths;
- observed repository structure;
- commands already run;
- errors or logs;
- APIs, schemas, migrations, or contracts;
- user-stated constraints.

Label unsupported but necessary claims as assumptions.

## Workload estimate

Estimate work before defining steps.

Use these buckets:

- XS: one local change, one narrow validation.
- S: a few files, one subsystem, narrow validation.
- M: multiple files or subsystems, coordinated validation.
- L: migrations, generated artifacts, compatibility risk, or several independently verifiable slices.
- XL: broad architectural or multi-phase work requiring explicit checkpoints.

## Step splitting

A normal execution step is an independent, verifiable, semantically complete unit of implementation.

Do not split implementation, repair, validation, and acceptance of the same unit into separate normal steps.

Split the plan when there are independent feature slices, separate backend/frontend slices, migration plus dependent behavior, generated artifacts plus consumers, pure patch phases, or separate compatibility/security/data-risk repairs.

If the plan is not split, state why a single normal execution step is safe.

## Step structure

Each execution step should include:

- Step ID;
- Status;
- Goal;
- Files or areas;
- Actions;
- Validation;
- Acceptance criteria;
- Commit or staging note.

Use these status values:

- `pending`
- `in_progress`
- `blocked`
- `failed`
- `repairing`
- `completed`
- `verified`
- `committed`
- `commit_deferred`

For a plan that has not been executed, initial normal step status should usually be `pending`.

## Checkpoints

For plans with three or more normal execution steps, add checkpoint acceptance after every three normal steps or at natural risk boundaries.

A checkpoint must verify integration across the completed steps before continuing.

If a checkpoint fails, repair and revalidate before continuing.

## Commit policy

Automatic commit mode is active only when the user explicitly requests commits, the task prompt says auto-commit, or the repository has an explicit agent auto-commit policy.

When automatic commit mode is active:

- commit each verified normal step;
- commit checkpoint repairs separately after repair validation;
- stage only explicit file paths;
- never use bulk staging.

When automatic commit mode is not active:

- do not commit automatically;
- provide changed files;
- provide exact files that would be staged;
- provide validation performed;
- provide suggested commit messages.

Before any commit, run `git status --short`. If unrelated user changes are present and cannot be cleanly separated, defer the commit and report the intended staging paths.

## Validation and acceptance

Every normal step must have its own validation and acceptance criteria.

Prefer narrow validation that directly covers the touched behavior.

Escalate to full-repository validation only for build/CI wiring, lockfile or dependency-graph changes, public/shared contracts, migration infrastructure, generated infrastructure, or an explicit project requirement.

Do not weaken tests to hide failures.

## Final checklist

Before producing the answer, verify that:

- the answer is standalone;
- the user's requested language is used;
- assumptions are explicit;
- evidence is concrete or clearly labeled;
- workload and split decisions are stated;
- every step has validation and acceptance criteria;
- commit mode is explicit;
- bulk staging is forbidden;
- rollback or recovery is included when useful;
- the answer avoids references to previous conversation context.
