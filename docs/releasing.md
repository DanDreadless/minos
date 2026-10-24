# Releasing Minos

**Gitea (`vault-tec.local`) is the source of truth. GitHub is a public
mirror. Every release is published on both**, carrying identical
binaries and checksums.

Gitea runs in a NAS container, so it never does build work: repository
Actions are disabled there and every workflow job is gated to
`github.server_url`. The mirror's runners build the artifacts; a
workstation drives the release and copies the results back to Gitea.

## Day-to-day

Push all work to Gitea — branches included, not just `main`. The
configured **push mirror** replicates every ref to GitHub automatically
(sync-on-commit), so nothing needs pushing to GitHub by hand.

## Cutting a release

```
cp .env.example .env    # first time only; .env is git-ignored
scripts/release.sh v0.19.1
```

The script sources `.env` for `GITEA_TOKEN` and `GITHUB_TOKEN`
(environment variables you export take precedence).

That does the whole flow:

1. **Preflight** — on `main`, clean tree, in sync with `origin`, tag not
   already used.
2. **Verify** — `gofmt` against the *committed blobs* (a CRLF checkout
   makes the working-tree check useless, and golangci-lint has missed a
   real problem here), `go test ./...`, golangci-lint, `svelte-check`,
   and a frontend build that must leave `web/dist` unchanged.
   `-race` needs cgo, so the GitHub run remains the authoritative race
   check — and it blocks the release there.
3. **Tag and push to Gitea**, then mirror to GitHub (the push mirror
   handles this; `GITHUB_TOKEN` is only needed if the mirror is off).
4. **Wait for the GitHub build** — binaries for seven targets, the
   GitHub Release, and multi-arch images on GHCR.
5. **Verify checksums** of every downloaded asset before publishing.
6. **Publish the Gitea release** with generated notes and the same
   assets attached.

Useful flags: `--assets-only <tag>` re-syncs assets onto an existing
release (also the recovery path if a run dies partway), `--skip-verify`
when the checks just ran, `--notes-file <path>` for hand-written notes.

## Release signing

Every release's `checksums.txt` is signed (Ed25519) by the release workflow
with the `MINOS_SIGNING_KEY` Actions secret, producing
`checksums.txt.sig`; the public half is `PublicKey` in
`internal/release/release.go`, compiled into every binary, and the
one-click updater refuses any release that does not verify. The signed
message includes the tag, so an old release cannot be passed off as a new
one. `release.sh` checks the signature before mirroring to Gitea.

The private seed is also kept in `.env` (`MINOS_SIGNING_KEY`) — back it up.
If it is lost or leaks, generate a new key and release binaries carrying
the new public key. Installed updaters only trust the old key, so every
install then needs one upgrade with the install command; after that the
button works again. Rotate promptly after a leak.

```sh
go run ./scripts/signrelease -keygen seed.txt   # prints the new public key
gh secret set MINOS_SIGNING_KEY < seed.txt     # then put the public key in release.go
```

## Tokens

- **Gitea token** — user → Settings → Applications → Generate token,
  `repository` write scope.
- **GitHub PAT** — needs `repo` **and** `workflow` scope; pushes that
  touch `.github/workflows/` are rejected otherwise. The push mirror
  stores one (Settings → Repository → Mirror Settings); rotating it
  means updating it there too.

The in-app update checker (`internal/updates`) reads GitHub releases and
needs no change.

## If you ever want CI on Gitea

Register an act_runner **on a workstation, not the NAS**, re-enable
Actions for the repo, and drop the `github.server_url` gates from the
jobs you want to run there. The `gh`/GHCR steps are GitHub-specific and
should stay gated regardless.
