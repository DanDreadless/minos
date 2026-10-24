package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"minos/internal/release"
	"minos/internal/updates"
)

// verifyRelease is the updater's trust check, run by deploy/minos-update.sh
// with the binary that is installed now — never the one being installed,
// which proves nothing about itself. It checks the release signature against
// the key compiled into this binary, the archive's checksum, and that the
// release is newer than this one: a downgrade is refused, because an older
// release may carry a fixed vulnerability.
func verifyRelease(args []string) error {
	fs := flag.NewFlagSet("verify-release", flag.ExitOnError)
	tag := fs.String("tag", "", "release tag, e.g. v1.2.3")
	dir := fs.String("dir", "", "directory holding checksums.txt, checksums.txt.sig and the archive")
	file := fs.String("file", "", "archive file name inside -dir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tag == "" || *dir == "" || *file == "" || strings.ContainsAny(*file, `/\`) {
		return errors.New("usage: minos verify-release -tag vX.Y.Z -dir DIR -file ARCHIVE")
	}
	if !updates.IsNewer(strings.TrimPrefix(*tag, "v"), version) {
		return fmt.Errorf("%s is not newer than the installed %s — refusing to downgrade", *tag, version)
	}
	sums, err := os.ReadFile(filepath.Join(*dir, "checksums.txt"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(*dir, release.SignatureFile))
	if err != nil {
		return fmt.Errorf("the release has no signature: %w", err)
	}
	if err := release.VerifyFile(release.PublicKey, *tag, sums, sig, filepath.Join(*dir, *file), *file); err != nil {
		return err
	}
	fmt.Println("verified", *file, *tag)
	return nil
}
