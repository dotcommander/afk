# task discovery

```sh
afk discover
afk ready
afk explain <id>
rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" .
```

AFK drains work quickly when queued tasks are concrete. The bottleneck is finding
the next useful task without inventing vague work. Use task discovery as a
numbered path-assessment workflow: classify the local path, inspect evidence
that matches what the path actually contains, convert candidates into mini-specs,
validate the task bodies, then ask one confirmation question before enqueueing.
`afk discover` prints this workflow stub without opening or creating the queue.

## discovery contract

A discovery pass produces candidates, not opinions. Every candidate must include:

| Field | Requirement |
|---|---|
| Title | One observable change. |
| Type | `fix`, `refactor`, `docs-sync`, `test-gap`, or `hardening`. |
| Evidence | File and line, command output, failing check, queue history, or doc mismatch. |
| Current proof | The current code, command, docs, or queue output that proves the task is still real. |
| Path kind | Stable free-form label for the path purpose; common values are `repo`, `docs`, `kb`, `web`, `media`, `data`, or `path`. |
| Scope | Exact package, command, doc, directory, media set, index, or behavior to touch. |
| Files | Absolute paths when the candidate references files. |
| Success | Observable done state the worker can prove. |
| Verification | Exact command or deterministic local check. |
| Confidence | `high`, `medium`, or `low`, based on current evidence quality. |
| Churn risk | `low`, `medium`, or `high`, with one short reason. |
| Risk | `low`, `medium`, or `high`, with one short reason. |
| Reject-if | One condition that should make the task invalid or blocked. |
| Suggested task | An internal complete `afk add --cwd ... --resource ... "<body>"` command. |

Good candidates are atomic, verifiable, and useful to a fresh worker with no
extra conversation context. Reject anything whose task body would say "continue",
"improve", "investigate broadly", or "make better".

Generated candidate bodies are mini-specs. They must be completable by one
worker in about one hour or less and include `Evidence:`, `Scope:`, `Success:`,
`Verify with`, and `Reject-if:` so the task is an execution contract rather than
an idea.

## workflow

1. Classify the path.
2. Gather bounded evidence that matches the path.
3. Check the queue for duplicates.
4. Convert evidence into queueable mini-specs.
5. Rank strong candidates by impact and reject weak leads.
6. Validate task bodies with `afk add --dry-run`.
7. Ask one enqueue confirmation.

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
- Value before ease: reject easy work when it does not remove a real bug, unblock
  a workflow, prevent a plausible failure mode, reduce operator pain, or make
  future work less ambiguous with measured payoff.

## signal sources

Start by classifying the path before model brainstorming. The first question is
not "what code task is here?" It is "what is this path primarily for?"

Useful purpose classes and conventional kind labels:

- `repo`: source repository, CLI, library, service, plugin, or tests.
- `web`: app or static site with routes, assets, screenshots, or UI smoke tests.
- `docs`: documentation tree, generated docs, API references, or manuals.
- `kb`: knowledge base, notes corpus, Markdown vault, research folder, or index.
- `media`: music, video, subtitles, thumbnails, playlists, or sidecar metadata.
- `data`: CSV/JSON/SQLite/datasets, schemas, imports, exports, or generated data.
- `path`: mixed or unclear local collection that needs scoped subdirectory tasks.

Start with bounded deterministic local probes:

```sh
pwd
find . -maxdepth 2 -type f | sed 's#^\./##' | sort | head -200
find . -maxdepth 2 -type d | sed 's#^\./##' | sort | head -80
git status --porcelain=v2
git diff --stat HEAD
rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
afk count
afk ready
afk ls --status failed --json
afk ls --status working --json
```

Run queue checks before suggesting candidates so discovery does not duplicate
pending or working tasks. Queue inspection may initialize the configured queue;
`afk discover` itself does not.

Useful task sources by path kind:

- `repo`: real bugs, broken workflows, unsafe reads, context propagation gaps,
  state handling risks, runtime/build blockers, deployment traps, high-friction
  local-dev blockers, or measured duplication with current risk.
- `web`: broken auth/session/cache/data flows, runtime or deployment blockers,
  bad form handling, route/action bugs, user-visible asset failures, or config
  and environment traps.
- `docs`: docs/source contradictions only when they mislead execution, setup,
  deployment, or operator behavior. Otherwise rank docs last.
- `kb`: orphaned notes, duplicate titles, missing frontmatter, uncategorized
  high-value notes, stale local indexes, or broken internal links.
- `media`: missing subtitles, thumbnails, playlists, sidecars, or catalog
  entries; inconsistent filenames; duplicate media files; metadata that can be
  checked without guessing artistic intent.
- `data`: schema/sample mismatch, stale generated exports, missing validation
  command, duplicate rows, broken import scripts, or checksum/index drift.
- `path`: mixed folders where each candidate can be scoped to one subdirectory
  and one path purpose.
- Queue history: failed AFK tasks whose `afk explain <id>` shows an actionable
  cause; long-pending or manually blocked tasks with clear dependency state.
- Work notes under `.work/`, specs, and architecture docs that already name
  concrete files or checks.

Stale reports are evidence leads, not evidence. If a report claims a problem but
current code, tests, or docs show it was already fixed, list it under
`Rejected` instead of producing a task.

## ranking priorities

Before scoring, run a value-vs-churn gate:

- Value: fixes real behavior, removes a safety/reliability hazard, unblocks a
  user/operator/developer workflow, resolves a current docs/source contradiction
  that would mislead execution, or reduces future ambiguity with measured payoff.
- Churn: style-only edits, cosmetic polish, generic cleanup, speculative
  abstractions, pure test/docs padding, stale-report busywork, or "nice to have"
  work without current proof.

Reject churn even when it is easy and small. Small only matters after value is
established.

Rank by practical impact before ease:

1. Core behavior or correctness: real bugs, broken workflows, bad state handling,
   auth/session/cache/data issues, race or stale-state hazards.
2. High-impact product or operator value: missing workflow pieces, broken UX
   paths, deployment/runtime blockers, local-dev blockers, config/env traps.
3. Safety or hardening: unsafe IO, unbounded reads, auth validation gaps, bad
   error handling, data loss risks.
4. Performance or reliability: cache stampedes, N+1 queries, resource leaks,
   retry/backoff issues, expensive hot paths.
5. Maintainability with measured payoff: small duplication removal only when
   current code proves risk or friction.
6. Tests/docs only as enablers: include them when they unblock or verify a
   higher-impact change, or when the issue is a broken command or docs/source
   contradiction.
7. Pure test gaps or docs polish: last priority; do not present them as primary
   discoveries when stronger codebase tasks exist.

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
- `readiness`: how directly the work can be performed by one AFK worker in about
  one hour or less.
- `risk`: blast radius, ambiguity, destructive potential, or missing context.
- `churn`: style-only cleanup, stale-report dependency, unrelated file spread,
  speculative abstraction, cosmetic polish, "nice to have" wording, or missing
  measurable payoff.

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
8. Drop pure test or docs polish while higher-impact codebase work is available.

## task body shape

Use this body pattern when a candidate is approved for enqueueing:

```text
[discovery:<kind>:<short-topic>] <one observable change>. Evidence: <file:line or command>. Scope: <absolute paths, package, doc tree, media set, index, or behavior>. Success: <observable done state>. Verify with <exact command or deterministic local check>. Reject-if: <condition that invalidates the task>. Constraints: keep changes scoped; do not refactor unrelated code.
```

`afk` validates generated discovery tasks more strictly when `--source
task-discovery` or `--tag discovery` is present. Generated task bodies must start
with `[discovery:<kind>:<topic>]` and include `Evidence:`, `Scope:`, `Success:`,
`Reject-if:`, and a verification command or deterministic local check. The
`<kind>` segment is a stable free-form routing label; `repo`, `docs`, `kb`,
`web`, `media`, `data`, and `path` are conventions, not a closed validator
taxonomy. Vague or churn-prone generated bodies are rejected.

Prefer structured metadata:

```sh
afk add --dry-run \
  --cwd /abs/path \
  --source task-discovery \
  --tag discovery \
  --resource <kind>:/abs/path \
  "[discovery:<kind>:topic] Evidence: <proof>. Scope: <path>. Success: <done state>. Verify with <command or deterministic check>. Reject-if: evidence no longer matches."

afk add \
  --cwd /abs/path \
  --source task-discovery \
  --tag discovery \
  --resource <kind>:/abs/path \
  "[discovery:<kind>:topic] Evidence: <proof>. Scope: <path>. Success: <done state>. Verify with <command or deterministic check>. Reject-if: evidence no longer matches."
```

Use `--blocked-by` when discovery finds an ordered chain. Use the same
`--resource <kind>:/abs/path` for tasks that would edit the same local path so
parallel workers do not collide.

Verification examples:

- `repo`: `go test ./...`, `bun test`, `php -l path/file.php`, or a documented
  project check.
- `web`: run the local smoke test, screenshot check, link check, or asset
  existence check that proves the UI path works.
- `docs` or `kb`: run a deterministic link/index/frontmatter check, or compare a
  generated index before and after.
- `media`: run a filename, sidecar, duplicate, subtitle, playlist, or catalog
  validation command.
- `data`: run schema validation, row counts, checksum checks, import dry-runs,
  or generated-output diffs.

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
   Success: `<observable done state>`
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
