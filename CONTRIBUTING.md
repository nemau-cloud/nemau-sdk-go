# Contributing to nemau-sdk-go

Bug reports, fixes and improvements are welcome. This page explains how to
build the SDK, run the tests and propose a change.

## Reporting a problem

Open an issue at <https://github.com/nemau-cloud/nemau-sdk-go/issues>. Include:

- the SDK version (`nemau.Version`) and the Go version;
- the method you called and the error it returned;
- for a failed task, the task id from the error message.

Do not paste access keys, tokens, kubeconfigs or presigned URLs into an issue.
To report a vulnerability, follow [SECURITY.md](SECURITY.md) instead of opening
an issue.

## Building and testing

The SDK requires Go 1.27 or later. From the repository root:

```sh
go build ./... && go vet ./... && go test ./... && gofmt -l .
```

`gofmt -l .` must print nothing. The CI pipeline also runs the tests with the
race detector:

```sh
go test -race ./...
```

`make check` formats the code with `gofmt -w`, then runs `go vet` and the
tests.

The tests run against a local `httptest` server (see `newTestClient` in
`client_test.go`). They need no account and no network access. A change in
behaviour comes with a test that covers it.

## Dependencies

The module depends on the Go standard library only, and `go.mod` has no
`require` directive. Do not add a dependency. If a change seems to need one,
open an issue to discuss it first.

## Proposing a change

1. For anything larger than a small fix, open an issue first to agree on the
   approach.
2. Fork the repository and create a branch from `main`.
3. Make the change, with tests. Update the doc comments and the guide in
   [docs/](docs/README.md) when the behaviour or the API changes.
4. Add an entry to [CHANGELOG.md](CHANGELOG.md) under `[Unreleased]`, in the
   matching section (`Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`).
   The changelog follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
5. Open a pull request against `main` on GitHub, and describe what the change
   does and why.

## Commit messages

Commit messages follow the
[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) format:
a type, an optional scope, and a short summary.

```text
fix(storage): refuse a listing limit above 1000 before sending the request
feat(dns): add VerifyZoneAndWait
docs: describe the retry policy
```

Common types are `feat`, `fix`, `docs`, `test`, `refactor` and `chore`. Write
the summary in English, and use the body to explain why the change is needed.
