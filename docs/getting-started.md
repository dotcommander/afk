# getting started

Add a task:

```sh
id=$(afk add "fix the failing queue test")
```

Inspect work:

```sh
afk status
afk tasks
afk find queue --json
afk task "$id"
```

Preview what an agent could claim:

```sh
afk take --dry-run --limit 5 --json
```

Claim and finish one task:

```sh
claimed=$(afk take --lease 30m --worker codex:1)
id=$(printf '%s\n' "$claimed" | jq -r .id)

# do the work
afk set "$id" done
```

Record failure without losing history:

```sh
afk set "$id" failed "missing credentials"
```

Hide obsolete work without deleting history:

```sh
afk set "$id" deleted "superseded by a narrower task"
```

Run the visibility layer:

```sh
afk serve
```
