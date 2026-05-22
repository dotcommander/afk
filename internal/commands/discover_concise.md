afk prompt --discover is the task-discovery contract. It prints guidance only; it does not mutate the queue.

Mine concrete AFK-ready candidate tasks from the material the user wants reviewed. Default to a concise operational pass; use `afk prompt --discover --full` when you need the full policy and edge-case guidance.

## Happy path

```bash
afk status --summary
afk take --dry-run --limit 0 --json --full
afk tasks --status todo --json
afk tasks --status doing --json
# inspect the requested target
# collect current file, command, or docs/source evidence
afk add --dry-run --source task-discovery --tag discovery --resource "<kind>:<target>" "<task body>"
# ask one enqueue confirmation
afk add --source task-discovery --tag discovery --resource "<kind>:<target>" "<task body>"
```

## Discovery checklist

- Classify the target from actual files or artifacts, not just its name.
- Check todo and doing tasks before broad probing so you do not rediscover duplicates.
- Inspect the smallest useful surface: manifests, local guidance, entrypoints, one primary implementation or content surface, and one contract surface such as README, tests, fixtures, schemas, examples, screenshots, or sample data.
- Run the narrowest deterministic check tied to the candidate when available.
- Accept only candidates with current evidence, atomic scope, exact verification, clear operator or product value, and about one hour or less of work for a fresh worker.
- Reject broad cleanup, generic tests, stale TODO text without corroboration, missing dependencies that look like stale install state, and product choices without a clear destination.
- Stop after 1-3 strong non-duplicate candidates unless the user explicitly asked for broad or batch discovery.

## Report before enqueueing

Include:

- targets inspected
- commands run
- accepted candidates
- rejected or duplicate leads
- skipped checks with concrete reasons
- dry-run validation result

Ask exactly one enqueue question: `add all`, `add 1 3`, or `no`.

## Task body template

Candidate bodies should start with `[discovery:<kind>:<topic>]` and include Evidence, Scope, Success, Verify with, Reject-if, and Constraints. Instantiate this template exactly:

<task-body-template>
[discovery:<kind>:<topic>] <one observable change>. Evidence: <file, command, URL, artifact, or record>. Scope: <exact package, directory, document, dataset, feature, or behavior>. Success: <observable done state>. Verify with <exact command or deterministic local check>. Reject-if: <condition that invalidates the task>. Constraints: keep changes scoped; do not refactor unrelated code.
</task-body-template>

Use stable kind labels such as repo, docs, kb, web, media, data, or workspace. Use a resource key that matches the target kind, such as `repo:<root>`, `docs:<root>`, `kb:<root>`, `web:<app>`, `media:<collection>`, `data:<dataset>`, or `workspace:<name>`.

Dry-run validation proves task-body shape only; it does not prove that discovery was deep, complete, or valuable.
