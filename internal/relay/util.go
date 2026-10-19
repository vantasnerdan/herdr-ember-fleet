package relay

import (
	"crypto/x509"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/keys"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/pair"
)

type x509Cert = x509.Certificate

// checkSelfSigned accepts any well-formed self-signed Ed25519 client
// certificate; which keys may do what is decided per call, by fingerprint.
func checkSelfSigned(raw [][]byte) error {
	return keys.VerifyPinned(func(string) bool { return true })(raw, nil)
}

func ticketHash(t []byte) []byte { return pair.TicketHash(t) }
