package library

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type idVectors struct {
	Valid []struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		PathUTF8Hex string `json:"pathUtf8Hex"`
		ID          string `json:"id"`
	} `json:"valid"`
	Invalid []struct {
		Name   string `json:"name"`
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"invalid"`
}

func loadIDVectors(t *testing.T) idVectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "test-vectors", "ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v idVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Valid) < 10 || len(v.Invalid) == 0 {
		t.Fatalf("not enough vectors: %d valid, %d invalid", len(v.Valid), len(v.Invalid))
	}
	return v
}

func TestIDVectorsValid(t *testing.T) {
	for _, c := range loadIDVectors(t).Valid {
		t.Run(c.Name, func(t *testing.T) {
			if got := hex.EncodeToString([]byte(c.Path)); got != c.PathUTF8Hex {
				t.Fatalf("path bytes = %s, want %s", got, c.PathUTF8Hex)
			}
			if got := EncodeID(c.Path); got != c.ID {
				t.Fatalf("EncodeID = %q, want %q", got, c.ID)
			}
			got, err := DecodeID(c.ID)
			if err != nil {
				t.Fatalf("DecodeID: %v", err)
			}
			if got != c.Path {
				t.Fatalf("DecodeID = %q, want %q", got, c.Path)
			}
		})
	}
}

func TestIDVectorsInvalid(t *testing.T) {
	root := t.TempDir()
	for _, c := range loadIDVectors(t).Invalid {
		t.Run(c.Name, func(t *testing.T) {
			if _, err := DecodeID(c.ID); !errors.Is(err, ErrInvalidID) {
				t.Fatalf("DecodeID(%q) error = %v, want ErrInvalidID", c.ID, err)
			}
			if _, err := ResolveID(root, c.ID); !errors.Is(err, ErrInvalidID) {
				t.Fatalf("ResolveID(%q) error = %v, want ErrInvalidID", c.ID, err)
			}
		})
	}
}
