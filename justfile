set dotenv-load := false

binary := "afk"
version := `git describe --tags --always --dirty`

_default:
    just --list

build:
    go build -ldflags "-X main.version={{version}}" -o {{binary}} ./cmd/afk

test-short:
    go test -short ./...

verify:
    go build ./...
    go test ./...
    go vet ./...
    git diff --check

install: build
    mkdir -p "$HOME/go/bin"
    rm -f "$HOME/go/bin/{{binary}}"
    install -m 0755 "{{binary}}" "$HOME/go/bin/{{binary}}"
