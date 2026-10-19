#!/bin/sh
# Move the hub (the web page and its records) from this computer to a server.
#
#   scripts/move-hub.sh USER@HOST [--listen 127.0.0.1:4790] [--url https://fleet.example.com]
#       The hub listens on a local port on the server (a user service, no
#       sudo). Put your own TLS proxy in front (Pangolin, Caddy, nginx, an
#       SSH tunnel...) and give its address with --url, or later with
#       `ember-fleet hub set-url https://...` on the server.
#
#   scripts/move-hub.sh USER@HOST --public DOMAIN [--email EMAIL]
#       The hub serves HTTPS itself on :443 with Let's Encrypt (a system
#       service as that user, sudo needed; ports 80 and 443 must be reachable).
#
# The hub's key and records move with it, so no machine has to pair again.
# The local copy is kept (stopped) as ~/.config/ember-fleet/hub.moved-DATE.
set -eu

usage() { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
[ $# -ge 1 ] || usage
target="$1"; shift
listen="127.0.0.1:4790"; url=""; public=""; email=""
while [ $# -gt 0 ]; do
  case "$1" in
    --listen) listen="$2"; shift 2 ;;
    --url) url="$2"; shift 2 ;;
    --public) public="$2"; shift 2 ;;
    --email) email="$2"; shift 2 ;;
    *) usage ;;
  esac
done
here="$(cd "$(dirname "$0")/.." && pwd)"
root="${EMBER_FLEET_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/ember-fleet}"
hubdir="$root/hub"

[ -f "$hubdir/hub.json" ] || { echo "No hub on this computer ($hubdir)" >&2; exit 1; }

echo "Checking $target ..."
uname_sm="$(ssh -o BatchMode=yes "$target" 'uname -sm')"
case "$uname_sm" in
  "Linux x86_64") plat=linux-amd64 ;;
  "Linux aarch64"|"Linux arm64") plat=linux-arm64 ;;
  *) echo "The hub needs a Linux server with systemd; $target is $uname_sm" >&2; exit 1 ;;
esac
if ssh -o BatchMode=yes "$target" '[ -f "$HOME/.config/ember-fleet/hub/hub.json" ]'; then
  echo "$target already has a hub; not overwriting it." >&2
  exit 1
fi
[ -x "$here/dist/ember-fleet-$plat" ] || "$here/scripts/build.sh"

echo "Stopping the hub here ..."
ember-fleet service remove hub || true

echo "Copying the hub's key and records ..."
ssh -o BatchMode=yes "$target" 'mkdir -p "$HOME/.local/bin" && mkdir -p -m 700 "$HOME/.config/ember-fleet/hub"'
tar -C "$hubdir" -cf - key.pem cert.pem hub.json | ssh -o BatchMode=yes "$target" 'umask 077 && tar -C "$HOME/.config/ember-fleet/hub" -xf -'

echo "Installing ember-fleet and the downloads it serves to new machines ..."
scp -q "$here/dist/ember-fleet-$plat" "$target:.local/bin/ember-fleet.new"
ssh -o BatchMode=yes "$target" 'chmod 755 "$HOME/.local/bin/ember-fleet.new" && mv "$HOME/.local/bin/ember-fleet.new" "$HOME/.local/bin/ember-fleet" && mkdir -p "$HOME/.config/ember-fleet/hub/dist"'
for f in "$here"/dist/ember-fleet-linux-* "$here"/dist/ember-fleet-darwin-*; do
  [ -f "$f" ] && scp -q "$f" "$target:.config/ember-fleet/hub/dist/"
done

if [ -n "$public" ]; then
  echo "Starting the hub on $target with HTTPS on :443 (sudo) ..."
  extra="--public $public"
  [ -n "$email" ] && extra="$extra --email $email"
  ssh -t "$target" "\$HOME/.local/bin/ember-fleet service install hub --system -- $extra"
  shown="https://$public"
else
  echo "Starting the hub on $target, listening on $listen ..."
  extra="--listen $listen"
  [ -n "$url" ] && extra="$extra --url $url"
  ssh -o BatchMode=yes "$target" "XDG_RUNTIME_DIR=\${XDG_RUNTIME_DIR:-/run/user/\$(id -u)} \$HOME/.local/bin/ember-fleet service install hub -- $extra"
  shown="${url:-http://$listen (on $target; reach it through your proxy or an SSH tunnel)}"
fi

mv "$hubdir" "$hubdir.moved-$(date +%Y%m%d-%H%M%S)"
rm -f "$root/cli/cli.json"

cat <<EOF

The hub now runs on $target: $shown

Sign in:              ssh $target .local/bin/ember-fleet login-link
Terminal tools here:  ssh $target .local/bin/ember-fleet token laptop
                      ember-fleet login <the hub's https address>   (paste the token)
EOF
if [ -z "$public" ]; then
  cat <<EOF

Behind a proxy with its own sign-in (Pangolin SSO and the like), let these
through without it, so new machines can install and the terminal tools work
(the hub checks its own sign-in, tokens and join links on all of them):
  /install.sh   /dl/*        new machines download ember-fleet
  /api/*                     only if you use ember-fleet attach/term from other computers
EOF
fi
