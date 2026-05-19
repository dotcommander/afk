# task discovery

```sh
afk discover
afk ready
afk explain <id>
rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" .
```

AFK drains work quickly when queued tasks are concrete. The bottleneck is finding
the next useful task without inventing vague work. Use task discovery as a
candidate-generation pass: inspect local evidence, rank possible work, emit
AFK-ready task bodies, then ask one confirmation question before enqueueing.
`afk discover` prints this workflow stub without opening or creating the queue.

## discovery contract

A discovery pass produces candidates, not opinions. Every candidate must include:

| Field | Requirement |
|---|---|
| Title | One observable change. |
| Type | `fix`, `refactor`, `docs-sync`, `test-gap`, or `hardening`. |
| Evidence | File and line, command output, failing check, queue history, or doc mismatch. |
| Current proof | The current code, command, docs, or queue output that proves the task is still real. |
| Scope | Exact package, command, doc, or behavior to touch. |
| Files | Absolute paths when the candidate references files. |
| Verification | Exact command or deterministic check. |
| Confidence | `high`, `medium`, or `low`, based on current evidence quality. |
| Churn risk | `low`, `medium`, or `high`, with one short reason. |
| Risk | `low`, `medium`, or `high`, with one short reason. |
| Reject-if | One condition that should make the task invalid or blocked. |
| Suggested task | An internal complete `afk add --cwd ... --resource ... "<body>"` command. |

Good candidates are atomic, verifiable, and useful to a fresh worker with no
extra conversation context. Reject anything whose task body would say "continue",
"improve", "investigate broadly", or "make better".

Quality gates are mandatory before a candidate is shown:

- Current evidence: current tests, command output, code, docs, queue history, or
  CLI behavior must support the candidate.
- Non-stale validation: old audits, reports, `.work/` notes, and TODOs must be
  rechecked against current code before use.
- Atomic scope: one behavior, one package, one docs/source mismatch, or one
  measured duplication cluster.
- Exact verification: a worker must be able to prove success with explicit
  commands or deterministic checks.
- No churn: reject broad cleanup, style-only changes, speculative abstractions,
  and multi-subsystem edits unless they can be split into ordered atomic tasks.

## signal sources

Start with deterministic local probes before model brainstorming:

```sh
git status --porcelain=v2
git diff --stat HEAD
rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
afk count
afk ready
afk ls --status failed --json
afk ls --status working --json
```

Useful task sources:

- Dirty or untracked files that look intentionally started but unfinished.
- Failing tests, lint, builds, or documented verification commands.
- TODO/FIXME/HACK/XXX/OPTIMIZE markers with enough local context to fix.
- Stale docs where CLI help, examples, or behavior disagree with source.
- Repeated code paths that can be collapsed without changing behavior.
- Public behavior without focused tests.
- Failed AFK tasks whose `afk explain <id>` shows an actionable cause.
- Long-pending or manually blocked tasks with clear dependency or credential
  state.
- Work notes under `.work/`, specs, and architecture docs that already name
  concrete files or checks.

Stale reports are evidence leads, not evidence. If a report claims a problem but
current code, tests, or docs show it was already fixed, list it under
`Rejected` instead of producing a task.

## scoring

Score after collecting evidence:

```text
score = impact + confidence + readiness - risk - churn
```

Use 1-5 for `impact`, `confidence`, and `readiness`; use 0-5 for `risk` and
`churn`:

- `impact`: user-visible correctness, developer velocity, queue health, or
  reduced future task ambiguity.
- `confidence`: strength of local evidence.
- `readiness`: how directly the work can be performed by one AFK worker.
- `risk`: blast radius, ambiguity, destructive potential, or missing context.
- `churn`: style-only cleanup, stale-report dependency, unrelated file spread,
  speculative abstraction, or missing measurable payoff.

Reward current failing tests or command warnings, docs/source contradictions,
small blast radius, exact verification, and measured duplication or hazard
removal. Penalize stale reports, style-only cleanup, untestable outcomes, broad
refactors, unrelated file spread, and missing current proof.

Prefer 3-7 strong candidates. Up to 10 is acceptable; do not pad with weak work.
Queue candidates with strong current proof after the user confirms. Otherwise
present the ranked candidates for review and do not mutate the queue.

## dedupe and rejection

Before suggesting or enqueueing a task:

1. Search pending and working tasks for the same file, behavior, or stable tag.
2. Search recent failed tasks with `afk explain <id>` if the candidate resembles
   a retry.
3. Drop duplicate work unless the new task has a narrower scope and clearer
   verification.
4. Drop candidates that depend on unavailable credentials, unstated product
   choices, or broad architectural judgment.
5. Drop candidates that touch the same files as another proposed independent
   task; chain them with dependencies or merge them.
6. Drop refactors that do not remove measured duplication, unblock a fix, reduce
   a known maintenance hazard, or add focused coverage around existing behavior.
7. Drop candidates based only on old reports, vague TODOs, or style preference.

## task body shape

Use this body pattern when a candidate is approved for enqueueing:

```text
[discovery:<repo>:<short-topic>] <one observable change>. Evidence: <file:line or command>. Scope: <absolute paths or package>. Constraints: keep changes scoped; do not refactor unrelated code. Verify with <exact command>.
```

`afk` validates generated discovery tasks more strictly when `--source
task-discovery` or `--tag discovery` is present. Generated task bodies must start
with `[discovery:<repo>:<topic>]` and include `Evidence:`, `Scope:`, and a
verification command. Vague or churn-prone generated bodies are rejected.

Prefer structured metadata:

```sh
afk add --dry-run \
  --cwd /abs/repo \
  --source task-discovery \
  --tag discovery \
  --resource repo:/abs/repo \
  "[discovery:repo:topic] <body>; verify with <command>"

afk add \
  --cwd /abs/repo \
  --source task-discovery \
  --tag discovery \
  --resource repo:/abs/repo \
  "[discovery:repo:topic] <body>; verify with <command>"
```

Use `--blocked-by` when discovery finds an ordered chain. Use the same
`--resource repo:/abs/repo` for tasks that would edit the same repository so
parallel workers do not collide.

## output format

Interactive discovery reports should use:

```markdown
## Candidate Tasks

1. **<title>** — score <n>, risk <low|medium|high>
   Type: `<fix|refactor|docs-sync|test-gap|hardening>`
   Confidence: `<high|medium|low>`
   Churn risk: `<low|medium|high>`
   Evidence: `<file:line>` or `<command>`
   Current proof: `<current code/test/doc/queue evidence>`
   Scope: `<path>` / `<package>` / `<command>`
   Verification: `<command>`
   Reject-if: `<condition that invalidates or blocks the task>`

## Rejected

- `<item>` — stale, broad, duplicate, unverified, too risky, low-value churn, or
  HITL-dependent.

## Add To AFK?

Reply `add all`, `add 1 3`, or `no`.
```

After confirmation, run `afk prompt` first, capture pre/post `afk count`,
validate selected generated task bodies with `afk add --dry-run`, add only the
approved high-confidence tasks that validate, and report the created ids. If the
user declines, leave the queue unchanged.
