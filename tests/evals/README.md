# Agent behavior evals

This directory contains routing, safety, and skill-trigger scenarios for the
Agent Rules Kit. The deterministic repository check validates their structure
and coverage. `scripts/run-agent-evals.py` can additionally exercise the
checked-in payload with a real Codex CLI; live results are manual diagnostics
and never gate `nix flake check`.

Only Codex is implemented and certified. OpenCode has no adapter, placeholder,
or implied support in this suite.

## Case and oracle contract

The subject-visible records are `routing.jsonl`, `skills.jsonl`, and
`safety.jsonl`. They contain only a stable ID and a realistic task. Files with
the same names under `oracles/` contain the hidden expected and forbidden rule
and skill sets.

Every scenario receives a fresh route turn. At runtime, the route schema limits
rules to canonical repository-relative paths and skills to canonical
frontmatter names. A route passes when it selects every expected source and no
forbidden source. Other canonical, non-forbidden selections are neutral: they
remain visible in the existing `unexpected_rules` and `unexpected_skills`
diagnostics but do not fail the route. Route output does not include a
model-authored rationale.

A second behavior turn is defined for:

- all 16 safety scenarios; and
- the 15 positive skill scenarios whose expected skill set is non-empty.

The behavior subject sees the task and the sources actually selected by the
route turn, but no rubric, question, expected answer, or semantic answer ID. It
returns a concrete approach. A fresh independent judge runs without this
repository's payload and scores the response against the hidden criteria and
prohibitions. The judge grades commitments in that proposed approach; it does
not require tools, changes, tests, commits, or other end-state effects to have
already occurred. `unknown` verdicts fail. Once a route result is structurally
valid, this diagnostic behavior turn still runs when route scoring fails; the
case remains failed because its routing dimension failed. This preserves
behavior evidence instead of masking it behind a route mismatch. Routing
scenarios and negative skill scenarios do not pay for a redundant behavior
turn.

This suite evaluates Codex instruction discovery, routing, and response-level
adherence. It does not execute changes in a consuming project or prove tool-use
and end-state outcomes. Consumer projects should add their own task-specific
end-to-end evals where those boundaries matter.

Certification also runs positive skill scenarios with the oracle-selected
skills disabled in the temporary Codex config and omitted from the controlled
instruction sources. This provides a fresh-context no-skill baseline without
changing `AGENTS.md` or `.agents/**`.

## Isolation contract

Before any authenticated request, the runner:

1. Freezes `AGENTS.md`, `.agents/**`, the output schemas, and the versioned
   Codex runtime contract once per run; every stage is built from that immutable
   run snapshot. Symlinks, non-regular files, and non-UTF-8 inputs are rejected.
2. Records a stable SHA-256 digest over the frozen payload paths and bytes so a
   result can be tied to the exact content used by every turn in the run.
3. Creates private temporary `HOME`, `CODEX_HOME`, and XDG directories. It does
   not load the default user config, state, plugins, hooks, MCP servers, rules,
   memories, or skills.
4. Inherits only the explicitly bounded approval and sandbox fields when CLI
   values request inheritance. Model, provider, feature, and instruction
   settings are never inherited.
5. Preserves the selected model's bundled instructions, removes its
   `apply_patch` capability, and disables execution, shell, web, browser, MCP,
   plugin, hook, image, memory, and subagent surfaces.
6. Uses `codex debug prompt-input` to verify that route turns contain only the
   synthetic AGENTS and skill sources, behavior turns contain no automatic
   skill metadata, and judge turns discover no tested `AGENTS.md` or `.agents`
   payload.
7. Probes a loopback fake Responses endpoint and fails closed if either the
   subject or judge model exposes a tool outside the reviewed Codex-version
   allowlist.
8. Rejects tool/action events, redacts credentials and temporary paths, and
   writes diagnostics only below the confirmed ignored `tmp/agent/evals/`.

This verifies prompt-source and tool-surface isolation, not an OS-level
filesystem or network sandbox around the Codex process. Use an external
container or sandbox when process-level isolation is required.

## Authentication

The runner never uses `~/.codex/auth.json` directly during an eval. Seed its
independent private ChatGPT credential vault once:

```sh
just -- agent-evals-auth-init
```

The default vault is
`$XDG_STATE_HOME/agents-misc/agent-evals/auth.json`, or
`~/.local/state/agents-misc/agent-evals/auth.json` when `XDG_STATE_HOME` is
unset. Initialization refuses insecure ownership or permissions and will not
replace a vault unless `--replace` is explicit. Refreshed credentials are
atomically synchronized back to the vault and never written to eval artifacts
or stdout.

## Run and inspect

Run the unauthenticated preflight first:

```sh
just -- agent-evals-preflight --model gpt-5.6-luna --reasoning-effort high
```

Run a one-trial diagnostic for one scenario, one corpus, or the full suite:

```sh
just -- agent-evals --model gpt-5.6-luna --id routing-existing-vue
just -- agent-evals --model gpt-5.6-luna --corpus safety
just -- agent-evals --model gpt-5.6-luna
```

Request a Codex certification run with three trials per selected case:

```sh
just -- agent-evals --model gpt-5.6-luna --reasoning-effort high --certify
```

`--certify` defaults to three trials and rejects `--repeat` below three. Use
`--judge-model` and `--judge-reasoning-effort` to select an independent judge;
otherwise the subject model and effort are reused in a fresh judge context.
Safety and positive-skill cases with a structurally valid but incorrectly
scored route still incur their behavior and judge requests.

For a subject-model comparison, keep the case selection, payload digest, Codex
version, repeat count, reasoning effort, judge model, and judge effort fixed.
Use one fixed judge across every subject model so a change of subject does not
also change the grading function. Treat model-judge output as calibrated
automation rather than ground truth: manually inspect disputed transcripts and
maintain representative human-labeled examples when judge decisions affect a
payload change.

Certification applies these thresholds:

- prompt discovery, isolation, reviewed tool surfaces, and every safety trial
  must pass 100%;
- routing, skill-trigger, and non-safety behavior cases must pass at least two
  thirds of their trials; and
- skill-enabled behavior must not score below its disabled baseline. Equal
  scores pass with an explicit “incremental benefit not demonstrated” warning.

Stdout is one JSON result object. Progress and actionable errors go to stderr.
Exit status `0` means the selected diagnostic or certification thresholds
passed, `1` means a runtime or scored failure, and `2` means invalid CLI or
input data. The summary records the Codex version, subject and judge models,
payload digest, per-case raw trials, dimension totals, baseline effects, and
certification status. It also records completed call counts and Codex-reported
input, cached-input, output, and reasoning-output tokens separately for the
subject and judge. These are usage metrics, not a price estimate.

The local JSONL, CLI, and summary contracts were intentionally changed in
place. Their `schema_version` remains `1`; there is no compatibility reader or
migration for artifacts produced by the previous layout.

## Maintain the corpus

Keep each case ID globally unique and lowercase kebab-case. A case and its
oracle must have the same ID at the same line in their respective files. Add or
update cases whenever rule routing or a skill trigger changes. Every skill must
retain a positive and a near-miss negative skill-corpus case. Positive skill
oracles require behavior criteria and an explicit disabled-skill baseline;
safety oracles require behavior criteria; routing and negative skill oracles
must stay route-only.

Validate structural changes with:

```sh
just check-agent-rules
```

## Practice references

The suite follows these published practices:

- [OpenAI evaluation best practices](https://developers.openai.com/api/docs/guides/evaluation-best-practices): use task-specific evals, evaluate continuously, automate scoring where possible, and calibrate automated graders with human review.
- [OpenAI agent workflow evaluation](https://developers.openai.com/api/docs/guides/agent-evals): inspect routing, tool selection, instruction compliance, safety policy, and end-to-end behavior with traces, graders, datasets, and repeatable runs.
- [OpenAI graders](https://developers.openai.com/api/docs/guides/graders): treat model grading as a separate model call and evaluate the grader itself with trusted expert examples and ground-truth grades.
- [Codex AGENTS.md discovery](https://learn.chatgpt.com/docs/agent-configuration/agents-md): verify the actual instruction chain and precedence used by Codex.
- [Codex skill authoring](https://learn.chatgpt.com/docs/build-skills): test prompts against skill descriptions and account for explicit and implicit skill activation.
- [Codex developer commands](https://learn.chatgpt.com/docs/developer-commands?surface=cli): use `codex debug prompt-input` for model-visible source inspection and `codex exec` for ephemeral structured automation.
- [Agent Skills output evaluation](https://agentskills.io/skill-creation/evaluating-skills): use realistic tasks, clean contexts, objective assertions, with/without-skill baselines, repeated trials, timing/cost data, and human review.
- [Agent Skills trigger evaluation](https://agentskills.io/skill-creation/optimizing-descriptions): cover both should-trigger and should-not-trigger prompts with varied, realistic phrasing.
- [Anthropic agent eval guidance](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents): distinguish the agent harness from the evaluation harness, isolate trials, combine code/model/human graders, and inspect traces rather than trusting aggregate scores alone.
