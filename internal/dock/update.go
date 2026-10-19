package dock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/herdr"
)

const maxBinary = 200 << 20

// selfUpdate replaces this dock's binary with one the hub sends, then exits so
// the service manager starts the new version. The bytes are checked against
// their size and SHA-256, and the new binary must run and report the expected
// version before it replaces the old one.
func (d *Dock) selfUpdate(ctx context.Context, c *chanCtx) {
	h := c.h
	if !d.cfg.acceptsUpdates() {
		c.fail("refused", "this machine updates by hand (accept_updates is false in dock.json)")
		return
	}
	if h.Size <= 0 || h.Size > maxBinary || len(h.SHA256) != 64 || h.Version == "" || len(h.Version) > 40 {
		c.fail("bad_update", "bad update header")
		return
	}
	if h.Version == Version {
		c.fail("current", "already on "+Version)
		return
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		c.fail("io", "cannot find this binary: "+err.Error())
		return
	}
	tmp := filepath.Join(filepath.Dir(exe), ".ember-fleet.update")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		c.fail("io", "cannot write next to "+exe+": "+err.Error())
		return
	}
	if c.ok() != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(c.r, h.Size))
	cerr := f.Close()
	fail := func(msg string) {
		os.Remove(tmp)
		d.audit.log(c, "update", "failed: "+msg)
		e2e.WriteJSON(c.conn, e2e.Reply{Code: "update_failed", Error: msg})
	}
	switch {
	case err != nil || cerr != nil || n != h.Size:
		fail("the update was cut short")
		return
	case hex.EncodeToString(sum.Sum(nil)) != strings.ToLower(h.SHA256):
		fail("the update's checksum does not match")
		return
	}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	out, err := exec.CommandContext(vctx, tmp, "version").Output()
	cancel()
	if err != nil || strings.TrimSpace(string(out)) != "ember-fleet "+h.Version {
		fail(fmt.Sprintf("the new binary did not report version %s (%q)", h.Version, strings.TrimSpace(string(out))))
		return
	}
	if err := os.Rename(tmp, exe); err != nil {
		fail("cannot replace the binary: " + err.Error())
		return
	}
	d.audit.log(c, "update", Version+" -> "+h.Version)
	e2e.WriteJSON(c.conn, e2e.Reply{OK: true, Path: exe})
	log.Printf("updated %s -> %s; restarting", Version, h.Version)
	go func() {
		time.Sleep(1500 * time.Millisecond) // let the reply reach the hub
		os.Exit(0)                          // systemd/launchd starts the new binary
	}()
}

// HerdrUpdateResult is what a herdr update did.
type HerdrUpdateResult struct {
	Before  string `json:"before"`
	After   string `json:"after"`
	Servers string `json:"servers"` // versions of the running servers afterwards
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// herdrUpdate runs `herdr update` (with --handoff if asked) and reports the
// installed and running versions afterwards. Without handoff, running
// servers keep their version until they restart; with it, herdr moves them
// to the new version while keeping pane processes alive (best effort).
func (d *Dock) herdrUpdate(ctx context.Context, c *chanCtx) {
	if !d.cfg.acceptsUpdates() {
		c.fail("refused", "this machine updates by hand (accept_updates is false in dock.json)")
		return
	}
	bin, err := herdr.Binary()
	if err != nil {
		c.fail("no_herdr", err.Error())
		return
	}
	res := HerdrUpdateResult{Before: herdr.Version(ctx)}
	args := []string{"update"}
	if c.h.Handoff {
		args = append(args, "--handoff")
	}
	uctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(uctx, bin, args...)
	cmd.Env = herdr.CleanEnv()
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	cmd.Stdin = nil // any question gets herdr's default answer (No)
	var out bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: 16 << 10}
	cmd.Stderr = cmd.Stdout
	d.audit.log(c, "herdr-update", strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		res.Error = err.Error()
	}
	res.Output = tail(out.String(), 2000)
	res.After = herdr.Version(ctx)
	herdrVersionCache.Store(nil) // the next info message reports the new version
	// Give a handed-off server a moment, then report what is actually running.
	time.Sleep(3 * time.Second)
	for _, s := range herdr.Sessions() {
		v := serverVersion(ctx, s)
		// `herdr update` hands off only when it installs something. A server
		// left on an older version than the installed herdr (installed earlier
		// by hand, say) is handed off to the installed binary here, the same
		// way herdr's updater does it. On any failure herdr rolls back and
		// the old server keeps running with its panes.
		if c.h.Handoff && res.After != "" && v != "" && v != res.After {
			if err := handoff(ctx, s, bin, res.After); err != nil {
				res.Output = tail(res.Output+"\nhandoff of "+s+" to "+res.After+": "+err.Error(), 2000)
			}
			v = serverVersion(ctx, s)
		}
		res.Servers += s + "=" + v + " "
		d.sessions.Poke(s)
	}
	res.Servers = strings.TrimSpace(res.Servers)
	b, _ := json.Marshal(res)
	e2e.WriteJSON(c.conn, e2e.Reply{OK: res.Error == "", Error: res.Error, Result: b})
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func serverVersion(ctx context.Context, session string) string {
	snap, _, err := readSnapshot(ctx, session)
	if err != nil {
		return ""
	}
	var v struct {
		Version string `json:"version"`
	}
	json.Unmarshal(snap, &v)
	return v.Version
}

// handoff asks a running server to hand its panes to the installed herdr,
// then waits until the server answering on the socket reports that version.
func handoff(ctx context.Context, session, bin, version string) error {
	hctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, _, err := herdr.Call(hctx, session, "server.live_handoff", map[string]any{"import_exe": bin, "expected_version": version})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		if serverVersion(hctx, session) == version {
			return nil
		}
	}
	return fmt.Errorf("the server did not come back on %s within 60 s", version)
}
