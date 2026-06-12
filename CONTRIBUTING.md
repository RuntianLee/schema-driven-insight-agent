# Contributing

## Development

```bash
go build ./... && go vet ./... && go test -race ./...      # root module (the only module — examples/toygame is YAML-only since v0.2.0)
go test -tags=integration ./etl/ -v                        # integration tests (needs Docker; testcontainers Postgres)
```

CI runs the same, plus a deterministic toygame smoke + eval gate (`cmd/seed` → `cmd/eval`, zero-code path) and a separate `integration` job.

## Release convention

Consumers pin this module by tag — so **any PR that changes Go code must be followed by a new tag** on the merged main (`git tag vX.Y.Z && git push origin vX.Y.Z`). Docs-only changes don't need a tag.

- Patch (`v0.1.x`): fixes and additive, backward-compatible changes.
- Minor (`v0.x.0`): new capabilities; may loosen/extend the adapter contract.
- The API may still evolve before `v1`; breaking changes are allowed but must be called out in the tag's release notes.

Rationale: local workspaces (`go.work`) build against HEAD, while external consumers and standalone CI resolve the published tag. An untagged code change silently skews the two ("local ahead, release behind") — tagging on merge keeps them aligned.

## Code conventions

- Comments follow the existing style (Chinese, explain *why* and contracts, not *what*).
- Every fix ships with a test that fails before and passes after.
- Naming in tests/prompts/fixtures must be fictional (toygame-style) — never real adapter schema vocabulary.
