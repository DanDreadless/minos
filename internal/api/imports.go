package api

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"minos/internal/config"
	"minos/internal/importer"
)

// maxUploadBytes caps an uploaded file. A Pi-hole gravity.db holds adlist
// URLs and allow/deny domains (not the compiled blocklists), so it stays
// small; config and AdGuard YAML are tiny.
const maxUploadBytes = 64 << 20

// importResponse is the report the UI renders after an import.
type importResponse struct {
	Lists        int      `json:"lists"`
	Allow        int      `json:"allow"`
	Deny         int      `json:"deny"`
	LocalRecords int      `json:"local_records"`
	Services     int      `json:"services"`
	Skipped      []string `json:"skipped"`
}

func reportView(r *importer.Report) importResponse {
	skipped := r.Skipped
	if skipped == nil {
		skipped = []string{}
	}
	return importResponse{
		Lists: r.Lists, Allow: r.Allow, Deny: r.Deny,
		LocalRecords: r.LocalRecords, Services: r.Services, Skipped: skipped,
	}
}

// saveUpload copies one multipart file part into dir under name, capped.
// Returns false (no error) when the optional part is absent.
func saveUpload(r *http.Request, field, dir, name string) (bool, error) {
	file, _, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	return true, copyToFile(file, filepath.Join(dir, name))
}

func copyToFile(src multipart.File, path string) error {
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// handleImportPihole ingests an uploaded gravity.db (and optional
// custom.list) and appends its settings to the running config.
func (s *Server) handleImportPihole(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "upload too large or not multipart form data")
		return
	}
	dir, err := os.MkdirTemp("", "minos-import-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not stage upload")
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if ok, err := saveUpload(r, "gravity", dir, "gravity.db"); err != nil || !ok {
		writeError(w, http.StatusBadRequest, "a gravity.db file is required")
		return
	}
	// custom.list is optional (Pi-hole's Local DNS Records).
	if _, err := saveUpload(r, "custom_list", dir, "custom.list"); err != nil {
		writeError(w, http.StatusBadRequest, "could not read custom.list")
		return
	}

	s.runImport(w, r, func(c *config.Config) (*importer.Report, error) {
		return importer.Pihole(dir, c)
	})
}

// handleImportAdGuard ingests an uploaded AdGuardHome.yaml.
func (s *Server) handleImportAdGuard(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "upload too large or not multipart form data")
		return
	}
	dir, err := os.MkdirTemp("", "minos-import-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not stage upload")
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if ok, err := saveUpload(r, "config", dir, "AdGuardHome.yaml"); err != nil || !ok {
		writeError(w, http.StatusBadRequest, "an AdGuardHome.yaml file is required")
		return
	}
	s.runImport(w, r, func(c *config.Config) (*importer.Report, error) {
		return importer.AdGuard(filepath.Join(dir, "AdGuardHome.yaml"), c)
	})
}

// runImport applies an import function inside a config Update (append-only,
// validated, persisted), fetches any new blocklists, and returns the report.
func (s *Server) runImport(w http.ResponseWriter, r *http.Request, do func(*config.Config) (*importer.Report, error)) {
	var report *importer.Report
	err := s.store.Update(func(c *config.Config) error {
		rep, ierr := do(c)
		report = rep
		return ierr
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Pull down any blocklists the import added so their rule counts show.
	s.lists.EnsureFetched(r.Context())
	writeJSON(w, http.StatusOK, reportView(report))
}

// handleImportConfig replaces the entire running config with an uploaded
// YAML backup — the counterpart to GET /api/config/export.
func (s *Server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload too large")
		return
	}
	restored, err := config.Parse(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("not a valid Minos config: %v", err))
		return
	}
	// Preserve the file-only listen addresses and storage settings: those
	// need a restart to change and must not be silently swapped from a
	// backup taken on another host.
	err = s.store.Update(func(c *config.Config) error {
		dnsListen, apiListen := c.DNS.Listen, c.API.Listen
		tls, qlog := c.DNS.TLS, c.QueryLog
		*c = *restored
		c.DNS.Listen, c.API.Listen = dnsListen, apiListen
		c.DNS.TLS, c.QueryLog = tls, qlog
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.lists.EnsureFetched(r.Context())
	writeJSON(w, http.StatusOK, viewOf(s.store.Get()))
}
