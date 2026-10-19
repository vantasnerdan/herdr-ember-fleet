#!/bin/sh
# Build the web page and the ember-fleet binaries into dist/.
#   scripts/build.sh            all platforms (linux, darwin × amd64, arm64)
#   scripts/build.sh --host     only this computer's platform
set -eu
cd "$(dirname "$0")/.."
version="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"

if [ -f web/package.json ]; then
  echo "Building the web page..."
  (cd web && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run build)
fi
touch internal/webui/dist/.gitkeep

mkdir -p dist
targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"
if [ "${1:-}" = "--host" ]; then
  targets="$(go env GOOS)/$(go env GOARCH)"
fi
for t in $targets; do
  os="${t%/*}"; arch="${t#*/}"
  out="dist/ember-fleet-$os-$arch"
  echo "Building $out ($version)..."
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out" ./cmd/ember-fleet
done
echo "$version" > dist/VERSION   # the hub pushes these builds to docks as this version
(cd dist && sha256sum ember-fleet-* > SHA256SUMS 2>/dev/null || shasum -a 256 ember-fleet-* > SHA256SUMS)
echo "Done: dist/"
