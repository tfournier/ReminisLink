package tlsid

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

const testDeviceID = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"

func opts(dir string) Options {
	return Options{Dir: dir, DeviceID: testDeviceID, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestCreatedOnceThenReused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	first, err := LoadOrCreate(opts(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Pin == "" {
		t.Fatalf("first = %+v", first)
	}
	leaf := first.Certificate.Leaf
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() || leaf.Subject.CommonName != testDeviceID {
		t.Fatalf("certificate: %T %s", leaf.PublicKey, leaf.Subject.CommonName)
	}
	if d := leaf.NotAfter.Sub(leaf.NotBefore); d < 19*365*24*time.Hour {
		t.Fatalf("validity %v", d)
	}
	if auth.CertificatePin(leaf.RawSubjectPublicKeyInfo) != first.Pin {
		t.Fatal("pin is not the SPKI hash")
	}
	certBefore, _ := os.ReadFile(filepath.Join(dir, CertFile))
	// Restart: same files, same pin, nothing rewritten.
	for i := 0; i < 2; i++ {
		again, err := LoadOrCreate(opts(dir))
		if err != nil {
			t.Fatal(err)
		}
		if again.Created || again.Pin != first.Pin {
			t.Fatalf("restart %d: created %v, pin changed %v", i, again.Created, again.Pin != first.Pin)
		}
	}
	if certAfter, _ := os.ReadFile(filepath.Join(dir, CertFile)); !bytes.Equal(certBefore, certAfter) {
		t.Fatal("certificate rewritten")
	}
	for _, f := range []string{CertFile, KeyFile} {
		if _, err := os.Stat(filepath.Join(dir, f+".tmp")); !os.IsNotExist(err) {
			t.Fatalf("%s.tmp left behind", f)
		}
	}
}

func TestRegeneratedOnlyWhenMissing(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreate(opts(dir))
	if err != nil {
		t.Fatal(err)
	}
	// The key alone is missing (crash between the two writes): new pair.
	if err := os.Remove(filepath.Join(dir, CertFile)); err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(opts(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Created || second.Pin == first.Pin {
		t.Fatal("missing certificate not replaced")
	}
	// A damaged key is moved aside, never deleted, then replaced.
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := LoadOrCreate(opts(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !third.Created || third.Pin == second.Pin {
		t.Fatal("damaged key not replaced")
	}
	if b, err := os.ReadFile(filepath.Join(dir, KeyFile+".bad")); err != nil || string(b) != "garbage" {
		t.Fatalf("damaged key not kept aside: %q %v", b, err)
	}
}

// A real TLS handshake: TLS 1.2 or later, HTTP/1.1, and the pinned client
// config refuses another certificate.
func TestServeAndPin(t *testing.T) {
	id, err := LoadOrCreate(opts(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	other, err := LoadOrCreate(opts(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", id.ServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.Version < tls.VersionTLS12 {
			t.Error("not TLS 1.2+")
		}
		io.WriteString(w, "ok")
	}), ErrorLog: log.New(io.Discard, "", 0)}
	go srv.Serve(ln)
	defer srv.Close()
	url := "https://" + ln.Addr().String() + "/"

	good := &http.Client{Transport: &http.Transport{TLSClientConfig: ClientConfig(id.Pin)}}
	resp, err := good.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" || resp.TLS.Version != tls.VersionTLS13 || resp.ProtoMajor != 1 {
		t.Fatalf("body %q, version %x, proto %s", body, resp.TLS.Version, resp.Proto)
	}
	bad := &http.Client{Transport: &http.Transport{TLSClientConfig: ClientConfig(other.Pin)}}
	if _, err := bad.Get(url); err == nil {
		t.Fatal("wrong pin accepted")
	}
	// No system trust: a default client refuses the self-signed certificate.
	if _, err := http.Get(url); err == nil {
		t.Fatal("self-signed certificate trusted by default")
	}
	// Plain HTTP on the TLS port gets no API answer.
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, _ := io.ReadAll(conn)
	if bytes.Contains(reply, []byte("ok")) {
		t.Fatal("plain HTTP served")
	}
	// TLS 1.1 is refused.
	old := ClientConfig(id.Pin)
	old.MaxVersion = tls.VersionTLS11
	old.MinVersion = tls.VersionTLS10
	if c, err := tls.Dial("tcp", ln.Addr().String(), old); err == nil {
		c.Close()
		t.Fatal("TLS 1.1 accepted")
	}
}

func TestGenerate(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	certPEM, keyPEM, err := Generate(testDeviceID, now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !leaf.NotBefore.Before(now) || !leaf.NotAfter.After(now.Add(19*365*24*time.Hour)) {
		t.Fatalf("validity %v - %v", leaf.NotBefore, leaf.NotAfter)
	}
	if err := leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature); err != nil {
		t.Fatalf("not self-signed: %v", err)
	}
}
