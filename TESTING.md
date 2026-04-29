# Testing on Umbrel

## Pre-release testing workflow

Use this to verify a build works on your Umbrel **before** tagging a release.

### 1. Build the test image

**Option A — GitHub Actions (recommended):**

Go to [Actions → Test Image → Run workflow](https://github.com/satsbook/satsbook/actions/workflows/test-image.yml), pick the branch, and click "Run workflow". This builds a multi-arch image and pushes it as `ghcr.io/satsbook/satsbook:test`.

**Option B — Local build:**

```bash
docker buildx build --platform linux/arm64 --builder multiarch \
  -t ghcr.io/satsbook/satsbook:test --push \
  --build-arg VERSION=test .
```

### 2. Deploy to Umbrel

SSH into your Umbrel:

```bash
ssh umbrel@umbrel.local
```

Find the docker-compose file:

```bash
sudo find / -path "*/satsbook-satsbook/docker-compose.yml" 2>/dev/null
```

Update the image tag (replace the path if different):

```bash
sudo sed -i 's|image: ghcr.io/satsbook/satsbook:.*|image: ghcr.io/satsbook/satsbook:test|' \
  /path/to/satsbook-satsbook/docker-compose.yml
```

Restart the app (try whichever works on your Umbrel version):

```bash
# Option 1: umbreld CLI (newer Umbrel)
sudo umbreld client apps.restart.mutate --input '"satsbook-satsbook"'

# Option 2: docker compose directly
cd /path/to/satsbook-satsbook/
sudo docker compose pull && sudo docker compose up -d
```

### 3. Verify

Check logs:

```bash
sudo docker logs -f $(sudo docker ps --filter name=satsbook-satsbook_server -q)
```

Open the app in your browser and verify it works.

### 4. Restore the released version

After testing, restore the production image tag:

```bash
sudo sed -i 's|image: ghcr.io/satsbook/satsbook:.*|image: ghcr.io/satsbook/satsbook:1.1.2|' \
  /path/to/satsbook-satsbook/docker-compose.yml
sudo docker compose pull && sudo docker compose up -d
```

(Replace `1.1.2` with the current released version.)

## Cutting a release

Once the test image is verified on Umbrel:

```bash
git tag v1.x.x
git push origin v1.x.x
```

This triggers the [Release workflow](https://github.com/satsbook/satsbook/actions/workflows/release.yml) which:
1. Builds & pushes the multi-arch Docker image
2. Cross-compiles linux binaries
3. Creates a GitHub Release with categorized notes
4. Auto-updates the [community app store](https://github.com/satsbook/umbrel-app-store)

## Local Docker testing (no Umbrel needed)

For quick smoke tests without an Umbrel:

```bash
./scripts/test-docker.sh
```

This builds the image, runs it locally on http://localhost:3000, and verifies the app starts. Useful for catching startup crashes before pushing anything.
