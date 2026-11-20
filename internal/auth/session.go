package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	// PairingTTL is how long a pairing secret stays valid.
	PairingTTL = 10 * time.Minute
	// MaxPairingFailures is the number of failed attempts before the secret
	// is regenerated and pairing is blocked.
	MaxPairingFailures = 5
	// PairingBlock is how long pairing stays blocked after too many failures.
	PairingBlock = 30 * time.Second
)

var (
	// ErrInvalidPairRequest is a malformed request (400 invalid_request).
	ErrInvalidPairRequest = errors.New("invalid pairing request")
	// ErrPairingFailed is a wrong proof or an expired secret (401 pairing_failed).
	ErrPairingFailed = errors.New("pairing failed")
	// ErrTooManyAttempts means pairing is blocked (429 too_many_attempts).
	ErrTooManyAttempts = errors.New("too many pairing attempts")
)

// PairRequest is the authentication part of POST /pair (v2, M13).
type PairRequest struct {
	ClientID    string
	PublicKey   string // clientPublicKey, b64u, exactly as received
	ClientNonce string // b64u, exactly as received
	Proof       string // b64u
}

// PairResult is what a successful attempt produces. The caller stores the
// client (ClientStore.Put) and answers with DeviceNonce and Proof.
type PairResult struct {
	ClientID    string
	PublicKey   ed25519.PublicKey
	DeviceNonce string // b64u
	Proof       string // b64u
}

// PairingSnapshot is what the pak displays.
type PairingSnapshot struct {
	Secret       []byte // the QR code carries b64u(Secret)
	ExpiresAt    time.Time
	BlockedUntil time.Time // zero when not blocked
	Generation   uint64    // changes each time the secret is regenerated
	// Open reports whether pairing is open (the pairing screen is shown,
	// docs/specs/05-console-pak.md §7.1). Secret is only meaningful
	// while it is.
	Open bool
}

// PairingSession holds the current pairing secret: 32 random bytes, valid
// PairingTTL, single use. There is no PIN (M13). After MaxPairingFailures
// failed attempts the secret is regenerated and pairing is blocked for
// PairingBlock.
//
// Pairing is on demand (M8, 05 §7.1): the session starts closed, Open
// draws a new secret when the pairing screen appears and Close
// invalidates it when it goes away. While closed, every well-formed
// attempt fails without counting as a failure.
type PairingSession struct {
	mu       sync.Mutex
	deviceID string
	now      func() time.Time
	rand     io.Reader

	secret       []byte
	expiresAt    time.Time
	failures     int
	blockedUntil time.Time
	generation   uint64
	open         bool
}

// NewPairingSession creates a closed session (see Open). A nil now uses
// time.Now and a nil random source uses crypto/rand.
func NewPairingSession(deviceID string, now func() time.Time, random io.Reader) (*PairingSession, error) {
	if now == nil {
		now = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	s := &PairingSession{deviceID: deviceID, now: now, rand: random}
	if err := s.regenerateLocked(now()); err != nil {
		return nil, err
	}
	return s, nil
}

// Open opens pairing with a new secret. Opening a session already
// open keeps its secret (the screen redraws without changing the QR code).
func (s *PairingSession) Open() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open {
		return nil
	}
	if err := s.regenerateLocked(s.now()); err != nil {
		return err
	}
	s.open = true
	return nil
}

// Close closes pairing and invalidates the secret: a new
// secret is drawn and kept hidden, so that nothing seen before still
// works. A block after too many failures stays in force.
func (s *PairingSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return nil
	}
	s.open = false
	return s.regenerateLocked(s.now())
}

// SetOpen opens or closes pairing.
func (s *PairingSession) SetOpen(open bool) error {
	if open {
		return s.Open()
	}
	return s.Close()
}

// IsOpen reports whether pairing is open.
func (s *PairingSession) IsOpen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// Snapshot returns the current secret, regenerating it first if it
// expired while pairing is open.
func (s *PairingSession) Snapshot() (PairingSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.open && !now.Before(s.expiresAt) {
		if err := s.regenerateLocked(now); err != nil {
			return PairingSnapshot{}, err
		}
	}
	snap := PairingSnapshot{
		Secret:     append([]byte(nil), s.secret...),
		ExpiresAt:  s.expiresAt,
		Generation: s.generation,
		Open:       s.open,
	}
	if now.Before(s.blockedUntil) {
		snap.BlockedUntil = s.blockedUntil
	}
	return snap, nil
}

// Attempt checks a pairing request (v2, M13). On success it produces the
// device proof and regenerates the secret (single use). When it returns
// ErrTooManyAttempts, retryAfter is how long pairing stays blocked.
//
// The fifth failure itself is answered with ErrPairingFailed; the attempts
// that follow during the block get ErrTooManyAttempts. Malformed requests
// do not count as failures (they do not test the secret).
func (s *PairingSession) Attempt(req PairRequest) (res PairResult, retryAfter time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !ValidID(req.ClientID) {
		return PairResult{}, 0, fmt.Errorf("%w: clientId must be a lowercase UUID", ErrInvalidPairRequest)
	}
	pub, err := DecodePublicKey(req.PublicKey)
	if err != nil {
		return PairResult{}, 0, fmt.Errorf("%w: clientPublicKey must be %d bytes in base64url", ErrInvalidPairRequest, PublicKeySize)
	}
	clientNonce, err := DecodeB64U(req.ClientNonce)
	if err != nil || len(clientNonce) != PairingNonceSize {
		return PairResult{}, 0, fmt.Errorf("%w: clientNonce must be %d bytes in base64url", ErrInvalidPairRequest, PairingNonceSize)
	}
	if !s.open {
		// Pairing on demand: closed, no secret to test, not a failure. This
		// comes before the block: closed pairing always answers
		// pairing_failed (the block still applies when it opens again).
		return PairResult{}, 0, ErrPairingFailed
	}
	if now.Before(s.blockedUntil) {
		return PairResult{}, s.blockedUntil.Sub(now), ErrTooManyAttempts
	}
	if !now.Before(s.expiresAt) {
		// Expired secret: the proof cannot match the new one. This counts as
		// a failure like any wrong proof.
		if err := s.regenerateLocked(now); err != nil {
			return PairResult{}, 0, err
		}
		return PairResult{}, 0, s.failLocked(now)
	}
	if !VerifyPairClientProof(s.secret, s.deviceID, req.ClientID, req.PublicKey, req.ClientNonce, req.Proof) {
		return PairResult{}, 0, s.failLocked(now)
	}
	deviceNonce := make([]byte, PairingNonceSize)
	if _, err := io.ReadFull(s.rand, deviceNonce); err != nil {
		return PairResult{}, 0, fmt.Errorf("device nonce: %w", err)
	}
	dn := EncodeB64U(deviceNonce)
	res = PairResult{
		ClientID:    req.ClientID,
		PublicKey:   pub,
		DeviceNonce: dn,
		Proof:       PairDeviceProof(s.secret, s.deviceID, req.ClientID, req.PublicKey, req.ClientNonce, dn),
	}
	if err := s.regenerateLocked(now); err != nil {
		return PairResult{}, 0, err
	}
	return res, 0, nil
}

func (s *PairingSession) failLocked(now time.Time) error {
	s.failures++
	if s.failures >= MaxPairingFailures {
		if err := s.regenerateLocked(now); err != nil {
			return err
		}
		s.blockedUntil = now.Add(PairingBlock)
	}
	return ErrPairingFailed
}

// regenerateLocked draws a new secret and resets the failure count.
func (s *PairingSession) regenerateLocked(now time.Time) error {
	secret := make([]byte, SecretSize)
	if _, err := io.ReadFull(s.rand, secret); err != nil {
		return fmt.Errorf("pairing secret: %w", err)
	}
	s.secret = secret
	s.expiresAt = now.Add(PairingTTL)
	s.failures = 0
	s.generation++
	return nil
}
