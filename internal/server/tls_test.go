package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/events"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/tlsid"
)

// M13: with Options.TLS, Serve answers over TLS only, the pinned client
// signs and reads the event stream (M14) over it, and plain HTTP gets no
// API answer.
func TestServeTLS(t *testing.T) {
	ident, err := tlsid.LoadOrCreate(tlsid.Options{Dir: t.TempDir(), DeviceID: testDeviceID})
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, func(o *Options) { o.TLS = ident.ServerConfig() })
	f.fastEvents(events.Options{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.server.Serve(ctx, ln) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return")
		}
	}()
	addr := ln.Addr().String()
	f.client.BaseURL = "https://" + addr
	f.client.HTTP = &http.Client{Transport: &http.Transport{TLSClientConfig: tlsid.ClientConfig(ident.Pin)}}

	// /hello and a signed route over the pinned connection.
	r := f.client.Send(t, mustRequest(t, "GET", f.client.BaseURL+"/api/v1/hello"))
	f.check("GET", "/hello", r)
	if r.Status != 200 {
		t.Fatalf("hello over TLS: %d %s", r.Status, r.Body)
	}
	f.do("GET", "/api/v1/systems", "/systems", nil, http.StatusOK)

	// The event stream works over TLS.
	s := f.openEvents(f.client)
	if s.Resp.TLS == nil || s.Resp.ProtoMajor != 1 {
		t.Fatalf("stream: TLS %v, proto %s", s.Resp.TLS != nil, s.Resp.Proto)
	}
	f.do("DELETE", "/api/v1/games/"+library.EncodeID(gbaFolder+"/Starlight Courier.gba"), "/games/{gameId}", nil, http.StatusOK)
	if ev := f.nextEvent(s); ev.Type != events.TypeLibrary {
		t.Fatalf("event %+v", ev)
	}

	// Another certificate (a fake pak) is refused by the pin.
	other, err := tlsid.LoadOrCreate(tlsid.Options{Dir: t.TempDir(), DeviceID: testDeviceID})
	if err != nil {
		t.Fatal(err)
	}
	wrong := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsid.ClientConfig(other.Pin)}}
	if _, err := wrong.Get(f.client.BaseURL + "/api/v1/hello"); err == nil {
		t.Fatal("a wrong pin was accepted")
	}
	// Plain HTTP gets no answer from the API.
	resp, err := http.Get("http://" + addr + "/api/v1/hello")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatalf("plain HTTP served: %s", body)
		}
	}
}

func mustRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
