# Security Policy

## Reporting a vulnerability

If you discover a security vulnerability in `afk`, please report it responsibly via a **GitHub private security advisory**:

https://github.com/dotcommander/afk/security/advisories/new

Do **not** open a public issue for security vulnerabilities.

## Scope

`afk` is a local-first CLI. Its only network surface is the optional `afk serve` dashboard, which binds to `127.0.0.1` by default (a non-loopback `--addr` prints a warning). Reports about the `goal`/`loop` command-execution paths, the SQLite queue, or local correctness and privilege issues are in scope.
