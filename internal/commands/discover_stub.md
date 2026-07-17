afk prompt --discover --full is the full task-discovery policy. It prints guidance only; it does not mutate the queue.

Mine concrete AFK-ready candidate tasks from the material the user wants reviewed.

1. Classify the target and choose review depth.

  Inventory the requested target before proposing work.
  For local files, list representative files and directories.
  For remote, pasted, or described material, identify the concrete artifacts available for inspection.

  Decide the primary purpose before proposing work: code repo, web app, docs set, knowledge base, media archive, data folder, mixed workspace, or other local collection.

  Choose depth before collecting candidates:
  - Level 1 triage is a shallow inventory. Use it only when the user asks for a quick pass, the target is unknown or mixed, or you cannot afford deeper review. Label the output as triage and emit few or no candidates unless a current failure is already proven.
  - Level 2 feature/command review is required for serious repo or web discovery, and for batches such as "each directory under X" unless the user explicitly asks for triage.
  - A structurally valid artifact is not proof of careful discovery. Do not call a pass complete until the evidence budget for the chosen depth has been met for every target.

2. Gather bounded evidence that matches the target.

  code repo: git status --porcelain=v2; git diff --stat HEAD; rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
  docs or knowledge base: rg -n "TODO|FIXME|stale|broken|missing|TBD" .; check internal links, indexes, frontmatter, duplicate titles, and orphaned notes
  web UI: inspect package/app manifests, routes, screenshots or smoke-test scripts, broken asset paths, and docs/source drift
  media archive: inspect filenames, sidecar metadata, duplicates, missing thumbnails/subtitles, broken playlists, and index/catalog files
  data folder: inspect schemas, samples, checksums, import/export scripts, stale generated outputs, and validation commands
  mixed workspace: split candidates by subdirectory or artifact group and keep each task scoped to one purpose

  Level 2 evidence is an escalation ladder, not a fixed checklist. Climb only as far as selecting and proving a candidate requires:
  - cheap signals first: git status --porcelain=v2, manifest and local-guidance files, and the todo/doing queue
  - then target-selection signals: recent churn, complex or large files, test coverage, imports/callers, and current marker or command pain
  - then inspect 1-2 relevant source surfaces: a command, route, feature map, or package script, plus the core source files tied to the primary behavior
  - then inspect at least one contract surface such as README command docs, tests, fixtures, schemas, config examples, screenshots, or sample data
  - run the narrowest declared deterministic check tied to the candidate: a package-scoped test (go test ./internal/<pkg>/...), a scoped npm/bun script, a documented parser/import validation, or the nearest scoped command. A repo-wide check such as go test ./... is a broad failure-locator for batch mining (see the monolith pass) — after it surfaces a cluster, scope the resulting task to one package or subsystem, not the whole run.
  - reach for broad probes (git diff, rg TODO|FIXME, git log, repo-wide test suites) only when needed to select a target or prove a lead
  - record an explicit skip reason only when the check has a concrete blocker such as missing dependency, credentials, network, cost, sandbox restriction, or no matching command
  - record rejected leads with reasons, including why TODO/backlog hits were not promoted

  Treat this as an evidence budget proportional to the target's surface area, not a mandatory fixed run against every target: a small single-purpose target needs only the cheap rungs plus one source surface and one scoped check; a large or mixed target climbs further.

  Resolve before promote:
  Treat each lead as a hypothesis. Before accepting it, state and test the strongest plausible benign, stale, already-fixed, prior-art, duplicate, or intentional explanation. When source, runtime output, docs, tests, generated artifacts, and config disagree, identify the authority for the disputed behavior and explain the arbitration. Probe guards, list/help output, config loading, and test ownership only when they can resolve the hypothesis; these are conditional probes, not a fixed checklist. If a named environment or evidence gap prevents resolution, keep the lead as residual instead of promoting or silently rejecting it.

  No Shallow Batch Passes:
  For batch discovery, coverage is not completion. Do not treat "every directory has an artifact" or "the batch audit script has no structural errors" as proof that discovery was done.

  A valid discovery pass must run a real evidence loop per target:
  1. classify the target from its actual files, not just directory name
  2. inspect manifest files, local guidance, entrypoints, and at least one primary implementation surface
  3. run the project's declared deterministic check when available:
     - package.json: prefer check, then test, then build
     - go.mod: go test ./...
     - composer.json, pyproject.toml, or config-only dirs: parse/validate or run the nearest documented check
  4. if the command fails, inspect enough source/config to decide whether the failure is an AFK-ready task, stale local dependency/install state, too broad for one task, or low-impact/noise
  5. only write "no strong candidate" after recording the command run, result, inspected files, and rejected leads

  Do not mass-produce same-shaped "no candidate" artifacts. Repeated generic no-candidate conclusions across many directories are a failure signal, not a successful batch.

  Monolith / frankenstein repo pass:
  This pass engages for broad repo mining, many unrelated command surfaces, "find all" or "mine this repo" requests, batch discovery, or when no strong candidate appears on the fast path. It is not the default for ordinary single-path discovery.
  If a repo has many unrelated command surfaces, mixed runtimes, a very large internal/ tree, many docs/audit/workspace notes, generated or archived code, or several independent failing packages, do not stop at the first failing command. First build a subsystem map and choose review slices from current evidence.

  Before selecting candidates in a monolith, record:
  - rough topology: command count, largest packages/directories, web/API/server surfaces, scheduler/background jobs, data/storage boundaries, and generated/archive areas to ignore
  - declared contracts: local guidance, README command promises, just/make/package scripts, docs/audit checklists, config examples, migrations, schemas, and test suites
  - entropy signals: stale compatibility layers, legacy/current dual paths, hidden env toggles, fallback paths that contradict local rules, context.Background in live execution code, unbounded IO, global state, and duplicated setup/wiring
  - failure clustering: run the broad declared check when acceptable, then group failures by subsystem instead of emitting one "make tests pass" task

  A monolith candidate should target one subsystem boundary or one failing contract, not the whole repo. Prefer slices such as "chat bootstrap panics on missing prompt config", "TLDR composer tests expect old prompt sections", "docs archive tests reference renamed files", or "provider factory error contract drift". Reject broad "clean up the monolith", "simplify architecture", and "make go test ./... green" tasks unless they are split into independent, verified subsystem tasks.

  For repo/web directories, a candidate may come directly from a current failing declared check when:
  - the failing command is declared by the project
  - the error points to concrete files/lines or a missing declared contract
  - the fix scope is under one hour
  - the verification command is exact
  - the task does not require product or infrastructure choices

  Strong discovery evidence examples:
  - bun run check fails in src/routes/+page.svelte with implicit-any handler errors
  - bun run build fails because package.json references missing build.js
  - svelte-check fails because tests/*.js are being type-checked by app tsconfig and lack test/runtime types

  Reject or downgrade examples:
  - missing dependencies when they are already declared in package.json and lockfile; likely stale install state
  - a huge failing test suite spanning unrelated domains; too broad unless narrowed to one failing contract
  - bun test reports "No tests found" without evidence that tests are expected
  - TODO/backlog text without current source/command proof

3. Check the queue for duplicates before suggesting candidates.

  Duplicate-check against todo and doing tasks early — before broad evidence collection — so effort is not spent re-discovering an already-queued task.

  afk status
  afk take --dry-run --limit 0 --json --full
  afk tasks --status todo --json
  afk tasks --status doing --json
  afk tasks --status failed --json

4. Convert evidence into queueable mini-specs.

  Accept only candidates with:
  - current evidence from files, command output, tests, or docs/source mismatch
  - corroboration beyond a single marker when the lead starts from TODO/FIXME/HACK/XXX text
  - atomic scope that a worker can complete independently
  - about one hour or less for a fresh worker
  - Evidence:, Scope:, Success:, Verify with, and Reject-if:
  - exact verification command or deterministic local check
  - clear value, low churn risk, and no broad cleanup/refactor wording
  - no duplicate todo or doing task for the same behavior

5. Rank by impact and reject before enqueueing.

  Classify every discovered lead exactly once:
  - accepted: current evidence proves queueable work
  - prior-art: the same observable change is already handled, documented, or queued
  - refuted: the resolving check shows the suspected defect is not present
  - rejected: the issue may be real, but it fails scope, value, risk, or queueability gates
  - residual: a named environment or evidence gap prevents resolution

  Total accounting is required: discovered leads must equal accepted + prior-art + refuted + rejected + residual.
  Classification is exclusive. Prior-art takes precedence when an existing artifact or queue entry already handles the same observable change. Use refuted only when no prior-art match exists and the resolving check shows the hypothesis is false.

  First ask: is this value or churn?
  Value changes remove a real bug, unblock a workflow, prevent a plausible failure mode, reduce operator pain, or make future work less ambiguous with measured payoff.
  Churn changes are style-only, cosmetic polish, generic cleanup, speculative abstraction, pure test/docs padding, or "nice to have" work without current proof.
  Reject churn even when it is easy.

  Rank the strong candidates you actually found using this priority order. For an ordinary single target this is typically 1-3; the 3-7 range applies only to broad or batch mining. Never pad to hit a number.
  1. core behavior or correctness: real bugs, broken workflows, bad state handling, auth/session/cache/data issues, race or stale-state hazards
  2. high-impact product or operator value: missing workflow pieces, broken UX paths, deployment/runtime blockers, local-dev blockers, config/env traps
  3. safety or hardening: unsafe IO, unbounded reads, auth validation gaps, bad error handling, data loss risks
  4. performance or reliability: cache stampedes, N+1 queries, resource leaks, retry/backoff issues, expensive hot paths
  5. maintainability with measured payoff: small duplication removal only when current code proves risk or friction
  6. tests/docs only as enablers for higher-impact work or broken command/docs-source contradictions
  7. pure test gaps or docs polish last; do not present them as primary discoveries when stronger codebase tasks exist
  Show rejected stale, broad, duplicate, risky, unverified, or churn leads.
  When candidates must run in order, enqueue the prerequisite first and pass its created id with --blocked-by to the dependent afk add command.

  If every lead is low-impact, refuted, prior-art, residual, or uncorroborated, say "no strong candidate" affirmatively and report the checks performed plus all five lead counts. Do not pad the output with easy TODO cleanup, docs polish, dependency drift, or generic tests, and do not ask for enqueue confirmation.

  Early-stop: once you have 1-3 strong, non-duplicate candidates that meet every section-4 acceptance criterion (current evidence, atomic scope, exact verification command, value not churn), stop probing — unless the user explicitly asked for broad mining, batch coverage, or a full repo review. The early-stop bar is the section-4 acceptance criteria, not merely "the dry-run passed" — dry-run only proves task-body shape. Do not keep probing to satisfy a breadth checklist after a strong candidate is proven. This does not relax the anti-shallow guardrails: a pass with no acceptance-criteria-meeting candidate is not finished.

  If a declared local gate fails, first ask whether a small fix can make the gate pass without product choices or broad refactoring. Broken declared checks are often better AFK candidates than TODO markers because they have exact verification.
  If a TODO or product promise points at a missing behavior but the destination/provider/architecture is absent, reject it, or classify it residual when a named evidence gap prevents resolution, rather than forcing an ambiguous task. Prefer the narrower failing check when one exists.

  Before concluding "no strong candidate", or before completing a broad pass, run one adversarial second pass against the highest-risk assumption or least-corroborated surface. Record the alternative explanation it tried to prove and the evidence that survived or changed classification.

6. Validate task bodies with dry-run.

Candidate bodies should start with [discovery:<kind>:<topic>] and include Evidence:, Scope:, Success:, Verify with, and Reject-if:. Instantiate this template exactly:

<task-body-template>
[discovery:<kind>:<topic>] <one observable change>. Evidence: <file, command, URL, artifact, or record>. Scope: <exact package, directory, document, dataset, feature, or behavior>. Success: <observable done state>. Verify with <exact command or deterministic local check>. Reject-if: <condition that invalidates the task>. Constraints: keep changes scoped; do not refactor unrelated code.
</task-body-template>

Use a stable kind such as repo, docs, kb, web, media, data, or workspace. The kind is a free-form routing label; those values are conventions, not a closed validator list.
Use concrete file, command, URL, artifact, or record references in Evidence:/Scope:.
Use a resource key that matches the target kind, such as repo:<root>, docs:<root>, kb:<root>, web:<app>, media:<collection>, data:<dataset>, or workspace:<name>.

Validate before enqueueing:
<dry-run-command>
afk add --dry-run --source task-discovery --tag discovery --resource "<kind>:<target>" \
  "[discovery:<kind>:<topic>] <one observable change>. Evidence: <file, command, URL, artifact, or record>. Scope: <exact package, directory, document, dataset, feature, or behavior>. Success: <observable done state>. Verify with <exact command or deterministic local check>. Reject-if: <condition that invalidates the task>. Constraints: keep changes scoped; do not refactor unrelated code."
</dry-run-command>

Dry-run validation proves that a task body is admissible. It does not prove that discovery was deep, complete, or valuable.

Immediately before dry-run and again before enqueue, recheck the candidate's current evidence, prior-art and queue duplicate state, and Reject-if condition. If the task body or its cwd, source, tags, resource, evidence, relevant worktree state, or duplicate context changed, rerun validation; do not reuse the stale result.
If material evidence, body, scope, success, or verification changes after presentation, re-present the candidate and require fresh confirmation before mutation. The original confirmation applies only to the candidate as presented.

For batch discovery, freeze the target manifest first and apply the chosen evidence budget per target. Before declaring completion, report:
  - target count and artifact count
  - accepted candidate count and no-strong-candidate count
  - generic no-candidate artifact count
  - rejected and residual lead counts
  - accepted, prior-art, refuted, rejected, and residual counts whose sum equals total leads
  - deterministic checks run and checks skipped with reasons
  - low-confidence or triage-only targets
  - whether validation covered task shape only, artifact shape only, or actual behavior
  - a sample of repeated no-candidate artifacts and whether they include real command evidence

If most artifacts are same-shaped and no candidates were found, rerun discovery with declared command probes before finishing.

When comparing a focused pass with an earlier breadth pass, preserve the earlier artifact as the baseline and report what changed:
  - which checks breadth skipped and focused discovery ran
  - which leads stayed rejected and why
  - which candidates are new, with current command/file evidence
  - whether the difference is better evidence, narrower scope, or a changed worktree

7. Ask one enqueue confirmation.

When at least one candidate survives, ask exactly one question such as: add all, add 1 3, or no. Queue only the confirmed candidates that pass freshness revalidation. When no candidate survives, report the affirmative zero result and do not ask an enqueue question.

Queue inspection commands such as afk status, afk take --dry-run --limit 0 --json --full, and afk tasks may initialize the configured queue if it does not exist yet.
