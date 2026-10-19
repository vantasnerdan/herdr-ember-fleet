#!/bin/sh
# Add a computer (one OS user on it) to the fleet.
#
#   scripts/add-host.sh NAME                 make a join link and print what to run there
#   scripts/add-host.sh NAME USER@HOST       ...and install + join over SSH, right now
#
# NAME is the machine's name in the fleet. Use the same NAME for several users
# on one computer (e.g. administrator and each agent user) and they are shown
# together under that machine.
#
# The link is made by the hub on this computer. If the hub runs elsewhere, set
# HUB_SSH=user@hub-host and the link is made there.
#
# Over SSH, the link travels on SSH's stdin: it never appears in a command
# line, a process list or shell history. SSH is only used for this one-time
# setup; after that the machine dials out to the relay and needs no SSH.
set -eu

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi
name="$1"
target="${2:-}"
here="$(cd "$(dirname "$0")/.." && pwd)"

ef() {
  if [ -n "${HUB_SSH:-}" ]; then
    # shellcheck disable=SC2029
    ssh -o BatchMode=yes "$HUB_SSH" "\$HOME/.local/bin/ember-fleet $*"
  else
    ember-fleet "$@"
  fi
}

out="$(ef link "$name")"
link="$(printf '%s\n' "$out" | grep -o 'ember-join://[^ ]*' | head -n 1)"
cmd="$(printf '%s\n' "$out" | grep -o 'curl -fsSL [^ ]*/install.sh | sh' | head -n 1)"
[ -n "$cmd" ] || cmd="(the hub is only on this computer, so use: scripts/add-host.sh $name USER@HOST)"
if [ -z "$link" ]; then
  printf '%s\n' "$out" >&2
  echo "Could not make a link (is the hub running? ember-fleet status)" >&2
  exit 1
fi

if [ -z "$target" ]; then
  cat <<EOF
Join link for "$name" (secret, works once, expires in 24 hours):

  $link

On the new computer, as the user whose herdr agents should join, run:

  $cmd

and paste the link when it asks. Or, if you can SSH there now:

  scripts/add-host.sh $name USER@HOST
EOF
  exit 0
fi

echo "Checking $target ..."
uname_sm="$(ssh -o BatchMode=yes "$target" 'uname -sm')"
case "$uname_sm" in
  "Linux x86_64") plat=linux-amd64 ;;
  "Linux aarch64"|"Linux arm64") plat=linux-arm64 ;;
  "Darwin arm64") plat=darwin-arm64 ;;
  "Darwin x86_64") plat=darwin-amd64 ;;
  *) echo "Unsupported system on $target: $uname_sm" >&2; exit 1 ;;
esac

bin="$here/dist/ember-fleet-$plat"
if [ ! -x "$bin" ]; then
  echo "Building ember-fleet for $plat ..."
  (cd "$here" && CGO_ENABLED=0 GOOS="${plat%-*}" GOARCH="${plat#*-}" go build -trimpath -ldflags "-s -w" -o "$bin" ./cmd/ember-fleet)
fi

echo "Installing ember-fleet on $target ..."
ssh -o BatchMode=yes "$target" 'mkdir -p "$HOME/.local/bin"'
scp -q "$bin" "$target:.local/bin/ember-fleet.new"
ssh -o BatchMode=yes "$target" 'chmod 755 "$HOME/.local/bin/ember-fleet.new" && mv "$HOME/.local/bin/ember-fleet.new" "$HOME/.local/bin/ember-fleet"'

if ! ssh -o BatchMode=yes "$target" 'command -v herdr >/dev/null 2>&1 || [ -x "$HOME/.local/bin/herdr" ] || [ -x /opt/homebrew/bin/herdr ] || [ -x /usr/local/bin/herdr ]'; then
  echo "Note: herdr is not installed on $target yet: curl -fsSL https://herdr.dev/install.sh | sh"
fi

echo "Joining ..."
printf '%s\n' "$link" | ssh -o BatchMode=yes "$target" 'XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}" "$HOME/.local/bin/ember-fleet" join'
echo "Done. $name should appear in the fleet within a few seconds."
