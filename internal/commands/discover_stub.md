afk discover is a workflow stub.

Mine concrete AFK-ready candidate tasks from the target repo. This command only prints guidance and does not open or create the queue.

Start with:
  git status --porcelain=v2
  rg -n "TODO|FIXME|HACK|XXX|OPTIMIZE" --glob '!vendor/**' --glob '!node_modules/**'
  afk count
  afk ready
  afk ls --status failed --json
  afk ls --status working --json

Accept only candidates with:
  - current evidence from files, command output, tests, or docs/source mismatch
  - atomic scope that a worker can complete independently
  - exact verification command
  - low churn risk and no broad cleanup/refactor wording
  - no duplicate pending or working task for the same behavior

Before enqueueing:
  1. Rank 3-7 strong candidates.
  2. Show rejected stale, broad, duplicate, risky, or unverified leads.
  3. Ask one confirmation question such as: add all, add 1 3, or no.
  4. Queue only the confirmed candidates that pass dry-run validation.

Candidate bodies should start with [discovery:<repo>:<topic>] and include Evidence:, Scope:, and Verify with.
Use absolute paths in Evidence:/Scope:, or pass --cwd "$(pwd)".

Validate before enqueueing:
  afk add --dry-run --cwd "$(pwd)" --source task-discovery --tag discovery --resource "repo:$(pwd)" \
    "[discovery:<repo>:<topic>] <one observable change>. Evidence: $(pwd)/path/file.go:1. Scope: $(pwd)/path/file.go. Constraints: keep changes scoped; do not refactor unrelated code. Verify with go test ./..."

Queue inspection commands such as afk count, afk ready, and afk ls may initialize the configured queue if it does not exist yet.
