// Package tlsid is the TLS identity of the pak (M13, docs/specs/03-protocole.md
// §1): an ECDSA P-256 key and a self-signed certificate, created on the
// first launch, kept on the SD card and reused at every launch. The app
// pins the certificate's public key (the "k" of the pairing link).
package tlsid

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/store"
)

// File names in the TLS folder.
const (
	CertFile = "cert.pem"
	KeyFile  = "key.pem"
)

// Validity is how long a new certificate is valid. The app pins the key,
// not the dates, but TLS stacks may still refuse an expired certificate.
const Validity = 20 * 365 * 24 * time.Hour

// Identity is the loaded certificate.
type Identity struct {
	Certificate tls.Certificate
	// Pin is b64u(SHA-256(SPKI DER)) of the certificate.
	Pin string
	// Created reports that a new key (a new pin) was created by this call.
	Created bool
}

// Options are the inputs of LoadOrCreate.
type Options struct {
	Dir      string // the TLS folder (store.Layout.TLSDir)
	DeviceID string // the common name of a new certificate
	Now      func() time.Time
	Rand     io.Reader // nil: crypto/rand
	Logger   *slog.Logger
}

// LoadOrCreate returns the identity kept in Dir. The private key is the
// identity (the pin is the hash of its public key):
//   - key and certificate readable and matching: they are used as is;
//   - key readable, certificate missing, damaged or not matching: a new
//     certificate is made for the same key, so the pin does not change;
//   - key missing or damaged: a new key and certificate (a new pin: the
//     apps must pair again, which is better than a pak that cannot start).
//
// A damaged file is moved aside with the suffix ".bad", never deleted.
func LoadOrCreate(o Options) (Identity, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Rand == nil {
		o.Rand = rand.Reader
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	certPath, keyPath := filepath.Join(o.Dir, CertFile), filepath.Join(o.Dir, KeyFile)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return Identity{}, fmt.Errorf("read key: %w", keyErr)
	}
	certPEM, certErr := os.ReadFile(certPath)
	if certErr != nil && !errors.Is(certErr, os.ErrNotExist) {
		return Identity{}, fmt.Errorf("read certificate: %w", certErr)
	}
	var key *ecdsa.PrivateKey
	if keyErr == nil {
		var err error
		if key, err = parseKey(keyPEM); err != nil {
			o.Logger.Warn("TLS key unreadable, creating a new identity (apps must pair again)", "error", err)
			if err := moveAside(keyPath); err != nil {
				return Identity{}, err
			}
			key = nil
		} else if certErr == nil {
			id, err := parse(certPEM, keyPEM)
			if err == nil {
				return id, nil
			}
			o.Logger.Warn("TLS certificate unreadable, making a new one for the same key (same pin)", "error", err)
		}
	}
	if certErr == nil {
		if err := moveAside(certPath); err != nil {
			return Identity{}, err
		}
	}
	created := key == nil
	if created {
		var err error
		if key, err = ecdsa.GenerateKey(elliptic.P256(), o.Rand); err != nil {
			return Identity{}, fmt.Errorf("generate key: %w", err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return Identity{}, fmt.Errorf("encode key: %w", err)
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		// The key first: a crash before the certificate is written leaves
		// a key without certificate, completed at the next launch.
		if err := store.WriteFileAtomic(keyPath, keyPEM, 0o600); err != nil {
			return Identity{}, fmt.Errorf("save key: %w", err)
		}
	}
	certPEM, err := certificateFor(key, o.DeviceID, o.Now(), o.Rand)
	if err != nil {
		return Identity{}, err
	}
	if err := store.WriteFileAtomic(certPath, certPEM, 0o644); err != nil {
		return Identity{}, fmt.Errorf("save certificate: %w", err)
	}
	id, err := parse(certPEM, keyPEM)
	if err != nil {
		return Identity{}, err
	}
	id.Created = created
	if created {
		o.Logger.Info("TLS identity created", "pin", id.Pin)
	} else {
		o.Logger.Info("TLS certificate made again for the same key", "pin", id.Pin)
	}
	return id, nil
}

func moveAside(p string) error {
	if err := os.Rename(p, p+".bad"); err != nil {
		return fmt.Errorf("move aside %s: %w", filepath.Base(p), err)
	}
	return nil
}

// parseKey reads a PKCS#8 ECDSA P-256 private key in PEM.
func parseKey(keyPEM []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("not an ECDSA P-256 key")
	}
	return key, nil
}

// Generate returns a new self-signed certificate (CN = deviceID, valid
// Validity from now) and its ECDSA P-256 key, in PEM.
func Generate(deviceID string, now time.Time, random io.Reader) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}
	if certPEM, err = certificateFor(key, deviceID, now, random); err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("encode key: %w", err)
	}
	return certPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// certificateFor returns a self-signed certificate of key (CN = deviceID,
// valid Validity from a day before now), in PEM.
func certificateFor(key *ecdsa.PrivateKey, deviceID string, now time.Time, random io.Reader) ([]byte, error) {
	serial, err := rand.Int(random, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	notBefore := now.Add(-24 * time.Hour).UTC().Truncate(time.Second) // a console clock a bit late
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: deviceID},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(random, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func parse(certPEM, keyPEM []byte) (Identity, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Identity{}, err
	}
	leaf := cert.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return Identity{}, err
		}
		cert.Leaf = leaf
	}
	if _, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok {
		return Identity{}, errors.New("certificate key is not ECDSA")
	}
	return Identity{Certificate: cert, Pin: auth.CertificatePin(leaf.RawSubjectPublicKeyInfo)}, nil
}

// ServerConfig is the TLS configuration of the API server: TLS 1.2 at
// least (Go prefers 1.3 and picks ChaCha20-Poly1305 by itself on a CPU
// without AES instructions), HTTP/1.1 only.
func (id Identity) ServerConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.Certificate},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}
}

// ClientConfig is a client configuration that accepts only a certificate
// whose public key has this pin (tests, tools): no CA, no host name.
func ClientConfig(pin string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // replaced by the pin check below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no certificate")
			}
			if auth.CertificatePin(cs.PeerCertificates[0].RawSubjectPublicKeyInfo) != pin {
				return errors.New("certificate pin mismatch")
			}
			return nil
		},
	}
}
