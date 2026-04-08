# Releasing Satsbook

Releases are fully automated by `.github/workflows/release.yml`, triggered when a
semver tag matching `v*` is pushed to `main`.

## What a release does

1. Builds and pushes a multi-arch Docker image (`linux/amd64`, `linux/arm64`) to
   `ghcr.io/satsbook/satsbook:<version>` and `:latest`.
2. Cross-compiles `satsbook-linux-amd64` and `satsbook-linux-arm64` binaries.
3. Creates a GitHub Release with auto-generated notes and the binaries attached.
4. Clones `satsbook/umbrel-app-store`, bumps the version in
   `satsbook-satsbook/umbrel-app.yml` and the image tag in
   `satsbook-satsbook/docker-compose.yml`, and pushes the change to `main`.

## Cutting a release

```bash
# From an up-to-date main:
git checkout main
git pull --ff-only
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

Then watch the workflow at:
https://github.com/satsbook/satsbook/actions/workflows/release.yml

## One-time setup

### `UMBREL_STORE_PAT` secret (required)

The workflow needs a fine-grained Personal Access Token to push to the app store
repo. Create one at https://github.com/settings/personal-access-tokens/new with:

- **Resource owner**: `satsbook`
- **Repository access**: only `satsbook/umbrel-app-store`
- **Permissions**: Repository → **Contents: Read and write**

Add it as a repo secret on `satsbook/satsbook` named `UMBREL_STORE_PAT`.

### Make the GHCR package public (after first release)

After the first successful release, the container image will be private by
default. Make it public:

1. https://github.com/orgs/satsbook/packages
2. Select `satsbook` → Package settings → Change visibility → Public.

## Version reporting

The running binary prints its version on startup:

```
satsbook 0.2.0 - Bitcoin node analytics and accounting
```

This is injected at build time via `-ldflags "-X main.version=<tag>"` in both
the Dockerfile and the release workflow. Local `make build` produces a binary
that reports `dev`.
