package main

import (
	"slices"

	"minos/internal/clients"
	"minos/internal/config"
	"minos/internal/dnsproxy"
	"minos/internal/notify"
)

// deviceInfos describes the devices behind ips for a notification, one
// entry per physical device (two IPs of one device are one entry).
func deviceInfos(devs []clients.Device, ips []string) []notify.DeviceInfo {
	var out []notify.DeviceInfo
	seen := map[string]bool{}
	for _, ip := range ips {
		info := notify.DeviceInfo{Key: ip, IP: ip}
		for _, d := range devs {
			if d.IP != ip && !slices.Contains(d.IPs, ip) {
				continue
			}
			info = notify.DeviceInfo{
				Key:        d.IP,
				Name:       firstNonEmpty(d.Name, d.Hostname, d.Model),
				IP:         ip,
				MAC:        d.MAC,
				Vendor:     d.Vendor,
				PrivateMAC: d.PrivateMAC,
			}
			if d.MAC != "" {
				info.Key = d.MAC
			}
			break
		}
		if !seen[info.Key] {
			seen[info.Key] = true
			out = append(out, info)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// upstreamHealth answers the notifier's questions from the resolver's
// live counters, limited to the upstreams configured now (counters for
// removed upstreams outlive them).
type upstreamHealth struct {
	proxy *dnsproxy.Server
	store *config.Store
}

func (h upstreamHealth) states() map[string]dnsproxy.UpstreamState {
	m := map[string]dnsproxy.UpstreamState{}
	for _, st := range h.proxy.UpstreamStats() {
		m[st.Name] = st.State
	}
	return m
}

func (h upstreamHealth) Working(name string) []string {
	states := h.states()
	var out []string
	for _, u := range h.store.Get().DNS.Upstreams {
		// An upstream never asked yet is untested, not broken: it is
		// where queries go next.
		if u.Address != name && states[u.Address] != dnsproxy.UpstreamFailing {
			out = append(out, u.Address)
		}
	}
	return out
}

func (h upstreamHealth) Failing(name string) bool {
	return h.states()[name] == dnsproxy.UpstreamFailing
}
