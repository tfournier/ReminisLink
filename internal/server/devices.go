package server

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// The paired clients as the console manages them (M10, kept by M13): the
// disconnection by the console and forgetting a client. Everything here
// is in memory only, except the client store.

// CodeDisconnected is the 401 code of a client disconnected by the console.
const CodeDisconnected = "disconnected"

// gate is the auth.Gate of every signed route, the one place where the
// disconnection and the access generation are enforced (see access). The
// verifier calls it after the signature check and the nonce reservation,
// before reading the body, so a refused request changes nothing and its
// body is never read. The admission is committed once the request is
// accepted (body verified).
func (s *Server) gate(r *http.Request, c auth.Client, opts auth.RouteOptions) (*auth.Admission, *auth.Rejection) {
	adm, rej := s.admit(c, opts)
	if hook := s.gateHook.Load(); hook != nil {
		(*hook)()
	}
	return adm, rej
}

func (s *Server) admit(c auth.Client, opts auth.RouteOptions) (*auth.Admission, *auth.Rejection) {
	ad, rej := s.access.admit(c, opts)
	if rej != nil {
		return nil, rej
	}
	return &auth.Admission{
		Commit: func() *auth.Rejection {
			rej, lifted := s.access.accept(ad, opts)
			if lifted {
				s.log.Info("client reconnected", "client", c.ID, "name", c.Name)
			}
			return rej
		},
		Value: ad,
	}, nil
}

// handleConnect answers POST /clients/me/connect. The disconnection was
// lifted, and the client recorded as connected, when the gate accepted the
// request (access.accept).
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// ErrUnknownClient is returned for a client that is not paired.
var ErrUnknownClient = errors.New("unknown client")

// Disconnect disconnects a paired client from the console (Settings >
// Devices): it leaves the connected clients, and its signed requests get
// 401 disconnected until it calls POST /clients/me/connect; its requests
// in flight are refused. In memory only.
func (s *Server) Disconnect(id string) error {
	c, ok := s.access.disconnect(id)
	if !ok {
		return ErrUnknownClient
	}
	s.log.Info("client disconnected by the console", "client", id, "name", c.Name)
	return nil
}

// Forget removes a paired client (Settings > Devices > Forget): its
// pairing is removed from the client store, so its next requests get
// 401 unknown_client, and its disconnection is dropped. It reports whether
// the client existed.
func (s *Server) Forget(id string) (bool, error) { return s.access.forget(id) }

// ForgetAll removes every paired client and returns how many there were.
func (s *Server) ForgetAll() (int, error) { return s.access.forgetAll() }

// DeviceStatus is a paired client as the Devices screen shows it.
type DeviceStatus struct {
	ID       string
	Name     string
	PairedAt time.Time
	// Platform is the ClientPlatform given at pairing (apple, android,
	// web, other) and Platforms the X-RL-Platform values seen (M13).
	Platform  string
	Platforms []string
	// Connected: a signed request accepted in the last ConnectedWindow, or
	// an open event stream (M14).
	Connected bool
	// Disconnected by the console, until it reconnects from the app.
	Disconnected bool
}

// Devices returns the paired clients with their state, the most recently
// paired first.
func (s *Server) Devices() []DeviceStatus {
	out := s.access.devices(s.now().Add(-ConnectedWindow))
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].PairedAt.Equal(out[j].PairedAt) {
			return out[i].PairedAt.After(out[j].PairedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
