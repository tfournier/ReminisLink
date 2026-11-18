package auth

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// DeriveKey must be HKDF-SHA256 (RFC 5869) with salt = clientNonce ||
// deviceNonce: compare with a direct Extract/Expand computation.
func TestDeriveKeyMatchesHKDFSHA256(t *testing.T) {
	ikm := bytes.Repeat([]byte{0x0b}, 22)
	cn := bytes.Repeat([]byte{1}, 32)
	dn := bytes.Repeat([]byte{2}, 32)
	got, err := DeriveKey(ikm, cn, dn, "d", "c")
	if err != nil {
		t.Fatal(err)
	}
	// PRK = HMAC(salt, ikm); OKM = HMAC(PRK, info || 0x01) for L = 32.
	prk := mac(append(append([]byte{}, cn...), dn...), string(ikm))
	okm := mac(prk, KeyInfo("d", "c")+"\x01")
	if !bytes.Equal(got, okm) {
		t.Fatalf("DeriveKey does not match HKDF-SHA256")
	}
}

func TestDeriveKeyRejectsBadInputs(t *testing.T) {
	n := make([]byte, 32)
	if _, err := DeriveKey(nil, n, n, "d", "c"); err == nil {
		t.Error("empty ikm accepted")
	}
	if _, err := DeriveKey([]byte("x"), n[:31], n, "d", "c"); err == nil {
		t.Error("short client nonce accepted")
	}
	if _, err := DeriveKey([]byte("x"), n, append(n, 0), "d", "c"); err == nil {
		t.Error("long device nonce accepted")
	}
}

func TestVerifyProofRejects(t *testing.T) {
	ikm := []byte("secret")
	proof := ClientProof(ikm, "d", "c", "n")
	cases := map[string]string{
		"empty":     "",
		"padding":   proof + "=",
		"std-b64":   strings.NewReplacer("-", "+", "_", "/").Replace(proof) + "+/",
		"truncated": proof[:len(proof)-2],
		"newline":   proof[:10] + "\n" + proof[10:],
		"cr":        "\r" + proof,
		"other":     ClientProof(ikm, "d", "c", "m"),
	}
	for name, p := range cases {
		if VerifyClientProof(ikm, "d", "c", "n", p) {
			t.Errorf("%s: accepted", name)
		}
	}
	if VerifyClientProof([]byte("other"), "d", "c", "n", proof) {
		t.Error("wrong ikm accepted")
	}
	if !VerifyClientProof(ikm, "d", "c", "n", proof) {
		t.Error("good proof rejected")
	}
	key := bytes.Repeat([]byte{7}, 32)
	dp := DeviceProof(key, "d", "c")
	if VerifyDeviceProof(key, "d", "x", dp) || !VerifyDeviceProof(key, "d", "c", dp) {
		t.Error("device proof verification is wrong")
	}
}

func TestValidID(t *testing.T) {
	for _, ok := range []string{"3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", "00000000-0000-0000-0000-000000000000"} {
		if !ValidID(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "3B1C8D2A-5E6F-4A7B-8C9D-0E1F2A3B4C5D", "3b1c8d2a5e6f4a7b8c9d0e1f2a3b4c5d", "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d|x", "../x"} {
		if ValidID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCanonicalQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"&&", ""},
		{"b=2&a=1", "a=1&b=2"},
		{"a=b=c", "a=b%3Dc"},
		{"a", "a="},
		{"=v", "=v"},
		{"k=%2b+%20", "k=%2B%20%20"},
		{"k=%00", "k=%00"},
		{"a=2&a=10&a=1", "a=1&a=10&a=2"},
		{"x=-._~", "x=-._~"},
		{"x=!*'();:@$,[]", "x=%21%2A%27%28%29%3B%3A%40%24%2C%5B%5D"},
		// Sorting is on encoded keys, byte order: "%" < "A" < "a".
		{"a=1&A=1&%C3%A9=1", "%C3%A9=1&A=1&a=1"},
	}
	for _, c := range cases {
		got, err := CanonicalQuery(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalRequestRejectsMalformed(t *testing.T) {
	good := RequestParts{
		Method: "GET", Path: "/api/v1/device", Time: "1795539600",
		Nonce: EncodeB64U(make([]byte, 16)), ContentSHA256: ContentSHA256(nil),
	}
	if _, err := CanonicalRequest(good); err != nil {
		t.Fatalf("good parts refused: %v", err)
	}
	mutate := map[string]func(*RequestParts){
		"empty-method":      func(p *RequestParts) { p.Method = "" },
		"method-newline":    func(p *RequestParts) { p.Method = "GET\nX" },
		"method-digit":      func(p *RequestParts) { p.Method = "G3T" },
		"method-dotless-i":  func(p *RequestParts) { p.Method = "getı" },
		"relative-path":     func(p *RequestParts) { p.Path = "api/v1/device" },
		"path-newline":      func(p *RequestParts) { p.Path = "/api\n/v1" },
		"path-space":        func(p *RequestParts) { p.Path = "/api /v1" },
		"path-query":        func(p *RequestParts) { p.Path = "/api?x=1" },
		"query-newline":     func(p *RequestParts) { p.RawQuery = "a=1\nb" },
		"query-bad-percent": func(p *RequestParts) { p.RawQuery = "a=%G0" },
		"time-empty":        func(p *RequestParts) { p.Time = "" },
		"time-negative":     func(p *RequestParts) { p.Time = "-1" },
		"time-leading-zero": func(p *RequestParts) { p.Time = "01795539600" },
		"time-float":        func(p *RequestParts) { p.Time = "1795539600.5" },
		"time-overflow":     func(p *RequestParts) { p.Time = "99999999999999999999" },
		"nonce-short":       func(p *RequestParts) { p.Nonce = EncodeB64U(make([]byte, 15)) },
		"nonce-padding":     func(p *RequestParts) { p.Nonce = EncodeB64U(make([]byte, 16)) + "==" },
		"sha-uppercase":     func(p *RequestParts) { p.ContentSHA256 = strings.ToUpper(ContentSHA256(nil)) },
		"sha-short":         func(p *RequestParts) { p.ContentSHA256 = "abc" },
		"sha-unsigned-case": func(p *RequestParts) { p.ContentSHA256 = "unsigned" },
	}
	for name, m := range mutate {
		p := good
		m(&p)
		if _, err := CanonicalRequest(p); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: error = %v, want ErrMalformed", name, err)
		}
	}
}

func TestSignatureCoversEveryField(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	base := RequestParts{
		Method: "POST", Path: "/api/v1/uploads", RawQuery: "a=1", Time: "1795539600",
		Nonce: EncodeB64U(make([]byte, 16)), ContentSHA256: ContentSHA256([]byte("{}")),
	}
	canonical, err := CanonicalRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	sig := Sign(key, canonical)
	variants := []func(*RequestParts){
		func(p *RequestParts) { p.Method = "PUT" },
		func(p *RequestParts) { p.Path = "/api/v1/uploadz" },
		func(p *RequestParts) { p.RawQuery = "a=2" },
		func(p *RequestParts) { p.Time = "1795539601" },
		func(p *RequestParts) { p.Nonce = EncodeB64U(bytes.Repeat([]byte{1}, 16)) },
		func(p *RequestParts) { p.ContentSHA256 = ContentSHA256([]byte("{ }")) },
	}
	for i, v := range variants {
		p := base
		v(&p)
		c, err := CanonicalRequest(p)
		if err != nil {
			t.Fatal(err)
		}
		if Verify(key, c, sig) {
			t.Errorf("variant %d: signature still valid", i)
		}
	}
	if Verify(bytes.Repeat([]byte{8}, 32), canonical, sig) {
		t.Error("wrong key accepted")
	}
}

func FuzzCanonicalQuery(f *testing.F) {
	f.Add("a=1&b=%20&c+d=%2B")
	f.Fuzz(func(t *testing.T, q string) {
		c, err := CanonicalQuery(q)
		if err != nil {
			return
		}
		// Canonicalization is idempotent.
		again, err := CanonicalQuery(c)
		if err != nil || again != c {
			t.Fatalf("not idempotent: %q -> %q -> %q (%v)", q, c, again, err)
		}
	})
}

func TestHasDuplicateQueryKey(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  bool
		err   bool
	}{
		{"", false, false},
		{"a=1&b=2", false, false},
		{"a=1&&b=2", false, false},
		{"a=1&a=2", true, false},
		{"a&a", true, false},
		{"a=1&%61=2", true, false},
		{"a+b=1&a%20b=2", true, false},
		{"a=1&A=2", false, false}, // keys are case-sensitive
		{"a=%zz", false, false},   // values are not decoded here
		{"%zz=1", false, true},
	} {
		got, err := HasDuplicateQueryKey(tc.query)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("HasDuplicateQueryKey(%q) = %v, %v; want %v, err=%v", tc.query, got, err, tc.want, tc.err)
		}
	}
}

func TestLogString(t *testing.T) {
	if LogString("abc", 5) != "abc" {
		t.Fatal("short string changed")
	}
	got := LogString(strings.Repeat("a", 600), MaxLoggedPath)
	if !strings.HasPrefix(got, strings.Repeat("a", 512)+"...[88 bytes truncated]") || len(got) > 540 {
		t.Fatalf("got %q", got)
	}
	// Never cut inside a rune.
	got = LogString("ééé", 3)
	if got != "é...[4 bytes truncated]" {
		t.Fatalf("got %q", got)
	}
	if LogClient("x\nfake log line") != "" || LogClient(testClientA) != testClientA {
		t.Fatal("LogClient")
	}
}
