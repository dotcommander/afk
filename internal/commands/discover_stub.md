afk discover is a workflow stub.

Mine concrete AFK-ready candidate tasks from the current local path. This command only prints guidance and does not open or create the queue.

1. Classify the path.

  pwd
  find . -maxdepth 2 -type f | sed 's#^\./##' | sort | head -200
  find . -maxdepth 2 -type d | sed 's#^\./##' | sort | head -80

  Decide the primary purpose before proposing work: code repo, web app, docs set, knowledge base, media archive, data folder, mixed workspace, or other local collection.

2. Gather bounded evidence that matches the path.

  code repo: git status --porcelain=v2; git diff --stat HEAD; rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
  docs or knowledge base: rg -n "TODO|FIXME|stale|broken|missing|TBD" .; check internal links, indexes, frontmatter, duplicate titles, and orphaned notes
  web UI: inspect package/app manifests, routes, screenshots or smoke-test scripts, broken asset paths, and docs/source drift
  media archive: inspect filenames, sidecar metadata, duplicates, missing thumbnails/subtitles, broken playlists, and index/catalog files
  data folder: inspect schemas, samples, checksums, import/export scripts, stale generated outputs, and validation commands
  mixed path: split candidates by subdirectory and keep each task scoped to one path purpose

3. Check the queue for duplicates before suggesting candidates.

  afk count
  afk ready
  afk ls --status failed --json
  afk ls --status working --json

4. Convert evidence into queueable mini-specs.

  Accept only candidates with:
  - current evidence from files, command output, tests, or docs/source mismatch
  - atomic scope that a worker can complete independently
  - about one hour or less for a fresh worker
  - Evidence:, Scope:, Success:, Verify with, and Reject-if:
  - exact verification command or deterministic local check
  - low churn risk and no broad cleanup/refactor wording
  - no duplicate pending or working task for the same behavior

5. Rank by impact and reject before enqueueing.

  Rank 3-7 strong candidates using this priority order:
  1. core behavior or correctness: real bugs, broken workflows, bad state handling, auth/session/cache/data issues, race or stale-state hazards
  2. high-impact product or operator value: missing workflow pieces, broken UX paths, deployment/runtime blockers, local-dev blockers, config/env traps
  3. safety or hardening: unsafe IO, unbounded reads, auth validation gaps, bad error handling, data loss risks
  4. performance or reliability: cache stampedes, N+1 queries, resource leaks, retry/backoff issues, expensive hot paths
  5. maintainability with measured payoff: small duplication removal only when current code proves risk or friction
  6. tests/docs only as enablers for higher-impact work or broken command/docs-source contradictions
  7. pure test gaps or docs polish last; do not present them as primary discoveries when stronger codebase tasks exist
  Show rejected stale, broad, duplicate, risky, or unverified leads.
  Use blocked_by when candidates must run in order.

6. Validate task bodies with dry-run.

Candidate bodies should start with [discovery:<kind>:<topic>] and include Evidence:, Scope:, Success:, Verify with, and Reject-if:.
Use a stable kind such as repo, docs, kb, web, media, data, or path. The kind is a free-form routing label; those values are conventions, not a closed validator list.
Use absolute paths in Evidence:/Scope:, or pass --cwd "$(pwd)".
Use a resource key that matches the path kind, such as repo:$(pwd), docs:$(pwd), kb:$(pwd), web:$(pwd), media:$(pwd), data:$(pwd), or path:$(pwd).

Validate before enqueueing:
  afk add --dry-run --cwd "$(pwd)" --source task-discovery --tag discovery --resource "<kind>:$(pwd)" \
    "[discovery:<kind>:<topic>] <one observable change>. Evidence: $(pwd)/path/file.ext:1. Scope: $(pwd)/path. Success: <observable done state>. Verify with <exact command or deterministic local check>. Reject-if: <condition that invalidates the task>. Constraints: keep changes scoped; do not refactor unrelated code."

7. Ask one enqueue confirmation.

Ask exactly one question such as: add all, add 1 3, or no. Queue only the confirmed candidates that pass dry-run validation.

Queue inspection commands such as afk count, afk ready, and afk ls may initialize the configured queue if it does not exist yet.
