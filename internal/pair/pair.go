// Package pair handles join links and the keys derived from them.
//
// A join link carries the relay's address and certificate pin, the hub's key
// fingerprint, a 32-byte secret and the machine name. From the secret:
//
//	ticket  = HMAC(secret, "ember-ticket-v1")   shown to the relay once, to enroll
//	pairkey = HMAC(secret, "ember-pair-v1")     never leaves the dock and the hub
//	linkID  = HMAC(secret, "ember-link-id-v1")  first 12 bytes, names the link
//
// The dock proves its key to the hub with Proof(pairkey, dockFP, hubFP, linkID)
// inside a TLS channel whose peer key is dockFP, and trusts only the hub key
// whose fingerprint is in the link. A relay that swaps either key cannot make a
// valid proof, because it never learns the secret.
package pair

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
)

const (
	JoinScheme  = "ember-join://"
	RelayScheme = "ember-relay://"
	version     = "v1"
)

// Join is a decoded join link.
type Join struct {
	Relay  string // host:port
	Pin    string // relay certificate fingerprint
	HubFP  string // hub key fingerprint
	Secret []byte // 32 bytes
	Name   string // machine name
}

// RelayLink lets a hub enroll with a relay.
type RelayLink struct {
	Relay  string
	Pin    string
	Ticket []byte
}

// NewSecret returns 32 random bytes.
func NewSecret() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func derive(secret []byte, label string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// Ticket is what the dock shows the relay to enroll.
func Ticket(secret []byte) []byte { return derive(secret, "ember-ticket-v1") }

// PairKey is the dock's proof key; the relay never sees it.
func PairKey(secret []byte) []byte { return derive(secret, "ember-pair-v1") }

// LinkID names a link without revealing its secret.
func LinkID(secret []byte) string {
	return base64.RawURLEncoding.EncodeToString(derive(secret, "ember-link-id-v1")[:12])
}

// TicketHash is what the relay stores.
func TicketHash(ticket []byte) []byte {
	sum := sha256.Sum256(ticket)
	return sum[:]
}

// Proof binds the dock's key, the hub's key and the link together.
func Proof(pairKey []byte, dockFP, hubFP, linkID string) ([]byte, error) {
	d, err := keys.FingerprintBytes(dockFP)
	if err != nil {
		return nil, err
	}
	h, err := keys.FingerprintBytes(hubFP)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, pairKey)
	m.Write([]byte("ember-pair-proof-v1\x00"))
	m.Write(d)
	m.Write(h)
	m.Write([]byte(linkID))
	return m.Sum(nil), nil
}

// CheckProof verifies a proof in constant time.
func CheckProof(pairKey []byte, dockFP, hubFP, linkID string, proof []byte) bool {
	want, err := Proof(pairKey, dockFP, hubFP, linkID)
	if err != nil {
		return false
	}
	return hmac.Equal(want, proof)
}

// String encodes a join link.
func (j Join) String() string {
	pin, _ := keys.FingerprintBytes(j.Pin)
	hub, _ := keys.FingerprintBytes(j.HubFP)
	payload := append(append(append(append([]byte{}, pin...), hub...), j.Secret...), []byte(j.Name)...)
	return JoinScheme + j.Relay + "/#" + version + "." + base64.RawURLEncoding.EncodeToString(payload)
}

// ParseJoin decodes a join link.
func ParseJoin(s string) (Join, error) {
	var j Join
	addr, payload, err := split(strings.TrimSpace(s), JoinScheme)
	if err != nil {
		return j, err
	}
	if len(payload) < 96 {
		return j, errors.New("join link is too short; copy the whole link")
	}
	j.Relay = addr
	j.Pin = keys.FingerprintFromBytes(payload[:32])
	j.HubFP = keys.FingerprintFromBytes(payload[32:64])
	j.Secret = append([]byte{}, payload[64:96]...)
	j.Name = string(payload[96:])
	if err := CheckName(j.Name); err != nil {
		return j, err
	}
	return j, nil
}

// String encodes a relay link.
func (r RelayLink) String() string {
	pin, _ := keys.FingerprintBytes(r.Pin)
	payload := append(append([]byte{}, pin...), r.Ticket...)
	return RelayScheme + r.Relay + "/#" + version + "." + base64.RawURLEncoding.EncodeToString(payload)
}

// ParseRelay decodes a relay link.
func ParseRelay(s string) (RelayLink, error) {
	var r RelayLink
	addr, payload, err := split(strings.TrimSpace(s), RelayScheme)
	if err != nil {
		return r, err
	}
	if len(payload) != 64 {
		return r, errors.New("relay link has the wrong length; copy the whole link")
	}
	r.Relay = addr
	r.Pin = keys.FingerprintFromBytes(payload[:32])
	r.Ticket = append([]byte{}, payload[32:]...)
	return r, nil
}

func split(s, scheme string) (string, []byte, error) {
	if !strings.HasPrefix(s, scheme) {
		return "", nil, fmt.Errorf("not an %s link", strings.TrimSuffix(scheme, "://"))
	}
	rest := strings.TrimPrefix(s, scheme)
	addr, frag, ok := strings.Cut(rest, "/#")
	if !ok {
		return "", nil, errors.New("link is missing its #v1. part; copy the whole link")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", nil, fmt.Errorf("bad relay address %q", addr)
	}
	v, enc, ok := strings.Cut(frag, ".")
	if !ok || v != version {
		return "", nil, errors.New("unsupported link version")
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", nil, errors.New("link is damaged; copy it again")
	}
	return addr, payload, nil
}

// CheckName accepts short printable names.
func CheckName(name string) error {
	if name == "" || len(name) > 64 || !utf8.ValidString(name) {
		return errors.New("name must be 1-64 characters")
	}
	if strings.HasPrefix(name, "-") || strings.TrimSpace(name) != name {
		return errors.New("name must not start with '-' or a space")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '/' {
			return errors.New("name has a character that is not allowed")
		}
	}
	return nil
}
