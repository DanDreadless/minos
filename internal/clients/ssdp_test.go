package clients

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"minos/internal/config"
)

func TestParseSSDPResponse(t *testing.T) {
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"well-formed", "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nLOCATION: http://192.168.1.40:8080/desc.xml\r\nST: upnp:rootdevice\r\n\r\n",
			"http://192.168.1.40:8080/desc.xml", true},
		{"lowercase header", "HTTP/1.1 200 OK\r\nlocation: http://192.168.1.40/d.xml\r\n\r\n",
			"http://192.168.1.40/d.xml", true},
		{"not a 200", "HTTP/1.1 503 Unavailable\r\nLOCATION: http://x/d.xml\r\n\r\n", "", false},
		{"missing location", "HTTP/1.1 200 OK\r\nST: upnp:rootdevice\r\n\r\n", "", false},
		{"junk", "\x00\x01\x02 garbage", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSSDPResponse([]byte(tc.in))
			if got != tc.want || ok != tc.ok {
				t.Errorf("parseSSDPResponse = %q/%v, want %q/%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

const testDeviceXML = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
    <friendlyName>Living Room TV</friendlyName>
    <manufacturer>Samsung Electronics</manufacturer>
    <modelName>QE55Q80A</modelName>
  </device>
</root>`

func TestFetchDeviceDescriptionPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/desc.xml":
			_, _ = w.Write([]byte(testDeviceXML))
		case "/redirect":
			http.Redirect(w, r, "/desc.xml", http.StatusFound)
		case "/huge":
			_, _ = w.Write([]byte(strings.Repeat("<!-- pad -->", 10000) + testDeviceXML))
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	host, port := u.Hostname(), u.Port()
	at := func(path string) string { return "http://" + host + ":" + port + path }

	// Happy path: LOCATION on the responder itself.
	desc, ok := fetchDeviceDescription(at("/desc.xml"), host)
	if !ok || desc.FriendlyName != "Living Room TV" ||
		desc.Manufacturer != "Samsung Electronics" || desc.ModelName != "QE55Q80A" {
		t.Fatalf("fetch = %+v/%v, want the parsed description", desc, ok)
	}

	// A LOCATION pointing anywhere but the responder is SSRF bait: dropped.
	if _, ok := fetchDeviceDescription(at("/desc.xml"), "192.0.2.99"); ok {
		t.Error("host-mismatched LOCATION was fetched")
	}
	// Only plain http (UPnP device descriptions are); anything else dropped.
	if _, ok := fetchDeviceDescription("https://"+host+":"+port+"/desc.xml", host); ok {
		t.Error("non-http scheme accepted")
	}
	// Redirects are never followed (302 is not 200).
	if _, ok := fetchDeviceDescription(at("/redirect"), host); ok {
		t.Error("redirect was followed")
	}
	// An oversized body is truncated at the cap; broken XML yields ok=false.
	if _, ok := fetchDeviceDescription(at("/huge"), host); ok {
		t.Error("oversized description accepted")
	}
	if _, ok := fetchDeviceDescription("://not-a-url", host); ok {
		t.Error("malformed URL accepted")
	}
}

// applySSDP feeds the registry under ssdp provenance — which outranks an
// existing mDNS name — and never creates rows for devices that have not
// queried Minos.
func TestApplySSDP(t *testing.T) {
	r := NewRegistry()
	r.ApplyConfig(config.Default())
	r.Touch("192.168.1.40", false, time.Now())
	r.setHostname("192.168.1.40", "tv.local", SourceMDNS)

	r.applySSDP("192.168.1.40", deviceDescription{
		FriendlyName: "Living Room TV",
		Manufacturer: "Samsung Electronics",
		ModelName:    "QE55Q80A",
	})
	if n, s := hostnameOf(r, "192.168.1.40"); n != "Living Room TV" || s != SourceSSDP {
		t.Errorf("name = %q/%q, want SSDP friendlyName", n, s)
	}
	devs := r.Devices(config.Default())
	if len(devs) != 1 || devs[0].Vendor != "Samsung Electronics" || devs[0].Model != "QE55Q80A" {
		t.Errorf("device = %+v, want Samsung manufacturer+model", devs)
	}

	// Unknown IP: nothing lands, no ghost row.
	r.applySSDP("192.168.1.222", deviceDescription{FriendlyName: "Ghost"})
	if _, ok := r.seen.Load("192.168.1.222"); ok {
		t.Error("applySSDP created a row for a device that never queried")
	}

	// Hostile fields are sanitised away.
	r.applySSDP("192.168.1.40", deviceDescription{FriendlyName: "evil\x1b[2Jname"})
	if n, _ := hostnameOf(r, "192.168.1.40"); n != "Living Room TV" {
		t.Errorf("control-byte name landed: %q", n)
	}
}
