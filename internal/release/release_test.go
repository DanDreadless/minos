package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) (seed, pub string) {
	t.Helper()
	p, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(p)
}

func TestSignVerify(t *testing.T) {
	seed, pub := testKey(t)
	sums := []byte("abc  minos_1.2.3_linux_arm64.tar.gz\n")
	sig, err := Sign(seed, "v1.2.3", sums)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(pub, "v1.2.3", sums, []byte(sig)); err != nil {
		t.Fatalf("a good signature was refused: %v", err)
	}
	// The tag is bound in: the same checksums cannot pass as another version.
	if Verify(pub, "v9.9.9", sums, []byte(sig)) == nil {
		t.Error("a signature verified for a different tag")
	}
	if Verify(pub, "v1.2.3", append(sums, 'x'), []byte(sig)) == nil {
		t.Error("tampered checksums verified")
	}
	_, other := testKey(t)
	if Verify(other, "v1.2.3", sums, []byte(sig)) == nil {
		t.Error("a signature verified under another key")
	}
	for _, bad := range []string{"", "!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if Verify(pub, "v1.2.3", sums, []byte(bad)) == nil {
			t.Errorf("malformed signature %q verified", bad)
		}
	}
	if _, err := Sign("nope", "v1", sums); err == nil {
		t.Error("a malformed seed signed")
	}
}

func TestPublicKeyIsValid(t *testing.T) {
	k, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(k) != ed25519.PublicKeySize {
		t.Fatalf("PublicKey is not a base64 Ed25519 key: %v", err)
	}
}

func TestVerifyFile(t *testing.T) {
	seed, pub := testKey(t)
	dir := t.TempDir()
	archive := filepath.Join(dir, "minos_1.2.3_linux_arm64.tar.gz")
	body := []byte("pretend archive")
	if err := os.WriteFile(archive, body, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	sums := []byte(hex.EncodeToString(h[:]) + "  ./minos_1.2.3_linux_arm64.tar.gz\n" + strings.Repeat("0", 64) + "  other.zip\n")
	sig, _ := Sign(seed, "v1.2.3", sums)
	if err := VerifyFile(pub, "v1.2.3", sums, []byte(sig), archive, filepath.Base(archive)); err != nil {
		t.Fatalf("a good archive was refused: %v", err)
	}
	if err := os.WriteFile(archive, []byte("swapped archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if VerifyFile(pub, "v1.2.3", sums, []byte(sig), archive, filepath.Base(archive)) == nil {
		t.Error("a swapped archive verified")
	}
	if got, err := ChecksumFor([]byte(strings.Repeat("a", 64)+" *bin.tar.gz\r\n"), "bin.tar.gz"); err != nil || got != strings.Repeat("a", 64) {
		t.Errorf("binary-mode line: %q, %v", got, err)
	}
	if _, err := ChecksumFor(sums, "missing.tar.gz"); err == nil {
		t.Error("an unlisted file had a checksum")
	}
}
