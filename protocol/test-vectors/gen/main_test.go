package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The committed vectors must be exactly the generator output. Regenerate
// with "make vectors" after changing the generator.
func TestCommittedVectorsMatchGenerator(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := Check("..", files); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDetectsDrift(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Check(dir, files); err != nil {
		t.Fatalf("fresh copy refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ids.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Check(dir, files); err == nil {
		t.Fatal("drift not detected")
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	for name := range a {
		if !bytes.Equal(a[name], b[name]) {
			t.Errorf("%s is not deterministic", name)
		}
	}
}
