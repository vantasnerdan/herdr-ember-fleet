#!/bin/sh
# Move the hub (the web page and its records) from this computer to a server,
# served over HTTPS with a Let's Encrypt certificate.
#
#   scripts/move-hub.sh USER@HOST DOMAIN [EMAIL]
#
#   USER@HOST  the server, reachable over SSH now, with sudo (to bind :443)
#   DOMAIN     its DNS name; ports 80 and 443 must reach it from the internet
#   EMAIL      optional contact address for Let's Encrypt
#
# The hub's key and records move with it, so no machine has to pair again.
# The local copy is kept (stopped) in ~/.config/ember-fleet/hub.moved-DATE.
set -eu

if [ $# -lt 2 ]; then
  sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi
target="$1"; domain="$2"; email="${3:-}"
here="$(cd "$(dirname "$0")/.." && pwd)"
hubdir="${EMBER_FLEET_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/ember-fleet}/hub"

[ -f "$hubdir/hub.json" ] || { echo "No hub on this computer ($hubdir)" >&2; exit 1; }

echo "Checking $target ..."
uname_sm="$(ssh -o BatchMode=yes "$target" 'uname -sm')"
case "$uname_sm" in
  "Linux x86_64") plat=linux-amd64 ;;
  "Linux aarch64"|"Linux arm64") plat=linux-arm64 ;;
  *) echo "The public hub needs Linux (systemd); $target is $uname_sm" >&2; exit 1 ;;
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

echo "Installing ember-fleet and the downloads it serves ..."
scp -q "$here/dist/ember-fleet-$plat" "$target:.local/bin/ember-fleet.new"
ssh -o BatchMode=yes "$target" 'chmod 755 "$HOME/.local/bin/ember-fleet.new" && mv "$HOME/.local/bin/ember-fleet.new" "$HOME/.local/bin/ember-fleet" && mkdir -p "$HOME/.config/ember-fleet/hub/dist"'
scp -q "$here"/dist/ember-fleet-* "$target:.config/ember-fleet/hub/dist/"

echo "Starting the hub on $target (sudo, for ports 80 and 443) ..."
extra="--public $domain"
[ -n "$email" ] && extra="$extra --email $email"
ssh -t "$target" "\$HOME/.local/bin/ember-fleet service install hub --system -- $extra"

mv "$hubdir" "$hubdir.moved-$(date +%Y%m%d-%H%M%S)"
rm -f "${EMBER_FLEET_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/ember-fleet}/cli/cli.json"

cat <<EOF

The hub now runs at https://$domain (the first visit may take a few seconds
while the certificate is issued).

Sign in:            ssh $target ember-fleet login-link
Terminal tools here: ssh $target ember-fleet token laptop
                     ember-fleet login https://$domain     (paste the token)
EOF
