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

## When the GitHub release build fails

The Release workflow runs its own tests (with the race detector) before it
builds anything, so a test that passed locally can still stop it — a
flaky one did, for v0.37.1. release.sh then stops with **"GitHub assets
never appeared for vX.Y.Z"** or **"the GitHub Release workflow did not
succeed"**. Nothing has been published: no GitHub release, no Gitea
release, no assets — only the tag exists, on both sides. A Pi asked to
install that version reports "the download failed; nothing was changed".

1. **Find the cause.**

   ```
   gh run list --repo DanDreadless/minos --workflow Release --limit 1
   gh run view <run-id> --repo DanDreadless/minos --log-failed
   ```

2. **If it was the build, not the code** (a runner hiccup, a registry
   timeout): rerun just the failed jobs with
   `gh run rerun <run-id> --repo DanDreadless/minos --failed`, then finish
   with `scripts/release.sh --assets-only vX.Y.Z`. Same tag, same commit.

3. **If it was the code or a test:** fix it on a branch and merge to main,
   then release the **next patch version** with the same notes plus a line
   saying the previous one was never published. Never move or reuse a tag
   that was pushed — someone may have fetched it, and release.sh refuses a
   used tag anyway.

4. **Remove the dead tag**, once you have confirmed it has no release on
   either side (`gh release view vX.Y.Z` says "release not found"; Gitea's
   `GET /api/v1/repos/dreadless/minos/releases/tags/vX.Y.Z` answers 404):

   ```
   git tag -d vX.Y.Z
   git push origin :refs/tags/vX.Y.Z
   # carry the deletion to GitHub through the push mirror:
   curl -X POST -H "Authorization: token $GITEA_TOKEN" \
     http://vault-tec.local:3000/api/v1/repos/dreadless/minos/push_mirrors-sync
   ```

   `gh api repos/DanDreadless/minos/git/refs/tags/vX.Y.Z` answering 404
   confirms it is gone there too.

If instead the binaries and GitHub release exist but a later job failed
(the Docker image, say), the release is half-published: rerun the failed
job as in step 2 and finish with `--assets-only` — do not cut a new
version for it.

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
