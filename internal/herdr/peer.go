package herdr

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// Peer identifies a herdr server process: PID plus start time, so a restarted
// server (even one that reuses the PID) is a different peer.
type Peer struct {
	PID   int
	UID   int
	Start int64
}

// Epoch is a short ID for one server process.
func (p Peer) Epoch() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d", p.PID, p.Start)))
	return base64.RawURLEncoding.EncodeToString(sum[:9])
}
