// Package herdr talks to the local herdr servers of the current OS user: the
// socket API (newline-delimited JSON, one request per connection), session
// discovery, and the installed herdr binary for terminal sessions and the
// client bridge.
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ConfigDir is herdr's config directory for this user.
func ConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "herdr")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr")
}

var sessionName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidSession reports whether name is a safe session name.
func ValidSession(name string) bool {
	return sessionName.MatchString(name) && name != "." && name != ".."
}

// SocketPath is the API socket for a session ("default" is the unnamed one).
func SocketPath(session string) string {
	if session == "default" {
		return filepath.Join(ConfigDir(), "herdr.sock")
	}
	return filepath.Join(ConfigDir(), "sessions", session, "herdr.sock")
}

// Sessions lists sessions that have an API socket file.
func Sessions() []string {
	out := []string{}
	if isSocket(SocketPath("default")) {
		out = append(out, "default")
	}
	entries, _ := os.ReadDir(filepath.Join(ConfigDir(), "sessions"))
	for _, e := range entries {
		if e.IsDir() && ValidSession(e.Name()) && e.Name() != "default" && isSocket(SocketPath(e.Name())) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out[min(1, len(out)):])
	return out
}

func isSocket(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

// Binary finds the installed herdr. Services start with a short PATH, so
// common install locations are checked too.
func Binary() (string, error) {
	if p, err := exec.LookPath("herdr"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "herdr"),
		"/opt/homebrew/bin/herdr",
		"/usr/local/bin/herdr",
		filepath.Join(home, ".cargo", "bin", "herdr"),
		filepath.Join(home, ".local", "share", "mise", "shims", "herdr"),
	} {
		if st, err := os.Stat(p); err == nil && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("herdr is not installed (see https://herdr.dev)")
}

// CleanEnv is the environment for herdr processes we start: ours, minus any
// HERDR_* variables (we may ourselves run inside a herdr pane).
func CleanEnv(extra ...string) []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HERDR_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// Command builds `herdr --session S args...` with a clean environment.
func Command(ctx context.Context, session string, args ...string) (*exec.Cmd, error) {
	bin, err := Binary()
	if err != nil {
		return nil, err
	}
	if !ValidSession(session) {
		return nil, fmt.Errorf("bad session name %q", session)
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"--session", session}, args...)...)
	cmd.Env = CleanEnv()
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	return cmd, nil
}

// Error is an error response from herdr.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

var reqSeq atomic.Uint64

// Conn is one API connection, with the identity of the server on the other end.
type Conn struct {
	c    *net.UnixConn
	r    *bufio.Reader
	Peer Peer
}

// Dial connects to a session's socket and identifies the server process.
func Dial(ctx context.Context, session string) (*Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", SocketPath(session))
	if err != nil {
		return nil, err
	}
	uc := c.(*net.UnixConn)
	peer, err := PeerOf(uc)
	if err != nil {
		uc.Close()
		return nil, fmt.Errorf("cannot identify the herdr server: %w", err)
	}
	if peer.UID != os.Getuid() {
		uc.Close()
		return nil, errors.New("herdr socket belongs to another user")
	}
	return &Conn{c: uc, r: bufio.NewReaderSize(uc, 64<<10), Peer: peer}, nil
}

// Close closes the connection.
func (c *Conn) Close() error { return c.c.Close() }

// Request sends one request. Herdr answers one request per connection.
func (c *Conn) Request(method string, params any) (string, error) {
	if params == nil {
		params = map[string]any{}
	}
	id := fmt.Sprintf("ef_%d", reqSeq.Add(1))
	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	c.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = c.c.Write(b)
	return id, err
}

// Read reads one response or event line.
func (c *Conn) Read(timeout time.Duration) (json.RawMessage, *Error, error) {
	if timeout > 0 {
		c.c.SetReadDeadline(time.Now().Add(timeout))
	} else {
		c.c.SetReadDeadline(time.Time{})
	}
	var out []byte
	for {
		chunk, err := c.r.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > 64<<20 {
			return nil, nil, errors.New("herdr response too large")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		break
	}
	var r response
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, nil, fmt.Errorf("bad herdr response: %w", err)
	}
	if r.Error != nil {
		return nil, r.Error, nil
	}
	if r.Result == nil {
		return out, nil, nil // an event line
	}
	return r.Result, nil, nil
}

// Call makes one request on a fresh connection.
func Call(ctx context.Context, session, method string, params any) (json.RawMessage, Peer, error) {
	c, err := Dial(ctx, session)
	if err != nil {
		return nil, Peer{}, err
	}
	defer c.Close()
	if _, err := c.Request(method, params); err != nil {
		return nil, c.Peer, err
	}
	res, herr, err := c.Read(30 * time.Second)
	if err != nil {
		return nil, c.Peer, err
	}
	if herr != nil {
		return nil, c.Peer, herr
	}
	return res, c.Peer, nil
}

// Version of the installed herdr binary ("0.9.5").
func Version(ctx context.Context) string {
	bin, err := Binary()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = CleanEnv()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) >= 2 {
		return f[1]
	}
	return strings.TrimSpace(string(out))
}
