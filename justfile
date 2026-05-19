set dotenv-load := false

binary := "afk"

_default:
    just --list

build:
    go build -o {{binary}} ./cmd/afk

install: build
    mkdir -p "$HOME/go/bin"
    rm -f "$HOME/go/bin/{{binary}}"
    install -m 0755 "{{binary}}" "$HOME/go/bin/{{binary}}"
