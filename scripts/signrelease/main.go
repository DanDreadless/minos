// Command signrelease signs a release's checksums.txt for the one-click
// updater (see internal/release). The release workflow runs it with the
// private seed in MINOS_SIGNING_KEY; it never ships in the minos binary.
//
//	go run ./scripts/signrelease -tag v1.2.3 dist/checksums.txt   # writes dist/checksums.txt.sig
//	go run ./scripts/signrelease -keygen seed.txt                 # new key: seed to the file, public key printed
//	go run ./scripts/signrelease -verify -tag v1.2.3 dir/checksums.txt   # check a published signature
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"minos/internal/release"
)

func main() {
	tag := flag.String("tag", "", "release tag being signed, e.g. v1.2.3")
	keygen := flag.Bool("keygen", false, "generate a new key: write the private seed to the named file and print the public key")
	verify := flag.Bool("verify", false, "verify checksums.txt.sig beside the named checksums.txt against release.PublicKey")
	flag.Parse()
	if flag.NArg() != 1 {
		fail("usage: signrelease -tag vX.Y.Z checksums.txt | signrelease -keygen seedfile")
	}
	if *keygen {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fail(err.Error())
		}
		seed := base64.StdEncoding.EncodeToString(priv.Seed())
		if err := os.WriteFile(flag.Arg(0), []byte(seed+"\n"), 0o600); err != nil {
			fail(err.Error())
		}
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
		return
	}
	if *tag == "" {
		fail("-tag is required")
	}
	if *verify {
		sums, err := os.ReadFile(flag.Arg(0))
		if err != nil {
			fail(err.Error())
		}
		sig, err := os.ReadFile(filepath.Join(filepath.Dir(flag.Arg(0)), release.SignatureFile))
		if err != nil {
			fail(err.Error())
		}
		if err := release.Verify(release.PublicKey, *tag, sums, sig); err != nil {
			fail(err.Error())
		}
		fmt.Println("signature ok", *tag)
		return
	}
	seed := os.Getenv("MINOS_SIGNING_KEY")
	if seed == "" {
		fail("MINOS_SIGNING_KEY is not set — releases must be signed")
	}
	sums, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fail(err.Error())
	}
	sig, err := release.Sign(seed, *tag, sums)
	if err != nil {
		fail(err.Error())
	}
	// Prove the signature verifies against the key compiled into Minos, so a
	// rotated secret without a matching PublicKey fails here, not on users.
	if err := release.Verify(release.PublicKey, *tag, sums, []byte(sig)); err != nil {
		fail("the signing key does not match release.PublicKey: " + err.Error())
	}
	out := filepath.Join(filepath.Dir(flag.Arg(0)), release.SignatureFile)
	if err := os.WriteFile(out, []byte(sig), 0o644); err != nil {
		fail(err.Error())
	}
	fmt.Println("signed", out)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "signrelease:", msg)
	os.Exit(1)
}
