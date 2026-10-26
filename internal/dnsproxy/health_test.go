package dnsproxy

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"minos/internal/config"
)

// serveTCP answers every question on addr with 198.51.100.7.
func serveTCP(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("could not bind %s: %v", addr, err)
	}
	srv := &dns.Server{Listener: ln, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(req)
		_ = w.WriteMsg(reply)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
}

func shrinkHealthTimers(t *testing.T) {
	t.Helper()
	oldCooldown, oldTick, oldIdle := failoverCooldown, probeTick, idleRecheck
	failoverCooldown, probeTick, idleRecheck = 50*time.Millisecond, 20*time.Millisecond, time.Hour
	t.Cleanup(func() { failoverCooldown, probeTick, idleRecheck = oldCooldown, oldTick, oldIdle })
}

// The field bug: a backup that failed alongside the primary (a network blip
// takes out both) was never asked again while the primary answered, so its
// breaker never cleared and it read "failing" for good.
func TestTrippedBackupRecoversWhilePrimaryAnswers(t *testing.T) {
	shrinkHealthTimers(t)
	primary, _ := startCountingUpstream(t)
	backup := deadUpstreamAddr(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{
			{Address: primary, Protocol: "udp"},
			{Address: backup, Protocol: "tcp"},
		}
		c.DNS.Cache.Enabled = false
	})
	for i := 0; i < failThreshold; i++ {
		srv.trackUpstream(backup, time.Millisecond, fmt.Errorf("i/o timeout"))
	}
	if st := upstreamStat(t, srv, backup); st.State != UpstreamFailing {
		t.Fatalf("setup: backup state %s, want failing", st.State)
	}
	serveTCP(t, backup) // the backup comes back

	addr := srv.UDPAddr().String()
	deadline := time.Now().Add(5 * time.Second)
	for {
		query(t, addr, "steady.example.org", dns.TypeA) // all answered by the primary
		if st := upstreamStat(t, srv, backup); st.State == UpstreamHealthy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the recovered backup never turned healthy while the primary answered")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// A backup no query has reached is probed, so its light says whether it
// works instead of "not used yet" forever.
func TestIdleBackupIsProbed(t *testing.T) {
	oldTick := probeTick
	probeTick = time.Hour // drive probeDue by hand
	// Restored after the server's own cleanup (cleanups run last-first),
	// never while its probe loop is running — the race CI caught.
	t.Cleanup(func() { probeTick = oldTick })
	primary, _ := startCountingUpstream(t)
	backup, backupCount := startCountingUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{
			{Address: primary, Protocol: "udp"},
			{Address: backup, Protocol: "udp"},
		}
	})
	srv.probeDue(context.Background(), time.Now())
	if st := upstreamStat(t, srv, backup); st.State != UpstreamHealthy || backupCount.Load() != 1 {
		t.Fatalf("after the probe: state %s, %d queries; want healthy after one", st.State, backupCount.Load())
	}
	// Recently checked: not probed again until idleRecheck passes.
	srv.probeDue(context.Background(), time.Now())
	if backupCount.Load() != 1 {
		t.Errorf("probed again immediately: %d queries", backupCount.Load())
	}
	srv.probeDue(context.Background(), time.Now().Add(idleRecheck+time.Second))
	if backupCount.Load() != 2 {
		t.Errorf("not re-checked after idleRecheck: %d queries", backupCount.Load())
	}
}

func TestTestUpstream(t *testing.T) {
	oldCooldown := failoverCooldown
	failoverCooldown = time.Hour
	defer func() { failoverCooldown = oldCooldown }()
	good, _ := startCountingUpstream(t)
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: good, Protocol: "udp"}}
	})
	// A configured upstream marked failing: the test clears it at once,
	// without waiting for the cooldown.
	for i := 0; i < failThreshold; i++ {
		srv.trackUpstream(good, time.Millisecond, fmt.Errorf("i/o timeout"))
	}
	res := srv.TestUpstream(context.Background(), config.Upstream{Address: good, Protocol: "udp"})
	if !res.OK || !res.Configured || res.Rcode != "NOERROR" {
		t.Fatalf("configured test = %+v", res)
	}
	if st := upstreamStat(t, srv, good); st.State != UpstreamHealthy {
		t.Errorf("after a passing test: state %s, want healthy", st.State)
	}

	// An unsaved upstream is tested but leaves no health record.
	other, _ := startCountingUpstream(t)
	if res := srv.TestUpstream(context.Background(), config.Upstream{Address: other, Protocol: "udp"}); !res.OK || res.Configured {
		t.Fatalf("unsaved test = %+v", res)
	}
	for _, u := range srv.UpstreamStats() {
		if u.Name == other {
			t.Error("testing an unsaved upstream created a health record")
		}
	}

	dead := deadUpstreamAddr(t)
	if res := srv.TestUpstream(context.Background(), config.Upstream{Address: dead, Protocol: "tcp"}); res.OK || res.Err == "" {
		t.Errorf("dead upstream test = %+v, want a failure with the error", res)
	}
	if res := srv.TestUpstream(context.Background(), config.Upstream{Address: "x", Protocol: "bogus"}); res.OK || res.Err == "" {
		t.Errorf("invalid upstream test = %+v, want an error", res)
	}
}

// An exchange cut short because the query's own time ran out (a slow
// primary used most of it) is not held against the upstream.
func TestQueryTimeoutIsNotTheUpstreamsFault(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0") // answers nothing
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	silent := pc.LocalAddr().String()
	srv, _ := startProxyCfg(t, "zero_ip", func(c *config.Config) {
		c.DNS.Upstreams = []config.Upstream{{Address: silent, Protocol: "udp"}}
		c.DNS.Cache.Enabled = false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := new(dns.Msg)
	req.SetQuestion("late.example.org.", dns.TypeA)
	if _, _, _, err := srv.forward(ctx, req, "late.example.org"); err == nil {
		t.Fatal("a silent upstream answered")
	}
	for _, u := range srv.UpstreamStats() {
		if u.Name == silent && u.Failures != 0 {
			t.Errorf("silent upstream charged %d failures for the query's own deadline", u.Failures)
		}
	}
}
