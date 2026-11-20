// Command gen writes the shared test vectors of protocol/test-vectors/.
//
// The output is deterministic: every input (keys, nonces, ids, times) is
// fixed below, and random-looking bytes are derived from fixed seeds. Run it
// from the repository root:
//
//	go run ./protocol/test-vectors/gen -out protocol/test-vectors
//
// (or "make vectors", which also copies the files to the Swift test
// resources). A Go test fails if the committed files differ from its output;
// "-check" does the same from the command line and checks the files parse.
//
// Expected canonical query strings are written by hand below and checked
// against the implementation, so the vectors do not merely echo the code.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
)

func main() {
	out := flag.String("out", "protocol/test-vectors", "output directory")
	check := flag.Bool("check", false, "only check that the files in -out are valid JSON and up to date")
	flag.Parse()
	files, err := Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	if *check {
		if err := Check(*out, files); err != nil {
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
		fmt.Println("test vectors are valid and up to date")
		return
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(*out, name), files[name], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", filepath.Join(*out, name))
	}
}

// Generate returns the content of each vector file, by file name.
func Generate() (map[string][]byte, error) {
	files := map[string]any{}
	var err error
	if files["pairing.json"], err = pairingVectors(); err != nil {
		return nil, err
	}
	if files["signing.json"], err = signingVectors(); err != nil {
		return nil, err
	}
	if files["ids.json"], err = idVectors(); err != nil {
		return nil, err
	}
	if files["tls.json"], err = tlsVectors(); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for name, v := range files {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = buf.Bytes()
	}
	return out, nil
}

// Check verifies that dir holds exactly the generated files, as valid JSON.
func Check(dir string, files map[string][]byte) error {
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !json.Valid(got) {
			return fmt.Errorf("%s is not valid JSON", name)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("%s is out of date: run make vectors", name)
		}
	}
	return nil
}

// seeded returns n bytes derived from a fixed seed string.
func seeded(seed string, n int) []byte {
	var out []byte
	for i := 0; len(out) < n; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("reminislink-test-vectors|%s|%d", seed, i)))
		out = append(out, sum[:]...)
	}
	return out[:n]
}

// ---------------------------------------------------------------- pairing

type pairingFile struct {
	Description string        `json:"description"`
	Cases       []pairingCase `json:"cases"`
	Links       []pairingLink `json:"links"`
}

type pairingCase struct {
	Name               string `json:"name"`
	Secret             string `json:"secret"`
	SecretHex          string `json:"secretHex"`
	DeviceID           string `json:"deviceId"`
	ClientID           string `json:"clientId"`
	ClientSeed         string `json:"clientSeed"`
	ClientPublicKey    string `json:"clientPublicKey"`
	ClientNonce        string `json:"clientNonce"`
	DeviceNonce        string `json:"deviceNonce"`
	ClientProofMessage string `json:"clientProofMessage"`
	ClientProof        string `json:"clientProof"`
	DeviceProofMessage string `json:"deviceProofMessage"`
	DeviceProof        string `json:"deviceProof"`
}

type pairingLink struct {
	Name       string `json:"name"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Secret     string `json:"secret"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Pin        string `json:"pin"`
	Link       string `json:"link"`
}

// ed25519Key returns the Ed25519 key of a fixed seed.
func ed25519Key(seed string) (ed25519.PrivateKey, []byte) {
	s := seeded(seed, ed25519.SeedSize)
	return ed25519.NewKeyFromSeed(s), s
}

func pairingVectors() (pairingFile, error) {
	type input struct {
		name, deviceID, clientID string
	}
	inputs := []input{
		{"pair-1", "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"},
		{"pair-2", "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "c0ffee00-1234-4abc-9def-000000000001"},
		{"pair-3", "00000000-0000-4000-8000-000000000000", "ffffffff-ffff-4fff-bfff-ffffffffffff"},
	}
	f := pairingFile{
		Description: "Pairing v2 (docs/specs/03-protocole.md §3, M13). Binary values are base64url without padding unless the field name ends in Hex. secret is the 32-byte QR secret (the HMAC key). clientSeed is the 32-byte Ed25519 seed (private key) of the client and clientPublicKey its public key, sent as is in POST /pair. clientProof = b64u(HMAC-SHA256(secret, clientProofMessage)); deviceProof = b64u(HMAC-SHA256(secret, deviceProofMessage)). links: pairing links (QR code) built from their fields; pin is b64u(SHA-256(SPKI DER)) of the console certificate (see tls.json).",
	}
	for _, in := range inputs {
		secret := seeded(in.name+"/secret", auth.SecretSize)
		priv, seed := ed25519Key(in.name + "/client-key")
		pub := auth.EncodeB64U(priv.Public().(ed25519.PublicKey))
		c := pairingCase{
			Name: in.name, Secret: auth.EncodeB64U(secret), SecretHex: hex.EncodeToString(secret),
			DeviceID: in.deviceID, ClientID: in.clientID,
			ClientSeed: auth.EncodeB64U(seed), ClientPublicKey: pub,
			ClientNonce: auth.EncodeB64U(seeded(in.name+"/clientNonce", auth.PairingNonceSize)),
			DeviceNonce: auth.EncodeB64U(seeded(in.name+"/deviceNonce", auth.PairingNonceSize)),
		}
		c.ClientProofMessage = auth.PairClientMessage(c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce)
		if want := "RL2-PAIR-CLIENT|" + c.DeviceID + "|" + c.ClientID + "|" + c.ClientPublicKey + "|" + c.ClientNonce; c.ClientProofMessage != want {
			return f, fmt.Errorf("%s: client message %q", in.name, c.ClientProofMessage)
		}
		c.ClientProof = auth.PairClientProof(secret, c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce)
		c.DeviceProofMessage = auth.PairDeviceMessage(c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce, c.DeviceNonce)
		if want := "RL2-PAIR-DEVICE|" + c.DeviceID + "|" + c.ClientID + "|" + c.ClientPublicKey + "|" + c.ClientNonce + "|" + c.DeviceNonce; c.DeviceProofMessage != want {
			return f, fmt.Errorf("%s: device message %q", in.name, c.DeviceProofMessage)
		}
		c.DeviceProof = auth.PairDeviceProof(secret, c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce, c.DeviceNonce)
		// Independent check of the HMAC.
		if hmacB64U(secret, c.ClientProofMessage) != c.ClientProof || hmacB64U(secret, c.DeviceProofMessage) != c.DeviceProof {
			return f, fmt.Errorf("%s: proof does not match HMAC-SHA256", in.name)
		}
		f.Cases = append(f.Cases, c)
	}
	_, _, pin, err := tlsKey()
	if err != nil {
		return f, err
	}
	links := []struct {
		name, deviceID, deviceName, host string
		port                             int
		want                             string // expected, written by hand except secret and pin
	}{
		{"ascii-name", "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "Brick", "192.168.1.42", 47821,
			"reminislink://pair?v=2&id=7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10&n=Brick&s=%s&h=192.168.1.42&p=47821&k=%s"},
		{"name-with-spaces-and-accents", "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "Brick de Théo & Zoé", "10.0.0.7", 47823,
			"reminislink://pair?v=2&id=7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10&n=Brick%%20de%%20Th%%C3%%A9o%%20%%26%%20Zo%%C3%%A9&s=%s&h=10.0.0.7&p=47823&k=%s"},
	}
	for _, l := range links {
		secret := seeded("link/"+l.name+"/secret", auth.SecretSize)
		link := auth.PairingLink(l.deviceID, l.deviceName, secret, l.host, l.port, pin)
		if want := fmt.Sprintf(l.want, auth.EncodeB64U(secret), pin); link != want {
			return f, fmt.Errorf("link %s = %q, want %q", l.name, link, want)
		}
		f.Links = append(f.Links, pairingLink{Name: l.name, DeviceID: l.deviceID, DeviceName: l.deviceName,
			Secret: auth.EncodeB64U(secret), Host: l.host, Port: l.port, Pin: pin, Link: link})
	}
	return f, nil
}

func hmacB64U(key []byte, msg string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// ---------------------------------------------------------------- signing

type signingFile struct {
	Description    string         `json:"description"`
	Cases          []signingCase  `json:"cases"`
	InvalidQueries []invalidQuery `json:"invalidQueries"`
}

type signingCase struct {
	Name             string            `json:"name"`
	Seed             string            `json:"seed"`
	PublicKey        string            `json:"publicKey"`
	ClientID         string            `json:"clientId"`
	Method           string            `json:"method"`
	Path             string            `json:"path"`
	Query            string            `json:"query"`
	Time             string            `json:"time"`
	Nonce            string            `json:"nonce"`
	Body             string            `json:"body"`
	ContentSHA256    string            `json:"contentSha256"`
	CanonicalQuery   string            `json:"canonicalQuery"`
	CanonicalRequest string            `json:"canonicalRequest"`
	Signature        string            `json:"signature"`
	Headers          map[string]string `json:"headers"`
}

type invalidQuery struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

func signingVectors() (signingFile, error) {
	key1, _ := ed25519Key("signing/key-1")
	key2, _ := ed25519Key("signing/key-2")
	const client1 = "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	const client2 = "9e8d7c6b-5a49-4382-9170-6f5e4d3c2b1a"
	gba := library.EncodeID("Game Boy Advance (GBA)")
	game := library.EncodeID("Game Boy Advance (GBA)/Sky Garden Quest.gba")
	// A game id whose encoding contains both "-" and "_".
	odd := library.EncodeID("Nintendo DS (NDS)/Kôdo ÿ Ëxtra ÿÿ~.nds")
	if !strings.ContainsAny(odd, "-") || !strings.ContainsAny(odd, "_") {
		return signingFile{}, fmt.Errorf("b64url id %q lacks - or _", odd)
	}

	type input struct {
		name           string
		key            ed25519.PrivateKey
		clientID       string
		method, path   string
		query          string
		time           string
		body           string
		unsigned       bool
		canonicalQuery string // expected, written by hand
	}
	inputs := []input{
		{name: "get-no-query", key: key1, clientID: client1, method: "GET", path: "/api/v1/systems",
			time: "1795539600"},
		{name: "get-query-sorted-and-encoded", key: key1, clientID: client1, method: "GET",
			path:           "/api/v1/systems/" + gba + "/games",
			query:          "q=Caf%c3%a9+R%c3%aaverie&path=a/b&plus=1%2B1&z=%7E~&a=2&a=10&flag",
			canonicalQuery: "a=10&a=2&flag=&path=a%2Fb&plus=1%2B1&q=Caf%C3%A9%20R%C3%AAverie&z=~~",
			time:           "1795539601"},
		{name: "get-query-raw-utf8-and-reserved", key: key2, clientID: client2, method: "GET",
			path:           "/api/v1/systems/" + gba + "/games",
			query:          "sp%20ace=x%2Fy*&name=Café%20🎮&&empty=&folderId=" + library.EncodeID("Game Boy Advance (GBA)/Hacks"),
			canonicalQuery: "empty=&folderId=" + library.EncodeID("Game Boy Advance (GBA)/Hacks") + "&name=Caf%C3%A9%20%F0%9F%8E%AE&sp%20ace=x%2Fy%2A",
			time:           "1795539602"},
		{name: "get-single-param", key: key1, clientID: client1, method: "GET", path: "/api/v1/sd/files",
			query: "type=cht", canonicalQuery: "type=cht", time: "1795539603"},
		{name: "post-json", key: key1, clientID: client1, method: "POST",
			path: "/api/v1/games/" + game + "/save/backups",
			body: `{"label":"Avant le boss final"}`, time: "1795539604"},
		{name: "post-json-utf8-body", key: key2, clientID: client2, method: "POST",
			path: "/api/v1/games/" + game + "/cheats/import",
			body: `{"source":{"type":"inline","text":"cheats = 1\n\ncheat0_desc = \"Argent illimité 💰\"\n"},"mode":"merge"}`,
			time: "1795539605"},
		{name: "delete-empty-body", key: key1, clientID: client1, method: "DELETE", path: "/api/v1/clients/me",
			time: "1795539606"},
		{name: "put-upload-chunk-unsigned", key: key1, clientID: client1, method: "PUT",
			path: "/api/v1/uploads/up_01JD3X5V7Q9W", body: "raw chunk bytes", unsigned: true, time: "1795539607"},
		{name: "get-path-with-b64url-id", key: key2, clientID: client2, method: "GET",
			path: "/api/v1/games/" + odd, time: "1795539608"},
		{name: "lowercase-method-normalized", key: key1, clientID: client1, method: "delete",
			path: "/api/v1/trash/tr_20261118T170000Z_a1b2", time: "1795539609"},
	}

	f := signingFile{
		Description: "Request signing RL2-ED25519 (docs/specs/03-protocole.md §4, M13). seed is the 32-byte Ed25519 private key (seed) of the client and publicKey its 32-byte public key; nonce and signature are base64url without padding. query is the raw query as sent (without '?'); body is the UTF-8 request body. method is as given by the caller and is uppercased in the canonical request and the request line. signature = b64u(Ed25519(seed, canonicalRequest)), 64 bytes: Ed25519 (RFC 8032) is deterministic, but a signer that adds randomness (CryptoKit) produces other valid signatures, so tests compare signatures by verifying them with publicKey. headers lists the signature headers to send.",
	}
	for _, in := range inputs {
		nonce := auth.EncodeB64U(seeded("signing/"+in.name+"/nonce", auth.RequestNonceSize))
		content := auth.ContentSHA256([]byte(in.body))
		if in.unsigned {
			content = auth.UnsignedPayload
		}
		cq, err := auth.CanonicalQuery(in.query)
		if err != nil {
			return f, fmt.Errorf("%s: %w", in.name, err)
		}
		if cq != in.canonicalQuery {
			return f, fmt.Errorf("%s: canonical query %q, expected %q", in.name, cq, in.canonicalQuery)
		}
		canonical, err := auth.CanonicalRequestRL2(auth.RequestParts{
			Method: in.method, Path: in.path, RawQuery: in.query,
			Time: in.time, Nonce: nonce, ContentSHA256: content,
		})
		if err != nil {
			return f, fmt.Errorf("%s: %w", in.name, err)
		}
		if want := strings.Join([]string{"RL2-ED25519", strings.ToUpper(in.method), in.path, in.canonicalQuery, in.time, nonce, content}, "\n"); canonical != want {
			return f, fmt.Errorf("%s: canonical request %q, expected %q", in.name, canonical, want)
		}
		sig := auth.SignRL2(in.key, canonical)
		pub := in.key.Public().(ed25519.PublicKey)
		if !auth.VerifyRL2(pub, canonical, sig) {
			return f, fmt.Errorf("%s: signature does not verify", in.name)
		}
		f.Cases = append(f.Cases, signingCase{
			Name: in.name, Seed: auth.EncodeB64U(in.key.Seed()), PublicKey: auth.EncodeB64U(pub),
			ClientID: in.clientID,
			Method:   in.method, Path: in.path, Query: in.query, Time: in.time, Nonce: nonce,
			Body: in.body, ContentSHA256: content, CanonicalQuery: cq,
			CanonicalRequest: canonical, Signature: sig,
			Headers: map[string]string{
				auth.HeaderClient:        in.clientID,
				auth.HeaderTime:          in.time,
				auth.HeaderNonce:         nonce,
				auth.HeaderContentSHA256: content,
				auth.HeaderSignature:     sig,
			},
		})
	}
	for _, q := range []invalidQuery{
		{"percent-not-hex", "a=%zz"},
		{"percent-truncated", "a=1%4"},
		{"percent-alone", "a=%"},
		{"percent-in-key", "%G1=1"},
	} {
		if _, err := auth.CanonicalQuery(q.Query); err == nil {
			return f, fmt.Errorf("invalid query %q accepted", q.Query)
		}
		f.InvalidQueries = append(f.InvalidQueries, q)
	}
	return f, nil
}

// ---------------------------------------------------------------- tls

type tlsFile struct {
	Description      string `json:"description"`
	PrivateKeyHex    string `json:"privateKeyHex"`
	PublicKeyX963Hex string `json:"publicKeyX963Hex"`
	SPKIDERHex       string `json:"spkiDerHex"`
	CertificatePEM   string `json:"certificatePem"`
	Pin              string `json:"pin"`
	PinStdBase64     string `json:"pinStdBase64"`
}

// tlsCertificatePEM was made once from the key of tlsKey (self-signed,
// CN = a device id, 20 years): an ECDSA signature is randomized, so the
// certificate is a fixed input, checked against the key below.
const tlsCertificatePEM = `-----BEGIN CERTIFICATE-----
MIIBgjCCASigAwIBAgIBATAKBggqhkjOPQQDAjAvMS0wKwYDVQQDEyQ3ZjNhOWMy
ZS0xYjRkLTRjNTUtOWE1MS0yZjBmOWU3ZDNhMTAwHhcNMjYxMTIwMDAwMDAwWhcN
NDYxMTIwMDAwMDAwWjAvMS0wKwYDVQQDEyQ3ZjNhOWMyZS0xYjRkLTRjNTUtOWE1
MS0yZjBmOWU3ZDNhMTAwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAATMq02qyXBQ
iFE9ganhYlkd0c0ybTAcPdsE+YGtHDWYngS0yS1JeYMS35LgzR7qiqJfEe26fdkq
VFOAGykR+k89ozUwMzAOBgNVHQ8BAf8EBAMCB4AwEwYDVR0lBAwwCgYIKwYBBQUH
AwEwDAYDVR0TAQH/BAIwADAKBggqhkjOPQQDAgNIADBFAiEAmEhpLZpeBA9iPYEp
hoBIfhFIcv1Xlk8RZI2Fk4zC3iYCIE9necCx3oipmTaGRdfHBt6Cv73U1pSxTgrw
3rNIS9IV
-----END CERTIFICATE-----
`

// tlsKey returns the fixed P-256 key of the TLS vectors, the DER of its
// SubjectPublicKeyInfo and its pin.
func tlsKey() (*ecdsa.PrivateKey, []byte, string, error) {
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), seeded("tls/key-1", 32))
	if err != nil {
		return nil, nil, "", err
	}
	spki, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, "", err
	}
	return priv, spki, auth.CertificatePin(spki), nil
}

func tlsVectors() (tlsFile, error) {
	priv, spki, pin, err := tlsKey()
	if err != nil {
		return tlsFile{}, err
	}
	block, _ := pem.Decode([]byte(tlsCertificatePEM))
	if block == nil {
		return tlsFile{}, fmt.Errorf("tls: bad certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return tlsFile{}, err
	}
	if !bytes.Equal(cert.RawSubjectPublicKeyInfo, spki) {
		return tlsFile{}, fmt.Errorf("tls: certificate key differs from the vector key")
	}
	sum := sha256.Sum256(spki)
	if base64.RawURLEncoding.EncodeToString(sum[:]) != pin {
		return tlsFile{}, fmt.Errorf("tls: pin mismatch")
	}
	raw, err := priv.Bytes()
	if err != nil {
		return tlsFile{}, err
	}
	x963, err := priv.PublicKey.Bytes()
	if err != nil {
		return tlsFile{}, err
	}
	return tlsFile{
		Description:      "Certificate pin (docs/specs/03-protocole.md §1 and §3, M13). The console certificate is self-signed with an ECDSA P-256 key. pin = b64u(SHA-256(DER of the certificate's SubjectPublicKeyInfo)), the \"k\" of the pairing link; pinStdBase64 is the same hash in standard base64 (curl --pinnedpubkey sha256//...). publicKeyX963Hex is the uncompressed point (04 || X || Y), what SecKeyCopyExternalRepresentation returns; spkiDerHex is that point behind the fixed P-256 SubjectPublicKeyInfo header. privateKeyHex is the scalar (tests only).",
		PrivateKeyHex:    hex.EncodeToString(raw),
		PublicKeyX963Hex: hex.EncodeToString(x963),
		SPKIDERHex:       hex.EncodeToString(spki),
		CertificatePEM:   tlsCertificatePEM,
		Pin:              pin,
		PinStdBase64:     base64.StdEncoding.EncodeToString(sum[:]),
	}, nil
}

// ---------------------------------------------------------------- ids

type idsFile struct {
	Description string      `json:"description"`
	Valid       []validID   `json:"valid"`
	Invalid     []invalidID `json:"invalid"`
}

type validID struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	PathUTF8Hex string `json:"pathUtf8Hex"`
	ID          string `json:"id"`
}

type invalidID struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func idVectors() (idsFile, error) {
	f := idsFile{
		Description: "Identifiers (docs/specs/03-protocole.md §5): id = base64url without padding of the UTF-8 bytes of the path relative to Roms/ ('/' separator). No Unicode normalization: pathUtf8Hex gives the exact bytes (the NFC and NFD spellings differ). invalid lists ids the pak must reject with 400 invalid_id; reason is informative.",
	}
	valid := []struct{ name, path string }{
		{"system-folder", "Game Boy Advance (GBA)"},
		{"system-with-sort-prefix", "01) Game Boy (GB)"},
		{"game-at-system-root", "Game Boy Advance (GBA)/Sky Garden Quest.gba"},
		{"game-in-subfolder", "Game Boy Advance (GBA)/Hacks/Sky Garden Quest DX.gba"},
		{"deep-subfolders", "Super Nintendo (SFC)/Homebrew/2024/Jam/Tiny Quest (v1.2).sfc"},
		{"accents-nfc", "Super Nintendo (SFC)/La Légende du Héros (Français).sfc"},
		{"accents-nfd", "Super Nintendo (SFC)/La Légende du Héros (Français).sfc"},
		{"spaces-double-and-trailing", "Arcade (FBN)/Street  Brawler .zip"},
		{"brackets-and-parentheses", "PlayStation (PS)/Space Odyssey (Disc 1) [!] (Rev 1).cue"},
		{"folder-m3u", "PlayStation (PS)/Night Trip (Europe)/Night Trip (Europe).m3u"},
		{"emoji", "Nintendo (FC)/\U0001F3AE Homebrew/Rocket \U0001F680.nes"},
		{"japanese", "Game Boy (GB)/ゲームボーイの冒険.gb"},
		{"ascii-punctuation", "Game Boy (GB)/Tic-Tac-Toe #2 + 100% & More, Vol.1 ~ 'Final'.gb"},
		{"b64url-dash-and-underscore", "Nintendo DS (NDS)/Kôdo ÿ Ëxtra ÿÿ~.nds"},
		{"dots-inside-names", "Game Boy (GB)/...Dots.../a..b.gb"},
	}
	for _, v := range valid {
		id := library.EncodeID(v.path)
		back, err := library.DecodeID(id)
		if err != nil || back != v.path {
			return f, fmt.Errorf("%s: round trip failed: %v", v.name, err)
		}
		f.Valid = append(f.Valid, validID{Name: v.name, Path: v.path, PathUTF8Hex: hex.EncodeToString([]byte(v.path)), ID: id})
	}
	enc := library.EncodeID
	invalid := []invalidID{
		{"empty", "", "empty id"},
		{"dot-dot", enc(".."), "parent component"},
		{"dot-dot-prefix", enc("../Saves/GBA/x.sav"), "parent component"},
		{"dot-dot-middle", enc("Game Boy Advance (GBA)/../../etc/passwd"), "parent component"},
		{"dot-dot-suffix", enc("Game Boy Advance (GBA)/.."), "parent component"},
		{"absolute", enc("/etc/passwd"), "absolute path"},
		{"absolute-sdcard", enc("/mnt/SDCARD/Roms/Game Boy Advance (GBA)/x.gba"), "absolute path"},
		{"nul-byte", enc("Game Boy Advance (GBA)/x.gba\x00.png"), "NUL byte"},
		{"control-char", enc("Game Boy Advance (GBA)/x\n.gba"), "control character"},
		{"backslash-traversal", enc("Game Boy Advance (GBA)\\..\\..\\x"), "backslash"},
		{"invalid-utf8", enc("Game Boy (GB)/\xff\xfe.gb"), "invalid UTF-8"},
		{"double-slash", enc("Game Boy (GB)//x.gb"), "non-canonical path"},
		{"dot-component", enc("./Game Boy (GB)/x.gb"), "dot component"},
		{"single-dot", enc("."), "dot component"},
		{"trailing-slash", enc("Game Boy (GB)/"), "non-canonical path"},
		{"base64-standard-alphabet", "R2FtZSBCb3kgKEdCKS9+P2E/", "invalid base64url"},
		{"base64-padding", "R2FtZQ==", "invalid base64url"},
		{"base64-bad-length", "R2FtZ", "invalid base64url"},
		{"base64-non-canonical-bits", "R2FtZR", "invalid base64url"},
		{"base64-whitespace", "R2Ft ZQ", "invalid base64url"},
		{"base64-newline", "R2Ft\nZQ", "invalid base64url"},
		{"base64-carriage-return", "\rR2FtZQ", "invalid base64url"},
		{"base64-percent-encoded", "R2FtZQ%3D%3D", "invalid base64url"},
	}
	for _, v := range invalid {
		if _, err := library.DecodeID(v.ID); err == nil {
			return f, fmt.Errorf("%s: invalid id %q accepted", v.Name, v.ID)
		}
	}
	f.Invalid = invalid
	return f, nil
}
