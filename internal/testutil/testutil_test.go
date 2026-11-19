package testutil

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfournier/ReminisLink/internal/auth"
)

func TestCopySDCard(t *testing.T) {
	sd := CopySDCard(t)
	if _, err := os.Stat(filepath.Join(sd, "Roms", "Game Boy Advance (GBA)", "Starlight Courier.gba")); err != nil {
		t.Fatal(err)
	}
	p := WriteFile(t, sd, "a/b/c.txt", "x")
	if data, err := os.ReadFile(p); err != nil || string(data) != "x" {
		t.Fatalf("%q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(SDCardSource(), "a")); err == nil {
		t.Fatal("the copy wrote into the repository")
	}
}

func TestClientSignsLikeTheApp(t *testing.T) {
	clients, err := auth.OpenClientStore(filepath.Join(t.TempDir(), "clients.json"))
	if err != nil {
		t.Fatal(err)
	}
	v := auth.NewVerifier(clients, nil, 0, nil)
	srv := httptest.NewServer(v.Require(auth.RouteJSON, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":{"code":"x"}}`))
	})))
	defer srv.Close()
	c := PairClient(t, clients, srv.URL)
	if r := c.Do(t, "POST", "/api/v1/a?b=%20c", []byte(`{}`)); r.Status != 200 || r.ErrorCode() != "x" {
		t.Fatalf("signed request: %d %s", r.Status, r.Body)
	}
	c.Key = make([]byte, auth.KeySize)
	if r := c.Do(t, "GET", "/api/v1/a", nil); r.Status != 401 || r.ErrorCode() != auth.CodeBadSignature {
		t.Fatalf("wrong key: %d %s", r.Status, r.Body)
	}
}

func TestEncodeBPS(t *testing.T) {
	p := EncodeBPS([]byte("abcdef"), []byte("abXYef!"))
	if string(p[:4]) != "BPS1" || len(p) < 4+3+12 {
		t.Fatalf("%q", p)
	}
}
