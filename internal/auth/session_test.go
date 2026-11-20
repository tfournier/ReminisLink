package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/url"
	"testing"
	"time"
)

const testDeviceID = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"

func newTestSession(t *testing.T) (*PairingSession, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	s, err := NewPairingSession(testDeviceID, clock.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Open(); err != nil {
		t.Fatal(err)
	}
	return s, clock
}

// clientRequest is a pairing v2 request made with secret, for a new
// Ed25519 key.
func clientRequest(t *testing.T, secret []byte, clientID string) PairRequest {
	t.Helper()
	nonce := make([]byte, PairingNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	n, k := EncodeB64U(nonce), EncodeB64U(pub)
	return PairRequest{ClientID: clientID, PublicKey: k, ClientNonce: n, Proof: PairClientProof(secret, testDeviceID, clientID, k, n)}
}

func badRequest(t *testing.T) PairRequest {
	return clientRequest(t, []byte("wrong secret"), testClientA)
}

func TestPairingSnapshot(t *testing.T) {
	s, clock := newTestSession(t)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Secret) != SecretSize {
		t.Fatalf("secret length %d", len(snap.Secret))
	}
	if !snap.ExpiresAt.Equal(clock.Now().Add(PairingTTL)) {
		t.Fatalf("expires at %v", snap.ExpiresAt)
	}
	again, _ := s.Snapshot()
	if !bytes.Equal(again.Secret, snap.Secret) || again.Generation != snap.Generation {
		t.Fatal("snapshot changed without reason")
	}
	clock.Advance(PairingTTL)
	expired, _ := s.Snapshot()
	if bytes.Equal(expired.Secret, snap.Secret) || expired.Generation == snap.Generation {
		t.Fatal("secret not regenerated after expiry")
	}
}

func TestPairingQRSuccessIsSingleUse(t *testing.T) {
	s, _ := newTestSession(t)
	snap, _ := s.Snapshot()
	req := clientRequest(t, snap.Secret, testClientA)

	res, _, err := s.Attempt(req)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	// The pak keeps the client's public key; the client checks the
	// device proof.
	dn, err := DecodeB64U(res.DeviceNonce)
	if err != nil || len(dn) != PairingNonceSize {
		t.Fatalf("device nonce %q", res.DeviceNonce)
	}
	if EncodeB64U(res.PublicKey) != req.PublicKey || res.ClientID != testClientA {
		t.Fatal("public key or client id not kept")
	}
	if !VerifyPairDeviceProof(snap.Secret, testDeviceID, testClientA, req.PublicKey, req.ClientNonce, res.DeviceNonce, res.Proof) {
		t.Fatal("device proof rejected by the client")
	}
	// The proof binds the public key: another key does not verify.
	other := clientRequest(t, snap.Secret, testClientA)
	if VerifyPairDeviceProof(snap.Secret, testDeviceID, testClientA, other.PublicKey, req.ClientNonce, res.DeviceNonce, res.Proof) {
		t.Fatal("device proof valid for another key")
	}

	// Single use: the same secret no longer works.
	after, _ := s.Snapshot()
	if bytes.Equal(after.Secret, snap.Secret) {
		t.Fatal("secret not regenerated after success")
	}
	if _, _, err := s.Attempt(clientRequest(t, snap.Secret, testClientA)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("reused secret: error = %v", err)
	}
}

// A proof made for one public key does not pair another one (the key
// sent is the key proven).
func TestPairingProofBindsThePublicKey(t *testing.T) {
	s, _ := newTestSession(t)
	snap, _ := s.Snapshot()
	req := clientRequest(t, snap.Secret, testClientB)
	other := clientRequest(t, snap.Secret, testClientB)
	req.PublicKey = other.PublicKey
	if _, _, err := s.Attempt(req); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("swapped key: %v", err)
	}
	req = clientRequest(t, snap.Secret, testClientB)
	req.ClientID = testClientA
	if _, _, err := s.Attempt(req); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("swapped client id: %v", err)
	}
}

func TestPairingExpiredSecretFails(t *testing.T) {
	s, clock := newTestSession(t)
	snap, _ := s.Snapshot()
	clock.Advance(PairingTTL)
	if _, _, err := s.Attempt(clientRequest(t, snap.Secret, testClientA)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("error = %v, want ErrPairingFailed", err)
	}
	fresh, _ := s.Snapshot()
	if bytes.Equal(fresh.Secret, snap.Secret) {
		t.Fatal("expired secret not regenerated")
	}
	if _, _, err := s.Attempt(clientRequest(t, fresh.Secret, testClientA)); err != nil {
		t.Fatalf("fresh secret refused: %v", err)
	}
}

func TestPairingBlocksAfterFiveFailures(t *testing.T) {
	s, clock := newTestSession(t)
	first, _ := s.Snapshot()
	for i := 1; i <= MaxPairingFailures; i++ {
		if _, _, err := s.Attempt(badRequest(t)); !errors.Is(err, ErrPairingFailed) {
			t.Fatalf("failure %d: error = %v", i, err)
		}
	}
	snap, _ := s.Snapshot()
	if bytes.Equal(snap.Secret, first.Secret) || snap.Generation == first.Generation {
		t.Fatal("secret not regenerated after 5 failures")
	}
	if !snap.BlockedUntil.Equal(clock.Now().Add(PairingBlock)) {
		t.Fatalf("blocked until %v", snap.BlockedUntil)
	}

	// Blocked, even with the right secret.
	good := clientRequest(t, snap.Secret, testClientA)
	_, retry, err := s.Attempt(good)
	if !errors.Is(err, ErrTooManyAttempts) || retry != PairingBlock {
		t.Fatalf("blocked attempt: retry %v, error %v", retry, err)
	}
	clock.Advance(PairingBlock - time.Second)
	if _, retry, err := s.Attempt(good); !errors.Is(err, ErrTooManyAttempts) || retry != time.Second {
		t.Fatalf("still blocked: retry %v, error %v", retry, err)
	}

	clock.Advance(time.Second)
	if s2, _ := s.Snapshot(); !s2.BlockedUntil.IsZero() {
		t.Fatal("block not lifted")
	}
	if _, _, err := s.Attempt(good); err != nil {
		t.Fatalf("after the block: %v", err)
	}
}

func TestPairingFailureCountResetsOnRegeneration(t *testing.T) {
	s, _ := newTestSession(t)
	for i := 0; i < MaxPairingFailures-1; i++ {
		_, _, _ = s.Attempt(badRequest(t))
	}
	snap, _ := s.Snapshot()
	if _, _, err := s.Attempt(clientRequest(t, snap.Secret, testClientA)); err != nil {
		t.Fatal(err)
	}
	// A success regenerates the secret and resets the count: 4 more
	// failures must not block.
	for i := 0; i < MaxPairingFailures-1; i++ {
		_, _, _ = s.Attempt(badRequest(t))
	}
	if snap, _ := s.Snapshot(); !snap.BlockedUntil.IsZero() {
		t.Fatal("blocked although the count was reset")
	}
}

func TestPairingMalformedRequests(t *testing.T) {
	s, _ := newTestSession(t)
	snap, _ := s.Snapshot()
	good := clientRequest(t, snap.Secret, testClientA)
	cases := map[string]func(*PairRequest){
		"key-missing":     func(r *PairRequest) { r.PublicKey = "" },
		"key-short":       func(r *PairRequest) { r.PublicKey = EncodeB64U(make([]byte, 31)) },
		"key-std-b64":     func(r *PairRequest) { r.PublicKey = r.PublicKey[:42] + "+/" },
		"client-id":       func(r *PairRequest) { r.ClientID = "3B1C8D2A-5E6F-4A7B-8C9D-0E1F2A3B4C5D" },
		"nonce-short":     func(r *PairRequest) { r.ClientNonce = EncodeB64U(make([]byte, 16)) },
		"nonce-not-b64":   func(r *PairRequest) { r.ClientNonce = "!!!" },
		"nonce-padded":    func(r *PairRequest) { r.ClientNonce += "=" },
		"empty-client-id": func(r *PairRequest) { r.ClientID = "" },
	}
	for name, mutate := range cases {
		r := good
		mutate(&r)
		if _, _, err := s.Attempt(r); !errors.Is(err, ErrInvalidPairRequest) {
			t.Errorf("%s: error = %v, want ErrInvalidPairRequest", name, err)
		}
	}
	// Malformed requests do not count as failures nor consume the secret.
	if _, _, err := s.Attempt(good); err != nil {
		t.Fatalf("good request after malformed ones: %v", err)
	}
}

func TestPairingRandomFailure(t *testing.T) {
	if _, err := NewPairingSession(testDeviceID, nil, bytes.NewReader(nil)); err == nil {
		t.Fatal("empty random source accepted")
	}
}

func TestPairingLink(t *testing.T) {
	secret := make([]byte, SecretSize)
	secret[0] = 0xfb
	pin := EncodeB64U(bytes.Repeat([]byte{0xff}, 32))
	got := PairingLink("7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "Brick de Thomas & co/é", secret, "192.168.1.42", 47821, pin)
	want := "reminislink://pair?v=2&id=7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10&n=Brick%20de%20Thomas%20%26%20co%2F%C3%A9" +
		"&s=-wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&h=192.168.1.42&p=47821&k=" + pin
	if got != want {
		t.Fatalf("PairingLink =\n %s\nwant\n %s", got, want)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "reminislink" || u.Host != "pair" || q.Get("v") != "2" || q.Get("n") != "Brick de Thomas & co/é" || q.Get("p") != "47821" || q.Get("k") != pin {
		t.Fatalf("parsed: %+v %v", u, q)
	}
	if s, err := DecodeB64U(q.Get("s")); err != nil || len(s) != SecretSize || s[0] != 0xfb {
		t.Fatalf("secret: %x %v", s, err)
	}
}

func TestPairingOnDemand(t *testing.T) {
	clock := newFakeClock()
	s, err := NewPairingSession(testDeviceID, clock.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A new session is closed: even a proof made with its hidden secret
	// fails, and it never counts as a failure.
	hidden, _ := s.Snapshot()
	if hidden.Open || s.IsOpen() {
		t.Fatal("a new session is open")
	}
	for i := 0; i < 2*MaxPairingFailures; i++ {
		if _, _, err := s.Attempt(clientRequest(t, hidden.Secret, testClientA)); !errors.Is(err, ErrPairingFailed) {
			t.Fatalf("closed session: error = %v", err)
		}
	}
	if snap, _ := s.Snapshot(); !snap.BlockedUntil.IsZero() {
		t.Fatal("attempts on a closed session were counted")
	}
	// Malformed requests are still refused as such.
	bad := clientRequest(t, hidden.Secret, testClientA)
	bad.PublicKey = "short"
	if _, _, err := s.Attempt(bad); !errors.Is(err, ErrInvalidPairRequest) {
		t.Fatalf("malformed on a closed session: %v", err)
	}

	// Opening draws a new secret.
	if err := s.Open(); err != nil {
		t.Fatal(err)
	}
	opened, _ := s.Snapshot()
	if !opened.Open || bytes.Equal(opened.Secret, hidden.Secret) || opened.Generation == hidden.Generation {
		t.Fatal("Open did not draw a new secret")
	}
	if !opened.ExpiresAt.Equal(clock.Now().Add(PairingTTL)) {
		t.Fatalf("expires at %v", opened.ExpiresAt)
	}
	// Opening again keeps it (the screen is redrawn).
	if err := s.Open(); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Snapshot(); again.Generation != opened.Generation {
		t.Fatal("a second Open changed the secret")
	}

	// Closing invalidates what was shown.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Attempt(clientRequest(t, opened.Secret, testClientA)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("secret shown before Close: %v", err)
	}
	if err := s.SetOpen(true); err != nil {
		t.Fatal(err)
	}
	reopened, _ := s.Snapshot()
	if bytes.Equal(reopened.Secret, opened.Secret) {
		t.Fatal("reopening reused the old secret")
	}
	if _, _, err := s.Attempt(clientRequest(t, opened.Secret, testClientA)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("old secret after reopening: %v", err)
	}
	if _, _, err := s.Attempt(clientRequest(t, reopened.Secret, testClientA)); err != nil {
		t.Fatalf("secret of the open session: %v", err)
	}
}

func TestPairingClosedDoesNotRegenerateOnExpiry(t *testing.T) {
	clock := newFakeClock()
	s, err := NewPairingSession(testDeviceID, clock.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot()
	clock.Advance(2 * PairingTTL)
	if after, _ := s.Snapshot(); after.Generation != before.Generation {
		t.Fatal("a closed session regenerated its hidden secret")
	}
}

// Codex review of M8: after five failures, closing the pairing screen
// must answer pairing_failed (not too_many_attempts); the block applies
// again when the screen opens before its end.
func TestPairingClosedAnswersFailedDuringABlock(t *testing.T) {
	s, clock := newTestSession(t)
	for i := 0; i < MaxPairingFailures; i++ {
		s.Attempt(badRequest(t))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Attempt(badRequest(t)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("closed during the block: %v", err)
	}
	if err := s.Open(); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot()
	if _, retry, err := s.Attempt(clientRequest(t, snap.Secret, testClientA)); !errors.Is(err, ErrTooManyAttempts) || retry <= 0 {
		t.Fatalf("reopened during the block: %v %v", retry, err)
	}
	clock.Advance(PairingBlock)
	snap, _ = s.Snapshot()
	if _, _, err := s.Attempt(clientRequest(t, snap.Secret, testClientA)); err != nil {
		t.Fatalf("after the block: %v", err)
	}
}
