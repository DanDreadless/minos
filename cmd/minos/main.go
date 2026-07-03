// Command minos runs the DNS sinkhole. This file does flag parsing, wiring,
// and graceful shutdown only — all behavior lives in internal packages.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"minos/internal/api"
	"minos/internal/config"
	"minos/internal/dnsproxy"
	"minos/internal/filter"
	"minos/internal/lists"
	"minos/internal/querylog"
	"minos/web"
)

var version = "0.1.0-dev" // overridden at release time via -ldflags

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "minos:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve":
		return serve(args)
	case "status":
		return clientStatus(args)
	case "pause":
		return clientPause(args)
	case "resume":
		return clientResume(args)
	case "version":
		fmt.Println("minos", version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: serve, status, pause, resume, version)", cmd)
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func parseCommon(name string, args []string) (*flag.FlagSet, *string, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cfgPath := fs.String("config", "minos.yaml", "path to config file")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn, error")
	return fs, cfgPath, logLevel
}

func serve(args []string) error {
	fs, cfgPath, logLevel := parseCommon("serve", args)
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLogging(*logLevel)

	store, err := config.Open(*cfgPath)
	if err != nil {
		return err
	}
	cfg := store.Get()

	qlog, err := querylog.Open(querylog.Options{
		RingSize:      cfg.QueryLog.RingSize,
		DBPath:        cfg.QueryLog.DBPath,
		Ephemeral:     cfg.QueryLog.Ephemeral,
		RetentionDays: cfg.QueryLog.RetentionDays,
	})
	if err != nil {
		return err
	}

	engine := filter.NewEngine()
	mgr := lists.NewManager(engine, store)

	proxy, err := dnsproxy.New(cfg, engine, qlog)
	if err != nil {
		return err
	}
	store.OnChange(func(c *config.Config) {
		if err := proxy.ApplyConfig(c); err != nil {
			slog.Error("apply config to dns proxy failed", "err", err)
		}
	})
	if err := proxy.Start(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go mgr.Run(ctx)

	static, err := iofs.Sub(web.Dist, "dist")
	if err != nil {
		return fmt.Errorf("embedded ui: %w", err)
	}
	apiSrv := api.New(engine, qlog, store, mgr, static, version)
	httpSrv := &http.Server{
		Addr:              cfg.API.Listen,
		Handler:           apiSrv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	httpErr := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.API.Listen)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-httpErr:
		return fmt.Errorf("api server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("api shutdown", "err", err)
	}
	if err := proxy.Shutdown(shutdownCtx); err != nil {
		slog.Warn("dns shutdown", "err", err)
	}
	// Close last: flushes buffered query log entries to disk.
	if err := qlog.Close(); err != nil {
		slog.Warn("query log close", "err", err)
	}
	return nil
}

func setupLogging(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}

// --- client subcommands: talk to a running instance over its API ---

func apiClient(cfgPath string) (base string, token string, err error) {
	store, err := config.Open(cfgPath)
	if err != nil {
		return "", "", err
	}
	cfg := store.Get()
	host := cfg.API.Listen
	if host[0] == ':' {
		host = "127.0.0.1" + host
	}
	return "http://" + host, cfg.API.Token, nil
}

func apiDo(cfgPath, method, path string, body any) (map[string]any, error) {
	base, token, err := apiClient(cfgPath)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("X-Api-Token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("is minos running? %w", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("bad response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%v", out["error"])
	}
	return out, nil
}

func clientStatus(args []string) error {
	fs, cfgPath, _ := parseCommon("status", args)
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := apiDo(*cfgPath, http.MethodGet, "/api/status", nil)
	if err != nil {
		return err
	}
	fmt.Printf("minos %v — up %vs\n", out["version"], out["uptime_seconds"])
	fmt.Printf("  queries: %v total, %v blocked\n", out["queries_total"], out["queries_blocked"])
	fmt.Printf("  rules:   %v block, %v allow (%v skipped)\n", out["rules"], out["allow_rules"], out["rules_skipped"])
	if out["paused"] == true {
		until := out["paused_until"]
		if until == nil {
			fmt.Println("  blocking: PAUSED indefinitely")
		} else {
			fmt.Printf("  blocking: PAUSED until %v\n", until)
		}
	} else {
		fmt.Println("  blocking: active")
	}
	return nil
}

func clientPause(args []string) error {
	duration := ""
	if len(args) > 0 && !isFlag(args[0]) {
		duration = args[0]
		args = args[1:]
	}
	fs, cfgPath, _ := parseCommon("pause", args)
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := apiDo(*cfgPath, http.MethodPost, "/api/pause", map[string]string{"duration": duration})
	if err != nil {
		return err
	}
	if until, ok := out["paused_until"]; ok {
		fmt.Printf("blocking paused until %v\n", until)
	} else {
		fmt.Println("blocking paused indefinitely (resume with: minos resume)")
	}
	return nil
}

func clientResume(args []string) error {
	fs, cfgPath, _ := parseCommon("resume", args)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := apiDo(*cfgPath, http.MethodDelete, "/api/pause", nil); err != nil {
		return err
	}
	fmt.Println("blocking resumed")
	return nil
}
