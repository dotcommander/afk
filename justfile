set dotenv-load := false

binary := "afk"

_default:
    just --list

build:
    go build -o {{binary}} ./cmd/afk

install: build
    mkdir -p "$HOME/go/bin"
    ln -sf "$(pwd)/{{binary}}" "$HOME/go/bin/{{binary}}"
