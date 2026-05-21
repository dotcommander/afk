# task discovery

```sh
afk prompt --discover
afk ready
afk explain <id>
rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" .
```

AFK drains work quickly when queued tasks are concrete. The bottleneck is finding
the next useful task without inventing vague work. Use task discovery as a
numbered review workflow: classify the target, inspect evidence
that matches what the material actually contains, convert candidates into mini-specs,
validate the task bodies, then ask one confirmation question before enqueueing.
`afk prompt --discover` prints this workflow stub without opening or creating the queue.

## find high-impact project work

Use discovery when you do not know what to work on yet and want critical or
high-impact candidates from a project:

```sh
afk prompt --discover
```

The command is read-only. It prints a workflow for a coding agent or human
reviewer. A serious repo or web review should use Level 2
feature/command review, which means:

- classify the target from actual files, not just the directory name
- inspect manifests, local guidance, entrypoints, and primary implementation
  surfaces
- run the declared deterministic check before deciding there is no task, such
  as `bun run check`, `bun run build`, `go test ./...`, `php artisan test`, or
  the nearest documented parser/validation command
- inspect failures enough to distinguish AFK-ready work from stale installs,
  broad suites, product choices, or low-impact noise
- return only concrete candidates with current evidence, exact scope, exact
  verification, and under-one-hour execution shape

If the review finds candidates, validate each task body before enqueueing:

```sh
afk add --dry-run --cwd /Users/you/code/my-project \
  --source task-discovery \
  --tag discovery \
  --resource repo:/Users/you/code/my-project \
  "[discovery:repo:settings-save] Fix the settings save bug. Evidence: /Users/you/code/my-project/internal/settings/store.go:42. Scope: /Users/you/code/my-project/internal/settings. Success: settings persist after refresh. Verify with go test ./... Reject-if: settings persistence moved out of this package."
```

After validation, enqueue only the accepted candidates:

```sh
afk add --cwd /Users/you/code/my-project \
  --source task-discovery \
  --tag discovery \
  --resource repo:/Users/you/code/my-project \
  "[discovery:repo:settings-save] Fix the settings save bug. Evidence: /Users/you/code/my-project/internal/settings/store.go:42. Scope: /Users/you/code/my-project/internal/settings. Success: settings persist after refresh. Verify with go test ./... Reject-if: settings persistence moved out of this package."
```

## discovery contract

A discovery pass produces candidates, not opinions. Every candidate must include:

| Field | Requirement |
|---|---|
| Title | One observable change. |
| Type | `fix`, `refactor`, `docs-sync`, `test-gap`, or `hardening`. |
| Evidence | File and line, command output, failing check, queue history, or doc mismatch. |
| Current proof | The current code, command, docs, or queue output that proves the task is still real. |
| Kind | Stable free-form label for the target purpose; common values are `repo`, `docs`, `kb`, `web`, `media`, `data`, or `workspace`. |
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

1. Classify the target and choose review depth.
2. Gather bounded evidence that matches the target.
3. Check the queue for duplicates.
4. Convert evidence into queueable mini-specs.
5. Rank strong candidates by impact and reject weak leads.
6. Validate one-off task bodies with `afk add --dry-run`, or generated batches
   with `afk import --dry-run`.
7. Ask one enqueue confirmation.

Choose and report review depth before candidate generation:

- Level 1 triage is a shallow inventory. Use it only when the user asks for a
  quick pass, the target is unknown or mixed, or a deeper review is not feasible.
  Label the output as triage and emit few or no candidates unless a current
  failure is already proven.
- Level 2 feature/command review is required for serious repo or web discovery,
  and for broad batches such as "each directory under X" unless the user
  explicitly asks for triage.
- A structurally valid artifact is not proof of careful discovery. Do not call a
  pass complete until the evidence budget for the chosen depth has been met for
  every target.

Quality gates are mandatory before a candidate is shown:

- Current evidence: current tests, command output, code, docs, queue history, or
  CLI behavior must support the candidate.
- Corroboration: TODO/FIXME/HACK/XXX text is only a lead. Promote it only when
  nearby implementation, caller, tests, docs, config, command output, or a
  contract surface proves current impact.
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

Start by classifying the target before model brainstorming. The first question
is not "what code task is here?" It is "what is this material primarily for?"

Useful purpose classes and conventional kind labels:

- `repo`: source repository, CLI, library, service, plugin, or tests.
- `web`: app or static site with routes, assets, screenshots, or UI smoke tests.
- `docs`: documentation tree, generated docs, API references, or manuals.
- `kb`: knowledge base, notes corpus, Markdown vault, research folder, or index.
- `media`: music, video, subtitles, thumbnails, playlists, or sidecar metadata.
- `data`: CSV/JSON/SQLite/datasets, schemas, imports, exports, or generated data.
- `workspace`: mixed or unclear local collection that needs scoped subdirectory tasks.

Start with bounded deterministic local probes:

```sh
pwd
find . -maxdepth 2 -type f | sed 's#^\./##' | sort | head -200
find . -maxdepth 2 -type d | sed 's#^\./##' | sort | head -80
git status --porcelain=v2
git diff --stat HEAD
rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
afk status
afk ready
afk ls --status failed --json
afk ls --status working --json
```

Run queue checks before suggesting candidates so discovery does not duplicate
pending or working tasks. Queue inspection may initialize the configured queue;
`afk prompt --discover` itself does not.

For Level 2 repo or web discovery, gather this minimum evidence before accepting
or rejecting candidates:

- Inspect local guidance plus manifest files.
- Gather target-selection signals before choosing files: recent churn, complex
  or large files, test coverage, imports/callers, and current marker or command
  pain.
- Inspect at least one command, route, feature map, or package script surface.
- Inspect at least three core source files tied to primary behavior, unless
  fewer exist.
- Inspect at least one contract surface such as README command docs, tests,
  fixtures, schemas, config examples, screenshots, or sample data.
- Run the narrowest declared deterministic check from the manifest or docs, such
  as `npm run check`, `bun run check`, `go test ./...`, `php artisan test`,
  `pytest`, or a documented parser/import validation.
- Record an explicit skip reason only when the check has a concrete blocker such
  as missing dependency, credentials, network, cost, sandbox restriction, or no
  matching command.
- Record rejected leads with reasons, including why TODO/backlog hits were not
  promoted.

Do not let batch breadth become a reason to skip the target's own declared gate.
If `package.json`, `go.mod`, `composer.json`, `pyproject.toml`, `justfile`,
`Makefile`, `README`, or local guidance names a check/build/test command, prefer
running that command before saying `no strong candidate`. A skipped declared gate
makes the target low-confidence or triage-only; it is not evidence that there
are no strong candidates.

Useful task sources by target kind:

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
- `workspace`: mixed folders where each candidate can be scoped to one
  subdirectory and one purpose.
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

If every lead is low-impact or uncorroborated, say `no strong candidate` and
explain what was inspected. Do not pad the output with easy TODO cleanup, docs
polish, dependency drift, or generic tests.

If a declared local gate fails, first ask whether a small fix can make the gate
pass without product choices or broad refactoring. Broken declared checks are
often better AFK candidates than TODO markers because they have exact
verification.

If a TODO or product promise points at a missing behavior but the
destination/provider/architecture is absent, reject or mark it provisional rather
than forcing an ambiguous task. Prefer the narrower failing check when one
exists.

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
`web`, `media`, `data`, and `workspace` are conventions, not a closed validator
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

When discovery produces a phased or dependent batch, prefer an import document
instead of looping `afk add`:

```json
{"tasks":[
  {
    "slug":"phase-1-topic",
    "body":"[discovery:repo:topic] One focused change.\n\nEvidence:\n- /abs/repo/file.go proves the issue.\n\nScope:\n- /abs/repo/file.go\n\nSuccess:\n- The issue is fixed.\n\nVerify:\n- cd /abs/repo && go test ./...\n\nReject-if:\n- The evidence no longer matches.",
    "cwd":"/abs/repo",
    "source":"bulk-afk-planner",
    "tags":["spec:repo-discovery","phase:1","discovery"],
    "resource_key":"repo:/abs/repo"
  }
]}
```

Validate with `afk import --dry-run < afk-import.json`. Generated import tasks
with `spec:` tags must include `Evidence:`, `Scope:`, `Success:`, `Verify:`,
and `Reject-if:` sections, and must carry an absolute `cwd` or absolute paths in
the body.

Dry-run validation proves that a task body or import document is admissible. It
does not prove that discovery was deep, complete, or valuable. Likewise, a batch
artifact audit can prove artifact shape without proving that the underlying
directory was carefully evaluated.

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

Batch discovery reports must also include:

- target count and artifact count
- accepted candidate count and no-strong-candidate count
- rejected or provisional lead count
- deterministic checks run and checks skipped with reasons
- low-confidence or triage-only targets
- whether validation covered task shape only, artifact shape only, or actual
  behavior

When comparing a focused pass with an earlier breadth pass, preserve the earlier
artifact as the baseline and report what changed:

- which checks breadth skipped and focused discovery ran
- which leads stayed rejected and why
- which candidates are new, with current command/file evidence
- whether the difference is better evidence, narrower scope, or a changed
  worktree

After confirmation, run `afk prompt` first, capture pre/post `afk status`,
validate selected one-off bodies with `afk add --dry-run` or selected batches
with `afk import --dry-run`, add/import only the approved high-confidence tasks
that validate, and report the created ids. If the user declines, leave the queue
unchanged.
