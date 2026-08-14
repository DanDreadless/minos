# Releasing Minos

Since the August 2026 migration, the **local Gitea instance is the source
of truth** and GitHub is a public mirror that builds the artifacts.
Releases still land on GitHub — binaries, checksums, and GHCR container
images — because the mirrored tag triggers the release workflow there.

**No CI runs on Gitea.** It lives in a NAS container that shouldn't be
loaded with build jobs, so repository Actions are off and every workflow
job is additionally gated to `github.server_url`. Verification happens on
a workstation before tagging, and on GitHub's runners after.

## One-time setup: the push mirror

On Gitea: **Settings → Repository → Mirror Settings → Add push mirror**

- Git remote URL: `https://github.com/DanDreadless/minos.git`
- Authorization: a GitHub personal access token with `repo` **and**
  `workflow` scope (fine-grained: Contents + Workflows read/write on the
  mirror repo). The `workflow` permission is required because pushes
  that touch `.github/workflows/` are otherwise rejected.
- Enable **Sync when commits are pushed**

Mirroring is a push of refs, so it carries branches and tags but not
release entries — hence the manual step below.

Without the mirror configured, push to GitHub by hand:

```
git push https://x-access-token:<PAT>@github.com/DanDreadless/minos.git main v0.19.0
```

## Cutting a release

1. **Verify locally** (this is the gate Gitea no longer provides):

   ```
   go test ./...            # -race needs cgo; the Linux CI run covers it
   golangci-lint run
   cd web && npx svelte-check && npm run build   # commit web/dist if changed
   ```

2. **Tag and push to Gitea:**

   ```
   git tag -a v0.19.0 -m "Minos v0.19.0: <headline>"
   git push origin v0.19.0
   ```

3. **Mirror to GitHub** (automatic with the push mirror; otherwise the
   manual push above). The mirrored tag starts the release workflow:
   race-detector tests, seven binary targets, the GitHub Release with
   checksums, and multi-arch images to GHCR.

4. **Record the release on Gitea.** Either "New release" from the tag in
   the web UI, or the API:

   ```
   curl -X POST -H "Authorization: token <GITEA_TOKEN>" \
     -H "Content-Type: application/json" \
     -d '{"tag_name":"v0.19.0","name":"Minos v0.19.0",
          "body":"Notes here. Binaries and images: https://github.com/DanDreadless/minos/releases/tag/v0.19.0"}' \
     http://vault-tec.local:3000/api/v1/repos/dreadless/minos/releases
   ```

   Point the notes at the GitHub assets; Gitea holds no binaries.

The in-app update checker (`internal/updates`) reads GitHub releases and
needs no change.

## If you ever want CI on Gitea

Register an act_runner **on a workstation, not the NAS**, re-enable
Actions for the repo, and drop the `github.server_url` gates from the
jobs you want to run there. The release workflow's `gh`/GHCR steps are
GitHub-specific and should stay gated regardless.
