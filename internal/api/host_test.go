package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"minos/internal/hostinfo"
)

// fakeHost stands in for hostinfo.Collector so the tests do not depend on
// what the machine running them happens to report.
type fakeHost struct {
	supported bool
	info      hostinfo.Info
	sample    *hostinfo.Sample
}

func (f fakeHost) Supported() bool          { return f.supported }
func (f fakeHost) Info() hostinfo.Info      { return f.info }
func (f fakeHost) Latest() *hostinfo.Sample { return f.sample }

func f64(v float64) *float64 { return &v }
func u64(v uint64) *uint64   { return &v }

func supportedHost() fakeHost {
	return fakeHost{
		supported: true,
		info: hostinfo.Info{
			Hostname: "vault-tec", OS: "linux", Arch: "arm64", CPUs: 4,
			Platform: "Debian GNU/Linux 12 (bookworm)", MemTotal: 4 << 30,
			MemSource: hostinfo.MemoryHost,
		},
		sample: &hostinfo.Sample{
			Time: time.Now(), CPUPercent: f64(12.5), Load1: f64(0.4),
			MemUsed: u64(1 << 30), MemAvailable: u64(3 << 30),
			DiskTotal: u64(32 << 30), DiskFree: u64(20 << 30),
			TempCelsius: f64(48.7), ProcRSS: u64(90 << 20), Goroutines: 42,
		},
	}
}

func TestHostEndpointReportsTheMachine(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.host = supportedHost()

	rec := doJSON(t, s.Router(), http.MethodGet, "/api/host", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got hostResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Supported {
		t.Fatal("supported = false, want true")
	}
	if got.Hostname != "vault-tec" || got.CPUs != 4 {
		t.Errorf("info = %+v, want the collector's values", got.Info)
	}
	if got.Sample == nil {
		t.Fatal("sample missing")
	}
	if got.Sample.CPUPercent == nil || *got.Sample.CPUPercent != 12.5 {
		t.Errorf("cpu_percent = %v, want 12.5", got.Sample.CPUPercent)
	}
}

// A container install reports nothing at all — not a hostname, not a
// zeroed sample. The UI hides the card on supported=false, so anything
// else leaking through would be shown as if it described the host.
func TestHostEndpointReportsNothingInAContainer(t *testing.T) {
	for _, tc := range []struct {
		name string
		host HostSource
	}{
		{"container", fakeHost{supported: false, info: hostinfo.Info{Hostname: "abc123"}}},
		{"unwired", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(t, "")
			s.host = tc.host

			rec := doJSON(t, s.Router(), http.MethodGet, "/api/host", "", nil)
			// Not a 404: the endpoint exists and answered. "Does not
			// apply here" is a different thing from "no such route".
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var got hostResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Supported {
				t.Error("supported = true, want false")
			}
			if got.Hostname != "" || got.Sample != nil {
				t.Errorf("leaked host detail: %+v sample=%v", got.Info, got.Sample)
			}
			// Nothing beyond the flag: a serialised "container": false
			// inside a container would be a confident falsehood.
			if body := strings.TrimSpace(rec.Body.String()); body != `{"supported":false}` {
				t.Errorf("body = %s, want exactly {\"supported\":false}", body)
			}
		})
	}
}

func TestHostMetricsExposed(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.host = supportedHost()

	rec := doJSON(t, s.Router(), http.MethodGet, "/metrics", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`minos_host_info{hostname="vault-tec",os="linux",arch="arm64",platform="Debian GNU/Linux 12 (bookworm)"} 1`,
		"minos_host_cpus 4",
		"minos_host_cpu_percent 12.5",
		"minos_host_load1 0.4",
		`minos_host_memory_bytes{state="used"}`,
		`minos_host_disk_bytes{state="free"}`,
		"minos_host_temperature_celsius 48.7",
		"minos_process_goroutines 42",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

// A metric this platform cannot supply must produce no series at all. A
// zero would graph as an idle, healthy machine when the truth is that
// nobody measured it — the one failure mode worse than a gap.
func TestHostMetricsOmitUnavailableReadings(t *testing.T) {
	h := supportedHost()
	h.sample = &hostinfo.Sample{Time: time.Now(), Goroutines: 7} // darwin-shaped: nothing measurable
	s, _ := newTestServer(t, "")
	s.host = h

	body := doJSON(t, s.Router(), http.MethodGet, "/metrics", "", nil).Body.String()
	for _, absent := range []string{
		"minos_host_cpu_percent",
		"minos_host_load1",
		"minos_host_memory_bytes",
		"minos_host_disk_bytes",
		"minos_host_temperature_celsius",
	} {
		if strings.Contains(body, absent) {
			t.Errorf("metrics contain %q for an unavailable reading, want it omitted", absent)
		}
	}
	if !strings.Contains(body, "minos_process_goroutines 7") {
		t.Error("goroutines missing; always available, should still be reported")
	}
}

// Container installs must not appear in a scrape either, or a dashboard
// would chart the host of a machine that never reported one.
func TestHostMetricsAbsentInAContainer(t *testing.T) {
	s, _ := newTestServer(t, "")
	s.host = fakeHost{supported: false, info: hostinfo.Info{Hostname: "abc123"}}

	body := doJSON(t, s.Router(), http.MethodGet, "/metrics", "", nil).Body.String()
	if strings.Contains(body, "minos_host_") {
		t.Error("host metrics present on a container install, want none")
	}
}
