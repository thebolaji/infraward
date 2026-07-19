# Contributing to InfraWard

## The easiest contribution: extend the mapping table

Most contributions will be adding a resource type. That's a data table
entry in [`internal/mapping/mapping.go`](internal/mapping/mapping.go), not
new Go code:

```go
{TerraformType: "aws_some_resource", CloudControlType: "AWS::Service::SomeResource", Source: CloudControl},
```

**Verify coverage against a real AWS account before adding an entry.**
Several entries in the current table needed correction after testing live —
some Cloud Control `List` handlers exist on paper but don't actually
support flat listing (see the comments above `Table` for specifics). A PR
adding a mapping entry that hasn't been checked against a real account
won't be merged.

## Branching strategy: GitHub Flow

- `main` is always releasable. Tagging it `vX.Y.Z` triggers a real release
  (GoReleaser builds and publishes binaries — see
  [`.goreleaser.yml`](.goreleaser.yml)).
- No `develop` branch, no long-lived release branches. Every change is a
  short-lived branch off `main`, merged back via pull request.
- Branch names are prefixed by kind:

  | Prefix | For |
  |---|---|
  | `feat/` | New functionality |
  | `fix/` | Bug fixes |
  | `docs/` | Documentation only |
  | `test/` | Test-only changes |
  | `chore/` | Tooling, CI, dependencies, releases |

  e.g. `feat/adopt-command`, `fix/route53-pagination`.

- Every PR must pass CI (build, vet, `gofmt` check, race-tested tests,
  cross-platform build matrix) before merging.
- Rebase or squash-merge, whichever keeps `main`'s history readable — avoid
  merge commits.

## Commit messages

Describe what changed and why, for a reader who wasn't in the room when it
happened. No narration of the process that produced the change (no "fixed
after testing," no session/date references) — just the resulting fact and
its rationale.

## Local checks before opening a PR

```sh
go build ./...
go vet ./...
gofmt -l .        # must print nothing
go test ./... -race
```

These are exactly what CI runs; passing them locally means CI will pass too.
