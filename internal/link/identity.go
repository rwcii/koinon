// Package link holds the parts of the daemon link between two of the maintainer's computers
// that need no daemon (#670): the link key and certificate, the advertised name, the
// invitation, the private link state and the frames on the TLS stream. The daemon in
// internal/core connects them to Remote, the store and the dashboard.
package link

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// KeyFile is the private file of the link key and its self-signed certificate.
const KeyFile = "link-key.pem"

// certificateLife is the validity of a new link certificate. Peers check the pin, never the
// validity dates; a certificate within renewWindow of its end is renewed with the same key,
// so the pin does not change and the maintainer does no work for expiry.
const (
	certificateLife = 2 * 365 * 24 * time.Hour
	renewWindow     = 30 * 24 * time.Hour
)

// Identity is this daemon's link key, its certificate and its pin.
type Identity struct {
	Certificate tls.Certificate
	Pin         string
}

// Pin is the lower-case hexadecimal SHA-256 of a certificate's SubjectPublicKeyInfo.
func Pin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// ValidPin reports whether pin has the form of Pin.
func ValidPin(pin string) bool {
	if len(pin) != 64 {
		return false
	}
	for _, c := range pin {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// LoadIdentity reads the link key of the state directory root, creates it when it is missing
// and renews its certificate with the same key near its end. now is the current time.
func LoadIdentity(root string, now time.Time) (Identity, error) {
	path := filepath.Join(root, KeyFile)
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	var key ed25519.PrivateKey
	var cert *x509.Certificate
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, 16385))
		f.Close()
		if readErr != nil || len(data) > 16384 {
			return Identity{}, errors.New("link_key_invalid: inspect link-key.pem in the private state directory")
		}
		key, cert, err = parseKeyFile(data)
		if err != nil {
			return Identity{}, fmt.Errorf("link_key_invalid: inspect link-key.pem in the private state directory: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, fmt.Errorf("link_key_invalid: %w", err)
	} else {
		if _, key, err = ed25519.GenerateKey(rand.Reader); err != nil {
			return Identity{}, err
		}
	}
	if cert == nil || now.Add(renewWindow).After(cert.NotAfter) {
		if cert, err = newCertificate(key, now); err != nil {
			return Identity{}, err
		}
		if err := writeKeyFile(path, key, cert); err != nil {
			return Identity{}, err
		}
	}
	return Identity{Certificate: tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}, Pin: Pin(cert)}, nil
}

func newCertificate(key ed25519.PrivateKey, now time.Time) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Koinon link"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(certificateLife),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func parseKeyFile(data []byte) (ed25519.PrivateKey, *x509.Certificate, error) {
	var key ed25519.PrivateKey
	var cert *x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		switch block.Type {
		case "PRIVATE KEY":
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			k, ok := parsed.(ed25519.PrivateKey)
			if err != nil || !ok || key != nil {
				return nil, nil, errors.New("not one Ed25519 private key")
			}
			key = k
		case "CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil || cert != nil {
				return nil, nil, errors.New("not one certificate")
			}
			cert = c
		default:
			return nil, nil, errors.New("unexpected block")
		}
	}
	if key == nil {
		return nil, nil, errors.New("no private key")
	}
	if cert != nil {
		public, ok := cert.PublicKey.(ed25519.PublicKey)
		if !ok || !public.Equal(key.Public()) {
			return nil, nil, errors.New("the certificate is not for the key")
		}
	}
	return key, cert, nil
}

func writeKeyFile(path string, key ed25519.PrivateKey, cert *x509.Certificate) error {
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	// Never replace an unsafe inode.
	if f, err := platform.OpenPrivate(path, os.O_RDONLY); err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})...)
	return platform.WriteAtomic(path, data, 0600)
}
