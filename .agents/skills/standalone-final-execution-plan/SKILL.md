---
name: standalone-final-execution-plan
description: Produce a complete, standalone final execution plan with evidence, workload estimation, step splitting, status tracking, validation checkpoints, and per-step precise commits. Use when the user asks for a final complete plan, a standalone plan, an unattended execution plan, a no-hidden-knowledge answer, detailed evidence, or a non-patch-style final answer. Default output language is English unless the user requests another language.
---

# Standalone Final Execution Plan

## Purpose

Use this skill when the final deliverable must be a complete, standalone execution plan that can be copied, reviewed, shared, or reused independently.

This skill controls the form, completeness, evidence quality, execution structure, validation discipline, and commit discipline of the final answer. It does not decide the technical solution by itself. The actual solution must still follow the user's request, the available project information, repository rules, and any task-specific instructions.

## Core Requirement

The final answer must be complete and standalone.

A reader should be able to understand, review, and execute the plan after reading only the final answer.

The answer must not depend on:

- previous conversation turns;
- hidden reasoning;
- unstated assumptions;
- implicit user preferences;
- "as mentioned above";
- current chat context.

If important information is missing, make the most reasonable safe assumption and state it explicitly under `Default Assumptions`.

Do not block the final answer only because some details are missing, unless the task would be unsafe, impossible, or fundamentally ambiguous.

## Language

Default final answer language: English.

If the user requests a specific language, use that language.

Examples of user language requests include:

- "give the final plan in Chinese";
- "给出最终的中文完整方案";
- "用中文输出";
- "write the plan in Japanese";
- "respond in English".

Keep actual names, commands, file paths, configuration keys, APIs, package names, protocols, standards, and technical terms in their conventional form.

When the user requests Chinese, write clear, direct, professional Chinese.

## Tone

Use final-solution tone.

The answer should describe the intended final state, design, execution method, evidence, validation method, commit strategy, and acceptance criteria.

Do not write the final answer as:

- a patch;
- a diff;
- an incremental change note;
- a conversation-dependent explanation;
- a summary of what changed from an earlier version.

Prefer wording such as:

- "The final plan is...";
- "The goal is...";
- "The design is...";
- "The evidence is...";
- "The execution steps are...";
- "The validation method is...";
- "The acceptance criteria are...".

When writing in Chinese, prefer wording such as:

- "最终方案是……";
- "该方案的目标是……";
- "设计如下……";
- "依据如下……";
- "执行步骤如下……";
- "验收标准如下……".

Avoid using the following as the main structure:

- "change A to B";
- "remove the previous...";
- "add the following patch...";
- "according to the above...";
- "as previously mentioned...";
- "this patch...";
- "these changes...".

It is acceptable to mention concrete file creation or modification when necessary, but frame it as part of the final implementation plan, not as a patch explanation.

## Standalone Context Rule

The final answer must include all context required to understand the plan.

When relevant, explicitly include:

- goal;
- background;
- scope;
- non-goals;
- constraints;
- default assumptions;
- target behavior;
- files or directories involved;
- commands to run;
- configuration names;
- version requirements;
- workload estimate;
- step splitting decision;
- execution steps;
- per-step status;
- per-step acceptance criteria;
- checkpoint acceptance criteria;
- commit strategy;
- validation method;
- final acceptance criteria;
- evidence;
- risks;
- rollback or recovery method.

Do not rely on references such as:

- "above";
- "previously";
- "the current context";
- "as already discussed";
- "the earlier plan".

## Evidence Rule

The final plan must include concrete evidence for important claims and decisions whenever possible.

Evidence should answer questions such as:

- Why is this plan necessary?
- Which files, modules, functions, routes, commands, settings, or documents are involved?
- Which observed facts support the proposed design?
- Which constraints come from source code, configuration, documentation, logs, tests, or user-provided requirements?
- Which risks are proven or suggested by concrete evidence?

Prefer concrete evidence such as:

- file paths;
- line numbers or line ranges;
- function, class, type, route, command, or configuration names;
- command outputs;
- test results;
- error messages;
- logs;
- schema fields;
- API names;
- version numbers;
- commit, issue, pull request, or release references;
- official documentation links;
- relevant source links;
- user-provided constraints that materially affect the solution.

For code-related evidence, prefer this style:

    path/to/file.ext:123-145: This shows the existing entry point, so the new feature should reuse it.

For reference links, prefer this style:

    Official documentation link: This supports the required option, so the plan can use it directly.

Do not invent evidence.

Do not invent file paths, line numbers, command outputs, logs, or links.

If exact evidence is unavailable, clearly label the item as:

- assumption;
- inference;
- item requiring verification.

## Evidence Placement Rule

For technical or investigative plans, include a dedicated section named one of:

- `## Evidence`
- `## Basis and Evidence`
- `## Current State and Evidence`

When writing in Chinese, use one of:

- `## 证据`
- `## 依据与证据`
- `## 现状与证据`

Use the shortest section name that fits the task.

Evidence should be concise but specific. Avoid dumping excessive raw content.

Each evidence item should explain why it matters to the plan.

A good evidence item has this shape:

    Evidence location: Observable fact → Impact on the plan.

Example:

    backend/jobs/video.go:42-68: Existing background job scheduling entry point → The new job should reuse this scheduler instead of introducing a second framework.

## Workload Estimation Rule

Before writing execution steps, estimate the workload.

Use relative effort, not a promise of wall-clock completion time.

Prefer this format:

| Item                      | Estimate                                              |
| ------------------------- | ----------------------------------------------------- |
| Scope                     | small / medium / large / extra-large                  |
| Risk                      | low / medium / high                                   |
| Affected areas            | list of files, modules, services, tools, or workflows |
| Expected number of steps  | number                                                |
| Split required            | yes / no                                              |
| Reason for split decision | concise reason                                        |

Recommended sizing:

- `small`: one focused change, one area, low risk, usually 1-2 steps.
- `medium`: multiple files or one cross-boundary behavior, usually 3-5 steps.
- `large`: multiple subsystems, generated artifacts, migrations, E2E, security, or compatibility risk, usually 6-10 steps.
- `extra-large`: long-running migration, large upstream patch workflow, multi-platform client, or high-risk automation, usually split into phases.

Split the plan into steps when any of the following is true:

- expected steps exceed 2;
- multiple subsystems are involved;
- database/schema/API/config/CLI contracts change;
- generated artifacts are involved;
- tests require multiple layers;
- security or compatibility risk exists;
- unattended execution is expected;
- the task is too large to safely commit as one unit.

If the plan is not split, explicitly state why a single step is safe.

## Step Status Rule

Every execution step must include a status.

Use this status vocabulary:

- `pending`: not started;
- `in_progress`: currently being executed;
- `blocked`: cannot proceed without external input or environment change;
- `failed`: validation failed;
- `repairing`: fixing a failed validation or checkpoint;
- `completed`: implementation work for the step is done;
- `verified`: implementation and validation for the step passed;
- `committed`: the step has been precisely staged and committed.

In a final plan that has not been executed yet, initial step statuses should usually be `pending`.

For unattended execution, each step should define how the status changes:

    pending -> in_progress -> completed -> verified -> committed

If validation fails:

    completed -> failed -> repairing -> completed -> verified -> committed

If the task cannot continue safely:

    in_progress -> blocked

## Step Structure Rule

Each step must be concrete and executable.

Use this structure for each step:

| Field           | Required content                                            |
| --------------- | ----------------------------------------------------------- |
| Step ID         | stable ID, such as `S1`, `S2`, `S3`                         |
| Status          | one of the approved status values                           |
| Goal            | what this step achieves                                     |
| Files or areas  | exact files when known; otherwise explicit discovery target |
| Actions         | concrete operations                                         |
| Validation      | exact commands or manual checks                             |
| Step acceptance | observable criteria for passing this step                   |
| Commit          | exact staging rule and commit message pattern               |

Avoid vague step actions such as:

- "adjust related code";
- "update configuration";
- "handle edge cases";
- "run necessary tests".

Replace them with concrete details whenever possible.

## Per-Step Validation Rule

Every step must have its own validation.

Validation must explain:

- what to run;
- what to inspect;
- what result means success;
- what result means failure;
- where outputs or logs go;
- what to do when validation fails.

If automated validation is unavailable, provide manual validation steps.

If validation fails, the executor must not proceed to the next normal step.

The executor must:

1. mark the step `failed`;
2. diagnose the failure;
3. mark the step `repairing`;
4. repair the issue;
5. rerun the step validation;
6. continue only after the step reaches `verified`.

## Checkpoint Rule

For unattended or multi-step execution, add checkpoint acceptance after every 3 normal steps.

If the plan has 1-2 steps:

- no checkpoint is required;
- still include per-step validation and final acceptance.

If the plan has 3 or more steps:

- add `Checkpoint C1` after steps `S1-S3`;
- add `Checkpoint C2` after steps `S4-S6`;
- add `Checkpoint C3` after steps `S7-S9`;
- continue the pattern for longer plans.

Each checkpoint must include:

- covered steps;
- required validation commands or manual checks;
- expected success result;
- failure handling;
- commit handling for checkpoint repairs.

If a checkpoint fails, do not continue to the next step group.

The executor must:

1. mark the checkpoint `failed`;
2. identify the failing step or integration point;
3. create a repair sub-step, such as `C1-R1`;
4. apply the minimal repair;
5. validate again;
6. precisely stage and commit the repair;
7. mark the checkpoint `verified`.

Checkpoint repairs must use their own commits.

## Commit Rule

Every completed step must be precisely staged and committed.

Do not rely on `.gitignore` as the only safety mechanism.

After each step reaches `verified`, the executor must:

1. run `git status --short`;
2. inspect the relevant diffs;
3. stage only explicit file paths that belong to the current step;
4. never run:
   - `git add .`;
   - `git add -A`;
   - `git add --all`;
   - any equivalent bulk staging command;
5. never stage ignored paths;
6. commit non-interactively.

Use the repository's commit convention.

If no repository convention is known, use Conventional Commits with a concise imperative subject.

Recommended commit header format:

    type(scope): subject

or:

    type: subject

Allowed default types:

- `feat`;
- `fix`;
- `chore`;
- `docs`;
- `refactor`;
- `test`.

The plan must include a proposed commit message for each step.

If a checkpoint repair is required, it must also be committed with a separate precise commit.

If commits are not allowed by user instruction, execution mode, or environment, the plan must state that and provide:

- files to stage;
- proposed commit message;
- validation performed;
- reason commit is deferred.

## Unattended Execution Rule

When the plan may be used for fully automated unattended execution, include strict safeguards.

The plan must define:

- stop conditions;
- repair loop behavior;
- maximum scope of automatic repair;
- when to stop and report `blocked`;
- what must never be done automatically;
- how to avoid destructive operations;
- how to avoid committing secrets or runtime artifacts.

Default stop conditions:

- missing required credentials;
- unclear destructive data operation;
- failed migration on non-disposable data;
- unexpected untracked secret or database file;
- validation failure that cannot be repaired within the current step;
- test failure outside the planned scope;
- command requiring system-level or global environment modification;
- user decision required by compatibility, security, licensing, or data-loss risk.

Unattended execution must not:

- install global tools;
- run host package managers;
- execute curl/wget-to-shell installers;
- delete user data;
- rewrite history;
- force-add ignored paths;
- commit secrets, logs, screenshots, databases, coverage, browser traces, or local environment files;
- run long-running full builds indefinitely.

## Repair Rule

When validation or checkpoint acceptance fails, repair must be explicit and minimal.

For each failure, classify it as one of:

- implementation defect;
- test defect;
- unclear requirement;
- environment blocker;
- dependency/tooling gap;
- unrelated existing failure.

Then choose one action:

- fix implementation;
- fix the test if the test is wrong;
- update the plan if the original assumption was wrong;
- mark blocked if external input or environment change is required;
- isolate unrelated existing failure and report it.

After repair:

- rerun the smallest relevant validation;
- update status;
- precisely stage files;
- commit the repair;
- continue only when validation passes.

## Evidence of Completion Rule

For each step and checkpoint, the final plan should define what evidence proves completion.

Examples:

- command output showing success;
- test command and expected pass result;
- file path and expected content;
- generated artifact path and reproducibility command;
- commit hash after execution;
- screenshot or report path under a temporary directory;
- database migration version record;
- API contract updated path.

Do not invent future command outputs or commit hashes.

Use placeholders only when the value will be produced during execution, such as:

    Commit hash: produced after execution.

## Final Plan Structure

For complex technical tasks, prefer this structure:

    # Final Plan

    ## Goal

    ## Scope

    ## Non-goals

    ## Default Assumptions

    ## Constraints

    ## Evidence

    ## Workload Estimate and Split Decision

    ## Final Design

    ## Execution Plan

    ### Step Table

    ### Step Details

    ## Checkpoint Acceptance

    ## Commit Plan

    ## Validation Plan

    ## Final Acceptance Criteria

    ## Risks and Safeguards

    ## Rollback or Recovery Plan

When writing in Chinese, prefer:

    # 最终方案

    ## 目标

    ## 范围

    ## 非目标

    ## 默认假设

    ## 约束

    ## 依据与证据

    ## 工作量估算与拆分判断

    ## 最终设计

    ## 执行计划

    ### 步骤总表

    ### 步骤详情

    ## 阶段性验收

    ## 提交计划

    ## 验证计划

    ## 最终验收标准

    ## 风险与保护措施

    ## 回滚或恢复方案

For smaller tasks, a shorter structure is acceptable, but it must still be standalone, evidenced, executable, and independently understandable.

Do not mechanically include irrelevant sections.

## Step Table Template

Use a table like this when the task has multiple steps:

    | Step | Status | Goal | Files or areas | Validation | Commit |
    |---|---|---|---|---|---|
    | S1 | pending | ... | ... | ... | `type(scope): subject` |
    | S2 | pending | ... | ... | ... | `type(scope): subject` |
    | S3 | pending | ... | ... | ... | `type(scope): subject` |
    | C1 | pending | Checkpoint for S1-S3 | ... | ... | repair commit only if needed |

When writing in Chinese:

    | 步骤 | 状态 | 目标 | 文件或区域 | 验证 | 提交 |
    |---|---|---|---|---|---|
    | S1 | pending | ... | ... | ... | `type(scope): subject` |
    | S2 | pending | ... | ... | ... | `type(scope): subject` |
    | S3 | pending | ... | ... | ... | `type(scope): subject` |
    | C1 | pending | S1-S3 阶段性验收 | ... | ... | 仅修复时提交 |

## Commit Plan Template

Include a commit plan like this:

    | Step | Files to stage | Commit message | Commit condition |
    |---|---|---|---|
    | S1 | exact paths | `type(scope): subject` | after S1 validation passes |
    | S2 | exact paths | `type(scope): subject` | after S2 validation passes |
    | C1-R1 | exact repair paths | `fix(scope): repair checkpoint failure` | only if C1 fails and repair passes |

When exact files are not known before discovery, state the discovery rule:

    Files to stage: exact files changed by S1 only, determined by `git status --short` and diff review. Bulk staging is forbidden.

## Validation Plan Template

Include a validation plan like this:

    | Level | Command or check | Success condition | Failure handling |
    |---|---|---|---|
    | Step | ... | ... | mark step failed and repair |
    | Checkpoint | ... | ... | create repair sub-step and commit |
    | Final | ... | ... | stop and report unresolved issue |

## Acceptance Criteria Rule

Include observable final acceptance criteria.

Good acceptance criteria are specific and checkable.

Examples:

- The plan states goal, scope, assumptions, constraints, evidence, execution steps, validation, commits, risks, and rollback.
- Every step has a status, validation method, acceptance criteria, and proposed commit message.
- Every group of 3 steps has a checkpoint acceptance rule.
- A failed step or checkpoint triggers repair before continuing.
- Each completed step is precisely staged and committed.
- Bulk staging commands are forbidden.
- Important claims include evidence or are labeled as assumptions.
- The answer does not depend on previous conversation history.
- The answer uses final-solution tone rather than patch tone.

## Alternatives Rule

Do not over-explain alternatives when the user asks for a final plan.

Include alternatives only when:

- the choice materially affects the result;
- the user explicitly asks for options;
- there is no single clearly preferred solution;
- risk tradeoffs must be documented.

When including alternatives:

- clearly identify the recommended option;
- provide evidence or reasoning;
- explain the impact on workload, validation, and commits.

## Hidden Knowledge Rule

Do not leave important decisions or requirements implicit.

If the plan depends on a decision, include the decision and the reason.

If the plan rejects an alternative, briefly state why.

If the user provided a constraint, restate that constraint in the final answer when it materially affects the solution.

The final answer must not require the reader to infer critical information from conversation history.

## Non-Patch Rule

Do not present the final answer as a patch.

Do not use diff-style framing unless the user explicitly asks for a diff.

Do not say or imply that files have already been modified unless they actually have been modified.

The answer should describe the final desired solution and how to implement, validate, commit, and recover.

## Practical Detail Rule

When the task is technical, include concrete details whenever known.

Prefer exact details such as:

- file paths;
- directory layout;
- command names;
- configuration keys;
- environment variables;
- function names;
- validation commands;
- expected outputs;
- failure conditions;
- rollback steps;
- commit messages.

Avoid vague instructions such as:

- "adjust related code";
- "modify the corresponding configuration";
- "run necessary tests";
- "handle edge cases".

Replace vague instructions with specific, actionable descriptions whenever possible.

## Final Checklist

Before producing the final answer, verify that it satisfies all of the following:

- The deliverable uses the user's requested language, or English by default.
- The answer is complete and standalone.
- The answer does not rely on previous messages or hidden context.
- The answer uses final-solution tone rather than patch tone.
- Important assumptions are explicitly stated.
- Important constraints from the user are preserved.
- Important claims and decisions include concrete evidence when possible.
- Missing evidence is clearly labeled as an assumption, inference, or item requiring verification.
- Workload is estimated before steps are defined.
- The plan states whether step splitting is required and why.
- Every step has a status.
- Every step has its own validation and acceptance criteria.
- Every completed step requires precise staging and commit.
- Bulk staging commands are forbidden.
- Every 3-step group has checkpoint acceptance when the plan has 3 or more steps.
- Checkpoint failure triggers repair before continuing.
- Unattended execution safeguards are included when relevant.
- Validation or acceptance criteria are included.
- Rollback or recovery is included when useful.
- The answer avoids references such as "above", "previously", "as mentioned", or "current context".
- The answer can be copied into a new document or conversation and still make sense.
