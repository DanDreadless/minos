//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestServeShutsDownGracefullyOnSIGTERM: systemd and Docker stop services
// with SIGTERM. serve must take its graceful path (flush the query log) and
// return nil. Without SIGTERM in shutdownSignals the default handler kills
// this test binary outright, so the test cannot pass by accident.
func TestServeShutsDownGracefullyOnSIGTERM(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "minos.yaml")
	yml := "dns:\n  listen: 127.0.0.1:0\n" +
		"lists:\n  sources: []\n" +
		"querylog:\n  db_path: " + filepath.ToSlash(filepath.Join(dir, "minos.db")) + "\n" +
		"api:\n  listen: 127.0.0.1:0\n"
	if err := os.WriteFile(cfg, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serve([]string{"-config", cfg, "-log-level", "error"}) }()
	time.Sleep(500 * time.Millisecond) // listeners up and the handler installed

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve after SIGTERM: %v, want a clean nil return", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not shut down within 20s of SIGTERM")
	}
}
