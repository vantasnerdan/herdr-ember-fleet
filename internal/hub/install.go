package hub

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var dlName = regexp.MustCompile(`^ember-fleet-(linux|darwin)-(amd64|arm64)$`)

// download serves release binaries from <hub dir>/dist, or this very binary
// when it matches the asked-for platform.
func (w *Web) download(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !dlName.MatchString(name) {
		http.NotFound(rw, r)
		return
	}
	p := filepath.Join(Dir(), "dist", name)
	if _, err := os.Stat(p); err != nil {
		if name == "ember-fleet-"+runtime.GOOS+"-"+runtime.GOARCH {
			if exe, err := os.Executable(); err == nil {
				p = exe
			}
		}
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(rw, "no build for that platform on this hub; run scripts/build.sh and copy dist/ to "+filepath.Join(Dir(), "dist"), http.StatusNotFound)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(rw, r, name, st.ModTime(), f)
}

// installScript is what `curl -fsSL <hub>/install.sh | sh` runs on a new
// machine: install the binary for this user, ask for the join link (on the
// terminal, so it stays out of shell history and process lists), join, and
// start the dock as a user service.
func (w *Web) installScript(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Write([]byte(strings.ReplaceAll(installSh, "@HUB@", w.Base())))
}

const installSh = `#!/bin/sh
# Herdr Ember Fleet: add this computer (this OS user) to a fleet.
set -eu
HUB="@HUB@"
if [ "$(id -u)" = "0" ]; then
  echo "Run this as the user whose herdr agents should join, not as root." >&2
  exit 1
fi
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "Unsupported system: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac
bin="$HOME/.local/bin"
mkdir -p "$bin"
echo "Downloading ember-fleet for $os/$arch from $HUB ..."
tmp="$bin/.ember-fleet.$$"
curl -fsSL "$HUB/dl/ember-fleet-$os-$arch" -o "$tmp"
chmod 755 "$tmp"
mv "$tmp" "$bin/ember-fleet"
if ! command -v herdr >/dev/null 2>&1 && [ ! -x "$HOME/.local/bin/herdr" ] && [ ! -x /opt/homebrew/bin/herdr ] && [ ! -x /usr/local/bin/herdr ]; then
  echo "Note: herdr is not installed yet. Install it with: curl -fsSL https://herdr.dev/install.sh | sh"
fi
if [ -r /dev/tty ]; then
  "$bin/ember-fleet" join < /dev/tty
else
  echo "Now run: $bin/ember-fleet join   (it asks for the link)"
fi
`
