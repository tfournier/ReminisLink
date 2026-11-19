package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"
	"time"
)

const (
	// PairingTTL is how long a pairing secret and PIN stay valid.
	PairingTTL = 10 * time.Minute
	// MaxPairingFailures is the number of failed attempts before the secret
	// and PIN are regenerated and pairing is blocked.
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

// PairRequest is the authentication part of POST /pair.
type PairRequest struct {
	Method      PairingMethod
	ClientID    string
	ClientNonce string // b64u, exactly as received
	Proof       string // b64u
}

// PairResult is what a successful attempt produces. The caller stores the
// client (ClientStore.Put) and answers with DeviceNonce and Proof.
type PairResult struct {
	ClientID    string
	Key         []byte
	DeviceNonce string // b64u
	Proof       string // b64u
}

// PairingSnapshot is what the pak displays.
type PairingSnapshot struct {
	Secret       []byte // the QR code carries b64u(Secret)
	PIN          string
	ExpiresAt    time.Time
	BlockedUntil time.Time // zero when not blocked
	Generation   uint64    // changes each time the secret is regenerated
	// Open reports whether pairing is open (the pairing screen is shown,
	// docs/specs/05-console-pak.md §7.1). Secret and PIN are only
	// meaningful while it is.
	Open bool
}

// PairingSession holds the current pairing secret and PIN: 32 random bytes
// and 6 random digits, valid PairingTTL, single use. After
// MaxPairingFailures failed attempts both are regenerated and pairing is
// blocked for PairingBlock.
//
// Pairing is on demand (M8, 05 §7.1): the session starts closed, Open
// draws a new secret and PIN when the pairing screen appears and Close
// invalidates them when it goes away. While closed, every well-formed
// attempt fails without counting as a failure.
type PairingSession struct {
	mu       sync.Mutex
	deviceID string
	now      func() time.Time
	rand     io.Reader

	secret       []byte
	pin          string
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

// Open opens pairing with a new secret and PIN. Opening a session already
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

// Close closes pairing and invalidates the secret and the PIN: a new
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

// Snapshot returns the current secret and PIN, regenerating them first if
// they expired while pairing is open.
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
		PIN:        s.pin,
		ExpiresAt:  s.expiresAt,
		Generation: s.generation,
		Open:       s.open,
	}
	if now.Before(s.blockedUntil) {
		snap.BlockedUntil = s.blockedUntil
	}
	return snap, nil
}

// Attempt checks a pairing request. On success it derives the key and the
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
	clientNonce, err := DecodeB64U(req.ClientNonce)
	if err != nil || len(clientNonce) != PairingNonceSize {
		return PairResult{}, 0, fmt.Errorf("%w: clientNonce must be %d bytes in base64url", ErrInvalidPairRequest, PairingNonceSize)
	}
	var ikm []byte
	switch req.Method {
	case MethodQR:
		ikm = s.secret
	case MethodPIN:
		ikm = PINIKM(s.pin)
	default:
		return PairResult{}, 0, fmt.Errorf("%w: method must be qr or pin", ErrInvalidPairRequest)
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
	if !VerifyClientProof(ikm, s.deviceID, req.ClientID, req.ClientNonce, req.Proof) {
		return PairResult{}, 0, s.failLocked(now)
	}
	deviceNonce := make([]byte, PairingNonceSize)
	if _, err := io.ReadFull(s.rand, deviceNonce); err != nil {
		return PairResult{}, 0, fmt.Errorf("device nonce: %w", err)
	}
	key, err := DeriveKey(ikm, clientNonce, deviceNonce, s.deviceID, req.ClientID)
	if err != nil {
		return PairResult{}, 0, err
	}
	res = PairResult{
		ClientID:    req.ClientID,
		Key:         key,
		DeviceNonce: EncodeB64U(deviceNonce),
		Proof:       DeviceProof(key, s.deviceID, req.ClientID),
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

// regenerateLocked draws a new secret and PIN and resets the failure count.
func (s *PairingSession) regenerateLocked(now time.Time) error {
	secret := make([]byte, SecretSize)
	if _, err := io.ReadFull(s.rand, secret); err != nil {
		return fmt.Errorf("pairing secret: %w", err)
	}
	n, err := rand.Int(s.rand, big.NewInt(1_000_000))
	if err != nil {
		return fmt.Errorf("pairing PIN: %w", err)
	}
	s.secret = secret
	s.pin = fmt.Sprintf("%0*d", PINLength, n.Int64())
	s.expiresAt = now.Add(PairingTTL)
	s.failures = 0
	s.generation++
	return nil
}
