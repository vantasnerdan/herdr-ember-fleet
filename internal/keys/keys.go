// Package keys holds a member's identity: an Ed25519 key and a self-signed
// certificate for it. A member is known everywhere by its fingerprint, the
// SHA-256 of the certificate's public key (SPKI), never by its address.
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Identity is a key pair with its self-signed certificate.
type Identity struct {
	Cert        tls.Certificate
	Fingerprint string
}

// Fingerprint of a certificate: base64url (no padding) of SHA-256 over its SPKI.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// FingerprintBytes decodes a fingerprint to its 32 raw bytes.
func FingerprintBytes(fp string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(fp)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("bad fingerprint %q", fp)
	}
	return b, nil
}

// FingerprintFromBytes encodes 32 raw bytes as a fingerprint.
func FingerprintFromBytes(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// Equal compares two fingerprints in constant time.
func Equal(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Short is a fingerprint shortened for display.
func Short(fp string) string {
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

// LoadOrCreate reads key.pem and cert.pem from dir, creating them if missing.
func LoadOrCreate(dir, commonName string) (*Identity, error) {
	keyPath, certPath := filepath.Join(dir, "key.pem"), filepath.Join(dir, "cert.pem")
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := create(keyPath, certPath, commonName); err != nil {
			return nil, err
		}
	}
	return Load(dir)
}

// Load reads an existing identity from dir.
func Load(dir string) (*Identity, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, fmt.Errorf("load key from %s: %w", dir, err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	cert.Leaf = leaf
	return &Identity{Cert: cert, Fingerprint: Fingerprint(leaf)}, nil
}

func create(keyPath, certPath, commonName string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(30, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{commonName},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return err
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), 0o600); err != nil {
		return err
	}
	return WriteFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// PeerFingerprint is the fingerprint of the first certificate a TLS peer sent.
func PeerFingerprint(state tls.ConnectionState) (string, error) {
	if len(state.PeerCertificates) == 0 {
		return "", errors.New("peer sent no certificate")
	}
	return Fingerprint(state.PeerCertificates[0]), nil
}

// VerifyPinned returns a VerifyPeerCertificate func that accepts exactly the
// raw certificate whose key matches one of the allowed fingerprints.
func VerifyPinned(allowed func(fp string) bool) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return errors.New("no certificate")
		}
		cert, err := x509.ParseCertificate(raw[0])
		if err != nil {
			return err
		}
		if _, ok := cert.PublicKey.(ed25519.PublicKey); !ok {
			return errors.New("only Ed25519 keys are accepted")
		}
		now := time.Now()
		if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
			return errors.New("certificate outside its validity period")
		}
		// The certificate is a leaf, not a CA, so check its self-signature
		// directly rather than with CheckSignatureFrom (which demands a CA).
		if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
			return fmt.Errorf("certificate is not self-signed by its key: %w", err)
		}
		if !allowed(Fingerprint(cert)) {
			return fmt.Errorf("key %s is not the pinned one", Short(Fingerprint(cert)))
		}
		return nil
	}
}

// WriteFileAtomic writes data to path via a synced temp file and rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
