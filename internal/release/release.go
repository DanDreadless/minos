// Package release verifies that a downloaded release is an official one:
// the release workflow signs checksums.txt with an Ed25519 key held only as
// a GitHub Actions secret, and every Minos binary carries the public half.
// The checksums alone prove an archive arrived intact, not who built it —
// they come from the same place as the archive. Stdlib only, so the
// one-click updater's trust root has no dependencies at all.
package release

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// PublicKey is the release-signing key (base64 Ed25519). Replacing it
// means every older binary refuses the new releases, so it changes only if
// the private key is lost or leaked. A var only so the updater's container
// test can build binaries trusting a throwaway key (-ldflags -X); release
// builds never set it.
var PublicKey = "tFGC9WkydgPUbATkBr4P1MCEhuig4+qepBpWrXCKXvM="

// SignatureFile is the asset published beside checksums.txt.
const SignatureFile = "checksums.txt.sig"

// Message is what gets signed: the tag is bound in, so a signature on an
// old release's checksums can never pass for a newer version.
func Message(tag string, checksums []byte) []byte {
	var b bytes.Buffer
	b.WriteString("minos-release-v1\n")
	b.WriteString(tag)
	b.WriteString("\n")
	b.Write(checksums)
	return b.Bytes()
}

// Sign returns the base64 signature of checksums for tag, given the
// base64 32-byte private seed.
func Sign(seed, tag string, checksums []byte) (string, error) {
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(s) != ed25519.SeedSize {
		return "", errors.New("the signing key must be a base64 32-byte Ed25519 seed")
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(s), Message(tag, checksums))
	return base64.StdEncoding.EncodeToString(sig) + "\n", nil
}

// Verify checks sig (base64, as published) over checksums for tag.
func Verify(pub, tag string, checksums, sig []byte) error {
	k, err := base64.StdEncoding.DecodeString(pub)
	if err != nil || len(k) != ed25519.PublicKeySize {
		return errors.New("bad public key")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return errors.New("the signature is malformed")
	}
	if !ed25519.Verify(k, Message(tag, checksums), s) {
		return errors.New("the signature does not match — this is not an official release")
	}
	return nil
}

// ChecksumFor returns the SHA-256 recorded in checksums for name, in
// sha256sum's format: "<hash>  <name>", or "<hash> *<name>" in binary mode.
// An optional "./" prefix is tolerated, like install.sh does.
func ChecksumFor(checksums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if len(line) < 66 || (line[64:66] != "  " && line[64:66] != " *") {
			continue
		}
		sum, file := line[:64], line[66:]
		if strings.TrimPrefix(file, "./") == name {
			return strings.ToLower(sum), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in checksums.txt", name)
}

// VerifyFile checks the signature, then that the file at path has the
// checksum recorded for its name.
func VerifyFile(pub, tag string, checksums, sig []byte, path, name string) error {
	if err := Verify(pub, tag, checksums, sig); err != nil {
		return err
	}
	want, err := ChecksumFor(checksums, name)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s does not match its checksum", name)
	}
	return nil
}
