# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

yocode is a code assistant project in its earliest stage: the repository contains only a README, an MIT LICENSE, and a Go `.gitignore`. There is no source code, build setup, or test suite yet.

The `.gitignore` suggests Go is the intended language. Until a `go.mod` exists, confirm the language and module path with the user before scaffolding.

## Commands

None are defined yet. Once a Go module exists, the standard toolchain applies:

- Build: `go build ./...`
- Test all: `go test ./...`
- Single test: `go test ./path/to/pkg -run '^TestName$'`
- Vet: `go vet ./...`

Update this section if a Makefile, task runner, or linter config (e.g. golangci-lint) is added.

## Architecture

Not yet established. Document the high-level structure here once the first packages land.
