# Releasing Minos

Since the August 2026 migration, the **local Gitea instance is the source
of truth** and GitHub is a public mirror. Releases still end up on GitHub
— binaries, checksums, and GHCR container images — because the mirrored
tag triggers the same release workflow there.

## One-time setup: the push mirror

On Gitea: **Settings → Repository → Mirror Settings → Add push mirror**

- Git remote URL: `https://github.com/DanDreadless/minos.git`
- Authorization: a GitHub personal access token with the `repo` scope
  (fine-grained: Contents read/write on the mirror repo)
- Enable **Sync when commits are pushed**

That mirrors branches *and tags*. GitHub Actions treats a mirrored tag
push like any other, so `release.yml` runs there on GitHub's own runners
with GitHub's own token — no extra secrets anywhere.

## Cutting a release

```
git tag -s v0.19.0 -m "Minos v0.19.0"
git push origin v0.19.0
```

What happens, in order:

1. **Gitea Actions** (local Docker runner): the race-detector test job,
   then a stub release entry on Gitea whose notes point at the GitHub
   assets. If the tests fail here, delete the tag, fix, re-tag.
2. **Push mirror** replicates the tag to GitHub.
3. **GitHub Actions** builds the seven binary targets, creates the GitHub
   Release with checksums, and pushes multi-arch images to GHCR — the
   pre-migration pipeline, unchanged.

The in-app update checker (`internal/updates`) reads GitHub releases and
keeps working without modification.

## Notes

- The `server_url` gates in `release.yml` keep each side to its own jobs:
  Gitea never runs `gh` or pushes to GHCR; GitHub never re-creates the
  Gitea release.
- If the mirror repo ever moves, update the URL in the `gitea-release`
  job body and this document.
