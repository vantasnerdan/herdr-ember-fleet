#!/bin/sh
# Run a throwaway relay, hub and dock on this computer, against your own herdr,
# in a temporary config folder. The page is at http://127.0.0.1:47900 and the
# Vite dev server (cd web && npm run dev) can proxy to it.
#
#   scripts/dev.sh          start (Ctrl-C stops everything)
set -eu
cd "$(dirname "$0")/.."
go build -o /tmp/ember-fleet-dev ./cmd/ember-fleet
home="$(mktemp -d /tmp/ember-fleet-dev.XXXXXX)"
export EMBER_FLEET_HOME="$home"
bin=/tmp/ember-fleet-dev
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$home"' EXIT INT TERM
$bin relay init --public 127.0.0.1:47820 --listen 127.0.0.1:47820 | grep -o 'ember-relay://[^ ]*' > "$home/hublink"
$bin relay serve & sleep 0.5
$bin hub init --fleet "Dev fleet" < "$home/hublink" >/dev/null
$bin hub serve --listen 127.0.0.1:47900 --dev-origin http://localhost:5173 & sleep 1.5
$bin link this-computer | grep -o 'ember-join://[^ ]*' > "$home/joinlink"
$bin join --no-service < "$home/joinlink" >/dev/null
$bin dock serve & sleep 1
echo
$bin login-link
echo "(EMBER_FLEET_HOME=$home)"
wait
