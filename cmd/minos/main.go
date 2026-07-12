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

	"minos/internal/acme"
	"minos/internal/api"
	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/dnsproxy"
	"minos/internal/filter"
	"minos/internal/importer"
	"minos/internal/lists"
	"minos/internal/notify"
	"minos/internal/querylog"
	"minos/internal/updates"
	"minos/web"
)

var (
	version = "0.1.0-dev" // overridden at release time via -ldflags
	// installMethod is stamped by release builds ("binary") and the
	// Dockerfile ("docker"); empty means an unstamped source build. Used
	// only to pick the right upgrade guidance — never to self-update.
	installMethod = ""
)

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
	case "import":
		return runImport(args)
	case "version":
		fmt.Println("minos", version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: serve, status, pause, resume, import, version)", cmd)
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

	notifier := notify.New(store)
	started := time.Now()

	reg := clients.NewRegistry()
	reg.SetQNameSource(qlog.RecentQNames) // traffic hints read the ring
	reg.ApplyConfig(cfg)
	reg.OnNewDevice(func(ip, mac, hostname string) {
		// Grace period: on a first boot (or after history loss) every
		// existing device looks new — don't flood the topic.
		if time.Since(started) < 5*time.Minute {
			return
		}
		detail := ip
		if hostname != "" {
			detail += " (" + hostname + ")"
		}
		if mac != "" {
			detail += " [" + mac + "]"
		}
		notifier.Publish("device_new", "New device on your network",
			detail+" made its first DNS query through Minos.")
	})
	// Rehydrate the device list from persisted history (best effort).
	if summaries, err := qlog.ClientsSummary(context.Background()); err != nil {
		slog.Warn("device history hydration failed", "err", err)
	} else {
		for _, s := range summaries {
			reg.Seed(s.Client, s.Total, s.Blocked, s.First, s.Last)
		}
	}

	proxy, err := dnsproxy.New(cfg, engine, qlog, reg)
	if err != nil {
		return err
	}
	proxy.SetAuditEngine(mgr.AuditEngine())
	// ACME: dynamic certificates for the DoT/DoH listeners. Wired before
	// Start so the listeners consult the manager on every handshake; the
	// renewal loop starts once the shutdown context exists below.
	var acmeMgr *acme.Manager
	if a := cfg.DNS.TLS.ACME; a != nil {
		acmeMgr, err = acme.NewManager(*a, *cfgPath)
		if err != nil {
			return fmt.Errorf("acme: %w", err)
		}
		acmeMgr.OnFailure(func(err error) {
			notifier.Publish("acme_renewal_failed", "Certificate issuance failing",
				"ACME for "+a.Domain+" is failing and will keep retrying hourly: "+err.Error())
		})
		proxy.SetCertSource(acmeMgr.GetCertificate)
	}

	proxy.OnUpstreamEvent(func(name string, sick bool) {
		if sick {
			notifier.Publish("upstream_sick", "Upstream resolver failing",
				name+" stopped answering; queries are using the remaining upstreams.")
			return
		}
		notifier.Publish("upstream_recovered", "Upstream resolver recovered",
			name+" is answering again.")
	})
	store.OnChange(func(c *config.Config) {
		if err := proxy.ApplyConfig(c); err != nil {
			slog.Error("apply config to dns proxy failed", "err", err)
		}
		qlog.SetRetentionDays(c.QueryLog.RetentionDays)
		qlog.Resize(c.QueryLog.RingSize)
		reg.ApplyConfig(c)
	})
	if err := proxy.Start(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go mgr.Run(ctx)
	go reg.Run(ctx)

	checker := updates.NewChecker(version, store)
	checker.OnUpdate(func(v string) {
		notifier.Publish("update_available", "Minos update available",
			"v"+v+" is out — https://github.com/DanDreadless/minos/releases")
	})
	go checker.Run(ctx)
	go notifier.Run(ctx)
	go notifier.RunDigest(ctx, qlog) // *querylog.Log satisfies notify.DigestStats
	if acmeMgr != nil {
		go acmeMgr.Run(ctx)
	}

	static, err := iofs.Sub(web.Dist, "dist")
	if err != nil {
		return fmt.Errorf("embedded ui: %w", err)
	}
	apiSrv := api.New(engine, qlog, store, mgr, reg, proxy, checker, static, version, installMethod)
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

// runImport merges another resolver's settings into the Minos config file.
// It edits the file directly, so restart a running instance afterwards.
func runImport(args []string) error {
	if len(args) < 2 || isFlag(args[0]) || isFlag(args[1]) {
		return fmt.Errorf("usage: minos import <pihole|adguard> <path> [-config minos.yaml]\n" +
			"  pihole:  path to /etc/pihole (or its gravity.db)\n" +
			"  adguard: path to AdGuardHome.yaml")
	}
	kind, src := args[0], args[1]
	fs, cfgPath, _ := parseCommon("import", args[2:])
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	store, err := config.Open(*cfgPath)
	if err != nil {
		return err
	}
	var rep *importer.Report
	err = store.Update(func(c *config.Config) error {
		var ierr error
		switch kind {
		case "pihole":
			rep, ierr = importer.Pihole(src, c)
		case "adguard":
			rep, ierr = importer.AdGuard(src, c)
		default:
			ierr = fmt.Errorf("unknown import source %q (pihole or adguard)", kind)
		}
		return ierr
	})
	if err != nil {
		return err
	}
	fmt.Println(rep)
	fmt.Printf("written to %s — restart minos (or start it) to apply\n", *cfgPath)
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
	if out["update_available"] == true {
		fmt.Printf("  update:  v%v is available — https://github.com/DanDreadless/minos/releases\n",
			out["latest_version"])
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
