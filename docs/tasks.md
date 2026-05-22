# tasks

Add a task:

```sh
afk add "fix the failing queue test"
afk add --dry-run --json "validate this task shape"
```

Useful metadata:

```sh
afk add --tag repo:afk --priority high --source roadmap.md "review scheduler indexes"
afk add --cwd /path/to/repo --resource repo:/path/to/repo "run the local smoke test"
afk add --blocked-by 123 "update docs after task 123 lands"
```

List and search:

```sh
afk tasks
afk tasks --status todo --json
afk tasks --status deleted
afk find scheduler --json
```

Inspect one task:

```sh
afk task 123
afk task 123 --json
```

Set status:

```sh
afk set 123 done "implemented and tested"
afk set 123 failed "missing credentials"
afk set 123 deleted "superseded by a narrower task"
```

Deleted tasks are hidden from default listings but remain available through `afk tasks --status deleted` and `afk task <id>`.
