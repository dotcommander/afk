afk prompt --discover is a workflow stub.

Mine concrete AFK-ready candidate tasks from the material the user wants reviewed. This command only prints guidance and does not open or create the queue.

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

  Minimum Level 2 evidence for each repo/web target:
  - inspect local guidance plus manifest files
  - gather target-selection signals before choosing files: recent churn, complex or large files, test coverage, imports/callers, and current marker or command pain
  - inspect at least one command, route, feature map, or package script surface
  - inspect at least three core source files tied to primary behavior, unless fewer exist
  - inspect at least one contract surface such as README command docs, tests, fixtures, schemas, config examples, screenshots, or sample data
  - run the narrowest declared deterministic check from the manifest or docs, such as npm run check, bun run check, go test ./..., php artisan test, pytest, or a documented parser/import validation
  - record an explicit skip reason only when the check has a concrete blocker such as missing dependency, credentials, network, cost, sandbox restriction, or no matching command
  - record rejected leads with reasons, including why TODO/backlog hits were not promoted

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

  afk count
  afk ready
  afk ls --status failed --json
  afk ls --status working --json

4. Convert evidence into queueable mini-specs.

  Accept only candidates with:
  - current evidence from files, command output, tests, or docs/source mismatch
  - corroboration beyond a single marker when the lead starts from TODO/FIXME/HACK/XXX text
  - atomic scope that a worker can complete independently
  - about one hour or less for a fresh worker
  - Evidence:, Scope:, Success:, Verify with, and Reject-if:
  - exact verification command or deterministic local check
  - clear value, low churn risk, and no broad cleanup/refactor wording
  - no duplicate pending or working task for the same behavior

5. Rank by impact and reject before enqueueing.

  First ask: is this value or churn?
  Value changes remove a real bug, unblock a workflow, prevent a plausible failure mode, reduce operator pain, or make future work less ambiguous with measured payoff.
  Churn changes are style-only, cosmetic polish, generic cleanup, speculative abstraction, pure test/docs padding, or "nice to have" work without current proof.
  Reject churn even when it is easy.

  Rank 3-7 strong candidates using this priority order:
  1. core behavior or correctness: real bugs, broken workflows, bad state handling, auth/session/cache/data issues, race or stale-state hazards
  2. high-impact product or operator value: missing workflow pieces, broken UX paths, deployment/runtime blockers, local-dev blockers, config/env traps
  3. safety or hardening: unsafe IO, unbounded reads, auth validation gaps, bad error handling, data loss risks
  4. performance or reliability: cache stampedes, N+1 queries, resource leaks, retry/backoff issues, expensive hot paths
  5. maintainability with measured payoff: small duplication removal only when current code proves risk or friction
  6. tests/docs only as enablers for higher-impact work or broken command/docs-source contradictions
  7. pure test gaps or docs polish last; do not present them as primary discoveries when stronger codebase tasks exist
  Show rejected stale, broad, duplicate, risky, unverified, or churn leads.
  Use blocked_by when candidates must run in order.

  If every lead is low-impact or uncorroborated, say "no strong candidate" and explain what was inspected. Do not pad the output with easy TODO cleanup, docs polish, dependency drift, or generic tests.
  If a declared local gate fails, first ask whether a small fix can make the gate pass without product choices or broad refactoring. Broken declared checks are often better AFK candidates than TODO markers because they have exact verification.
  If a TODO or product promise points at a missing behavior but the destination/provider/architecture is absent, reject or mark provisional rather than forcing an ambiguous task. Prefer the narrower failing check when one exists.

6. Validate task bodies with dry-run.

Candidate bodies should start with [discovery:<kind>:<topic>] and include Evidence:, Scope:, Success:, Verify with, and Reject-if:.
Use a stable kind such as repo, docs, kb, web, media, data, or workspace. The kind is a free-form routing label; those values are conventions, not a closed validator list.
Use concrete file, command, URL, artifact, or record references in Evidence:/Scope:.
Use a resource key that matches the target kind, such as repo:<root>, docs:<root>, kb:<root>, web:<app>, media:<collection>, data:<dataset>, or workspace:<name>.

Validate before enqueueing:
  afk add --dry-run --source task-discovery --tag discovery --resource "<kind>:<target>" \
    "[discovery:<kind>:<topic>] <one observable change>. Evidence: <file, command, URL, artifact, or record>. Scope: <exact package, directory, document, dataset, feature, or behavior>. Success: <observable done state>. Verify with <exact command or deterministic local check>. Reject-if: <condition that invalidates the task>. Constraints: keep changes scoped; do not refactor unrelated code."

Dry-run validation proves that a task body is admissible. It does not prove that discovery was deep, complete, or valuable.

For batch discovery, freeze the target manifest first and apply the chosen evidence budget per target. Before declaring completion, report:
  - target count and artifact count
  - accepted candidate count and no-strong-candidate count
  - generic no-candidate artifact count
  - rejected or provisional lead count
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

Ask exactly one question such as: add all, add 1 3, or no. Queue only the confirmed candidates that pass dry-run validation.

Queue inspection commands such as afk count, afk ready, and afk ls may initialize the configured queue if it does not exist yet.
