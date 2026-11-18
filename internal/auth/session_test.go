package auth

import (
	"bytes"
	"crypto/rand"
	"errors"
	"regexp"
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
	return s, clock
}

func clientRequest(t *testing.T, method PairingMethod, ikm []byte, clientID string) PairRequest {
	t.Helper()
	nonce := make([]byte, PairingNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	n := EncodeB64U(nonce)
	return PairRequest{Method: method, ClientID: clientID, ClientNonce: n, Proof: ClientProof(ikm, testDeviceID, clientID, n)}
}

func badRequest(t *testing.T) PairRequest {
	return clientRequest(t, MethodQR, []byte("wrong secret"), testClientA)
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
	if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(snap.PIN) {
		t.Fatalf("PIN %q", snap.PIN)
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
	req := clientRequest(t, MethodQR, snap.Secret, testClientA)

	res, _, err := s.Attempt(req)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	// The client derives the same key and checks the device proof.
	cn, _ := DecodeB64U(req.ClientNonce)
	dn, err := DecodeB64U(res.DeviceNonce)
	if err != nil || len(dn) != PairingNonceSize {
		t.Fatalf("device nonce %q", res.DeviceNonce)
	}
	key, err := DeriveKey(snap.Secret, cn, dn, testDeviceID, testClientA)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, res.Key) || res.ClientID != testClientA {
		t.Fatal("client and device keys differ")
	}
	if !VerifyDeviceProof(key, testDeviceID, testClientA, res.Proof) {
		t.Fatal("device proof rejected by the client")
	}

	// Single use: the same secret no longer works.
	after, _ := s.Snapshot()
	if bytes.Equal(after.Secret, snap.Secret) || after.PIN == snap.PIN && bytes.Equal(after.Secret, snap.Secret) {
		t.Fatal("secret not regenerated after success")
	}
	if _, _, err := s.Attempt(clientRequest(t, MethodQR, snap.Secret, testClientA)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("reused secret: error = %v", err)
	}
}

func TestPairingPINSuccess(t *testing.T) {
	s, _ := newTestSession(t)
	snap, _ := s.Snapshot()
	res, _, err := s.Attempt(clientRequest(t, MethodPIN, PINIKM(snap.PIN), testClientB))
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if len(res.Key) != KeySize {
		t.Fatal("no key")
	}
	// The QR secret does not work in PIN mode.
	snap, _ = s.Snapshot()
	if _, _, err := s.Attempt(clientRequest(t, MethodPIN, snap.Secret, testClientB)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("error = %v", err)
	}
}

func TestPairingExpiredSecretFails(t *testing.T) {
	s, clock := newTestSession(t)
	snap, _ := s.Snapshot()
	clock.Advance(PairingTTL)
	if _, _, err := s.Attempt(clientRequest(t, MethodQR, snap.Secret, testClientA)); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("error = %v, want ErrPairingFailed", err)
	}
	fresh, _ := s.Snapshot()
	if bytes.Equal(fresh.Secret, snap.Secret) {
		t.Fatal("expired secret not regenerated")
	}
	if _, _, err := s.Attempt(clientRequest(t, MethodQR, fresh.Secret, testClientA)); err != nil {
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
	good := clientRequest(t, MethodQR, snap.Secret, testClientA)
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
	if _, _, err := s.Attempt(clientRequest(t, MethodQR, snap.Secret, testClientA)); err != nil {
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
	good := clientRequest(t, MethodQR, snap.Secret, testClientA)
	cases := map[string]func(*PairRequest){
		"method":          func(r *PairRequest) { r.Method = "password" },
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
