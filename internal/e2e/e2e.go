// Package e2e runs TLS 1.3 between hub and dock inside a relay channel, each
// side pinning the other's key, and defines the first message of a channel.
package e2e

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
)

// Version of the channel protocol.
const Version = 1

// Channel kinds.
const (
	KindPair     = "pair"
	KindWatch    = "watch"
	KindObserve  = "observe"
	KindControl  = "control"
	KindAction   = "action"
	KindEndpoint = "endpoint"
	KindUpload   = "upload"
	KindUpdate   = "update"       // a new ember-fleet binary for the dock
	KindHerdrUp  = "herdr-update" // run `herdr update` on the dock's machine
)

// Header is the first line the hub sends inside a channel. The dock decides
// what to allow from this, which the relay can neither read nor change.
type Header struct {
	V        int             `json:"v"`
	Kind     string          `json:"kind"`
	Operator string          `json:"operator,omitempty"` // who is acting, for the audit log
	Session  string          `json:"session,omitempty"`
	Pane     string          `json:"pane,omitempty"`
	Terminal string          `json:"terminal,omitempty"` // the pane's terminal ID, as last seen
	Cols     int             `json:"cols,omitempty"`
	Rows     int             `json:"rows,omitempty"`
	Takeover bool            `json:"takeover,omitempty"`
	Epoch    string          `json:"epoch,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Name     string          `json:"name,omitempty"`    // upload: the file's name
	Size     int64           `json:"size,omitempty"`    // upload: exact byte count that follows
	Version  string          `json:"version,omitempty"` // update: the version being installed
	SHA256   string          `json:"sha256,omitempty"`  // update: hex digest of the bytes that follow
	Handoff  bool            `json:"handoff,omitempty"` // herdr-update: live handoff of running servers
}

// Reply is the dock's first line back.
type Reply struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
	// pair
	LinkID string `json:"link_id,omitempty"`
	Proof  []byte `json:"proof,omitempty"`
	// upload
	Path string `json:"path,omitempty"`
	// action
	Result  json.RawMessage `json:"result,omitempty"`
	Outcome string          `json:"outcome,omitempty"` // "not_sent" or "unknown" when OK is false
}

// HubTLS is the hub's side: it accepts only dock keys allowed by allow.
func HubTLS(id *keys.Identity, allow func(fp string) bool) *tls.Config {
	return &tls.Config{
		Certificates:          []tls.Certificate{id.Cert},
		InsecureSkipVerify:    true, // replaced by the pin check
		VerifyPeerCertificate: keys.VerifyPinned(allow),
		MinVersion:            tls.VersionTLS13,
		ServerName:            "ember-dock",
	}
}

// DockTLS is the dock's side: it accepts only hub keys allowed by allow.
func DockTLS(id *keys.Identity, allow func(fp string) bool) *tls.Config {
	return &tls.Config{
		Certificates:          []tls.Certificate{id.Cert},
		ClientAuth:            tls.RequireAnyClientCert,
		VerifyPeerCertificate: keys.VerifyPinned(allow),
		MinVersion:            tls.VersionTLS13,
	}
}

// Client runs the hub's TLS handshake over conn.
func Client(ctx context.Context, conn net.Conn, cfg *tls.Config) (*tls.Conn, string, error) {
	t := tls.Client(conn, cfg)
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := t.HandshakeContext(hctx); err != nil {
		conn.Close()
		return nil, "", err
	}
	fp, err := keys.PeerFingerprint(t.ConnectionState())
	if err != nil {
		t.Close()
		return nil, "", err
	}
	return t, fp, nil
}

// Server runs the dock's TLS handshake over conn.
func Server(ctx context.Context, conn net.Conn, cfg *tls.Config) (*tls.Conn, string, error) {
	t := tls.Server(conn, cfg)
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := t.HandshakeContext(hctx); err != nil {
		conn.Close()
		return nil, "", err
	}
	fp, err := keys.PeerFingerprint(t.ConnectionState())
	if err != nil {
		t.Close()
		return nil, "", err
	}
	return t, fp, nil
}

// MaxLine bounds one JSON line (a full terminal frame can be large).
const MaxLine = 16 << 20

// WriteJSON writes v as one line.
func WriteJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ReadLine reads one line of at most MaxLine bytes, without the newline.
func ReadLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := r.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > MaxLine {
			return nil, errors.New("line too long")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		return out[:len(out)-1], nil
	}
}

// ReadJSON reads one line into v.
func ReadJSON(r *bufio.Reader, v any) error {
	line, err := ReadLine(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}
