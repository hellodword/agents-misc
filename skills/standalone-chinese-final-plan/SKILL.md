---
name: standalone-chinese-final-plan
description: Produce a complete, self-contained final plan in Chinese. Use when the user asks for a final complete plan, a standalone description, no hidden knowledge, no dependency on current context, detailed evidence, or a non-patch-style answer.
---

---

# Standalone Chinese Final Plan

## Purpose

Use this skill when the final deliverable must be a complete, standalone Chinese plan that can be copied, reviewed, shared, or reused independently.

This skill controls the form, completeness, evidence quality, and clarity of the final answer. It does not decide the technical solution by itself. The actual solution must still follow the user's request, the available project information, and any task-specific instructions.

## Core Requirement

The final answer must be a complete Chinese plan that does not rely on hidden knowledge, previous conversation turns, unstated assumptions, or the current context.

A reader should be able to understand, review, and execute the plan after reading only the final answer.

The answer must also include enough evidence to justify the plan. Important claims, constraints, risks, and implementation decisions should be backed by concrete references whenever possible.

## Language

Write the final deliverable in Chinese.

Use clear, direct, professional Chinese.

Keep English only for actual names, commands, file paths, configuration keys, APIs, package names, protocols, standards, or technical terms that should remain in English.

## Tone

Use final-solution tone.

The answer should describe the intended final state, design, execution method, evidence, and verification method.

Do not write the final answer as a patch, diff, incremental change note, or conversation-dependent explanation.

Prefer wording such as:

- “最终方案是……”
- “该方案的目标是……”
- “设计如下……”
- “依据如下……”
- “执行步骤如下……”
- “验收标准如下……”

Avoid using the following as the main structure:

- “把 A 改成 B”
- “删除原来的……”
- “新增下面这段……”
- “根据上面的内容……”
- “如前所述……”
- “这个 patch……”
- “以上修改……”

It is acceptable to mention specific changes when necessary, but they must be framed as part of the final implementation plan, not as a patch explanation.

## Standalone Context Rule

The final answer must include all context required to understand the plan.

Do not rely on:

- previous messages
- “上文”
- “前面提到的”
- “当前上下文”
- unstated project assumptions
- hidden reasoning
- implicit user preferences

When relevant, explicitly include:

- goal
- background
- scope
- constraints
- assumptions
- non-goals
- target behavior
- files or directories involved
- commands to run
- configuration names
- version requirements
- evidence
- execution steps
- validation method
- acceptance criteria
- risks
- rollback or recovery method

If important information is missing, make the most reasonable assumption and state it explicitly under “默认假设”.

Do not block the final answer only because some details are missing, unless the task would be unsafe, impossible, or fundamentally ambiguous.

## Evidence Rule

The final plan must include detailed evidence for important claims and decisions.

Use evidence to answer questions such as:

- Why is this plan necessary?
- Which files, modules, functions, routes, commands, settings, or documents are involved?
- Which observed facts support the proposed design?
- Which constraints come from source code, configuration, documentation, logs, tests, or user-provided requirements?
- Which risks are proven or suggested by concrete evidence?

Prefer concrete evidence such as:

- file paths
- line numbers or line ranges
- function, class, type, route, command, or configuration names
- command outputs
- test results
- error messages
- logs
- schema fields
- API names
- version numbers
- commit, issue, pull request, or release references
- official documentation links
- relevant source links
- user-provided constraints that materially affect the solution

For code-related evidence, prefer this style:

```text
path/to/file.ext:123-145：说明这里体现了什么事实。
```

For reference links, prefer this style:

```text
官方文档或源码链接：说明该链接支持了什么结论。
```

Do not invent evidence.

Do not invent file paths, line numbers, command outputs, or links.

If exact evidence is unavailable, clearly label the item as an assumption, inference, or item requiring verification.

## Evidence Placement Rule

For technical or investigative plans, include a dedicated section named one of:

- `## 证据`
- `## 依据与证据`
- `## 现状与证据`

Use the shortest section name that fits the task.

Evidence should be concise but specific. Avoid dumping excessive raw content. Each evidence item should explain why it matters to the plan.

A good evidence item has this shape:

```text
证据位置：可观察事实 → 对方案的影响。
```

For example:

```text
backend/jobs/video.go:42-68：已有后台任务调度入口 → 新任务应复用现有调度机制，而不是引入第二套调度框架。
```

## Completeness Rule

The final answer must be complete enough for another reader to execute or review without asking for the original conversation.

For complex technical tasks, prefer this structure:

```markdown
# 方案

## 目标

## 背景与约束

## 默认假设

## 依据与证据

## 最终设计

## 执行步骤

## 验证方式

## 验收标准

## 风险与注意事项

## 回滚方案
```

For smaller tasks, a shorter structure is acceptable, but it must still be standalone and complete.

Do not mechanically include irrelevant sections. Include the sections needed to make the final answer clear, evidenced, executable, and independently understandable.

## Hidden Knowledge Rule

Do not leave important decisions or requirements implicit.

If the plan depends on a decision, include the decision and the reason.

If the plan rejects an alternative, briefly state why.

If the user provided a constraint, restate that constraint in the final answer when it affects the solution.

The final answer must not require the reader to infer critical information from the conversation history.

## Non-Patch Rule

Do not present the final answer as a patch.

Do not use diff-style framing unless the user explicitly asks for a diff.

Do not say or imply that files have already been modified unless they actually have been modified.

The answer should describe the final desired solution and how to implement or verify it.

## Practical Detail Rule

When the task is technical, include concrete details whenever they are known.

Prefer exact details such as:

- file paths
- directory layout
- command names
- configuration keys
- environment variables
- function names
- validation commands
- expected outputs
- failure conditions
- rollback steps

Avoid vague instructions such as:

- “调整相关代码”
- “修改对应配置”
- “进行必要测试”
- “处理边界情况”

Replace vague instructions with specific, actionable descriptions whenever possible.

## Alternatives Rule

Do not over-explain alternatives when the user asks for a final plan.

Include alternatives only when:

- the choice materially affects the result
- the user explicitly asks for options
- there is no single clearly preferred solution
- risk tradeoffs must be documented

When including alternatives, clearly identify the recommended option and provide evidence or reasoning for that recommendation.

## Validation Rule

For technical or operational plans, include a verification method.

The verification method should explain:

- what to run
- what to inspect
- what result means success
- what result means failure

If automated validation is unavailable, provide manual validation steps.

## Acceptance Criteria Rule

For technical or process-oriented plans, include observable acceptance criteria.

Good acceptance criteria are specific and checkable.

Examples:

- “相关文件路径、配置项、命令和验收方式已在方案中显式列出。”
- “关键判断都有对应证据，或被明确标记为默认假设。”
- “读者无需阅读原始对话即可理解目标、约束、执行步骤和验收标准。”
- “最终输出不包含‘上文’、‘前面’、‘当前上下文’等依赖对话历史的表述。”

## Example Rule

Examples should describe reusable patterns, not a full implementation.

Keep examples short.

Do not use this skill itself as the subject of examples, because self-referential examples can mislead later use.

Good example pattern:

```markdown
## 依据与证据

- `path/to/file.ext:10-25`：这里定义了现有入口 → 新方案应复用该入口。
- `path/to/config.example:3-8`：这里展示了已有配置命名风格 → 新配置应保持同一命名习惯。
- 官方文档链接：该能力支持所需参数 → 方案可以直接使用该能力，无需自建替代实现。

## 最终设计

基于上述证据，采用最小侵入方案：复用现有入口，补充必要配置，并通过现有验证流程确认行为正确。
```

Bad example pattern:

```markdown
按照前面说的，把原来的东西改一下。这里新增一个配置，然后处理一下边界情况。这个 patch 应该就可以了。
```

## Final Checklist

Before producing the final answer, verify that it satisfies all of the following:

- The deliverable is written in Chinese.
- The answer is complete and standalone.
- The answer does not rely on previous messages or hidden context.
- The answer uses final-solution tone rather than patch tone.
- Important assumptions are explicitly stated.
- Important constraints from the user are preserved.
- Important claims and decisions include concrete evidence when possible.
- Evidence uses file paths, line numbers, reference links, command outputs, logs, or other verifiable details when available.
- Technical details are concrete where possible.
- Validation or acceptance criteria are included when useful.
- The answer avoids references such as “上文”, “前面”, “如前所述”, or “当前上下文”.
- The answer can be copied into a new document or conversation and still make sense.
