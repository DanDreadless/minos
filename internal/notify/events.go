package notify

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The wording of every event lives here, one constructor per event, so it
// can be read (and tested) in one place. Rules: say what happened, why it
// matters and what to do; name things the way the UI does; link to where
// it can be acted on.

// DeviceInfo is what a new-device notification says about a device.
type DeviceInfo struct {
	Key        string // the device page key: MAC when known, else IP
	Name       string // user label, else the best discovered name
	IP         string
	MAC        string
	Vendor     string
	PrivateMAC bool
}

// label is the device's name, or the most specific stand-in.
func (d DeviceInfo) label() string {
	switch {
	case d.Name != "":
		return d.Name
	case d.Vendor != "":
		return d.Vendor + " device"
	case d.PrivateMAC:
		return "A device with a private address"
	}
	return "An unnamed device"
}

// DeviceHref is the UI path of a device's page.
func DeviceHref(key string) string { return "#/device/" + url.PathEscape(key) }

// NewDevicesEvent announces devices that made their first DNS query. One
// device gets its own message and a link to its page; several (a router
// reboot, guests arriving) are one message linking to the Devices list.
func NewDevicesEvent(devs []DeviceInfo) Event {
	if len(devs) == 1 {
		d := devs[0]
		var facts []string
		if d.Vendor != "" && d.Name != "" {
			facts = append(facts, "made by "+d.Vendor)
		}
		facts = append(facts, "address "+d.IP)
		switch {
		case d.PrivateMAC:
			facts = append(facts, "private (randomized) MAC address")
		case d.MAC != "":
			facts = append(facts, "MAC "+d.MAC)
		}
		title := "New device: " + d.label()
		if d.Name == "" && d.Vendor == "" {
			title = "New device on your network"
		}
		return Event{
			Type:  TypeDeviceNew,
			Title: title,
			Message: fmt.Sprintf("%s made its first DNS query through Minos (%s). "+
				"If you don't recognise it, open it in Minos to name it, put it in a group, or block it.",
				d.label(), strings.Join(facts, ", ")),
			Priority: PriorityDefault,
			Link:     DeviceHref(d.Key),
			Actions:  []Action{{Label: "Open device", URL: DeviceHref(d.Key)}},
		}
	}
	var b strings.Builder
	shown := devs
	if len(shown) > 8 {
		shown = shown[:8]
	}
	for _, d := range shown {
		fmt.Fprintf(&b, "• %s (%s)\n", d.label(), d.IP)
	}
	if more := len(devs) - len(shown); more > 0 {
		fmt.Fprintf(&b, "…and %d more.\n", more)
	}
	b.WriteString("Open Devices in Minos to name them, group them, or block any you don't recognise.")
	return Event{
		Type:     TypeDeviceNew,
		Title:    fmt.Sprintf("%d new devices on your network", len(devs)),
		Message:  b.String(),
		Priority: PriorityDefault,
		Link:     "#/devices",
		Actions:  []Action{{Label: "Open Devices", URL: "#/devices"}},
	}
}

// ListEvent describes a blocklist changing state. state is the list's
// state word (lists.ListState); detail is the plain reason.
func ListEvent(name, state, detail string) Event {
	e := Event{
		Type:     TypeListFailing,
		Link:     "#/lists",
		Actions:  []Action{{Label: "Open blocklists", URL: "#/lists"}},
		Priority: PriorityDefault,
	}
	why := ""
	if detail != "" {
		why = " (" + detail + ")"
	}
	switch state {
	case "ok":
		e.Type = TypeListRecovered
		e.Priority = PriorityLow
		e.Title = "Blocklist updating again: " + name
		e.Message = name + " downloaded successfully and is up to date. Nothing to do."
	case "retired":
		e.Priority = PriorityHigh
		e.Title = "Blocklist retired: " + name
		e.Message = name + " is no longer published" + why + ". Minos keeps blocking with the last copy, " +
			"which will slowly go out of date. Replace it with a current list on the Blocklists page."
	case "unauthorized":
		e.Priority = PriorityHigh
		e.Title = "Blocklist refusing downloads: " + name
		e.Message = name + " now refuses the download" + why + ". If it needs a key, check the list's " +
			"headers in minos.yaml. Minos keeps blocking with the last copy meanwhile."
	case "unavailable":
		e.Priority = PriorityHigh
		e.Title = "Blocklist not loaded: " + name
		e.Message = name + " has never downloaded" + why + ", so it is not blocking anything yet. " +
			"Minos keeps retrying; check the address on the Blocklists page if this persists."
	default:
		e.Title = "Blocklist out of date: " + name
		e.Message = name + " has failed to update for over two refresh intervals" + why +
			". Minos keeps blocking with the last copy and keeps retrying."
	}
	return e
}

// UpstreamEvent describes one upstream resolver failing or recovering.
// working lists the other upstreams still answering; when none are, the
// network has no DNS at all and the event is DNSDownEvent instead.
func UpstreamEvent(name string, sick bool, working []string) Event {
	e := Event{
		Link:    "#/settings?tab=dns",
		Actions: []Action{{Label: "Open resolver settings", URL: "#/settings?tab=dns"}},
	}
	if !sick {
		e.Type = TypeUpstreamRecovered
		e.Priority = PriorityLow
		e.Title = "Resolver answering again: " + name
		e.Message = name + " is answering queries again. Nothing to do."
		return e
	}
	if len(working) == 0 {
		return DNSDownEvent(name)
	}
	e.Type = TypeUpstreamSick
	e.Priority = PriorityDefault
	e.Title = "Resolver not answering: " + name
	e.Message = fmt.Sprintf("%s stopped answering. Lookups are going to %s meanwhile, so the network "+
		"is unaffected. Minos re-tests it every 30 seconds; nothing to do unless it stays down.",
		name, strings.Join(working, ", "))
	return e
}

// DNSDownEvent: no upstream answers, so nothing outside the blocklists and
// local records resolves. The one event that ignores quiet hours.
func DNSDownEvent(last string) Event {
	return Event{
		Type:     TypeDNSDown,
		Priority: PriorityUrgent,
		Title:    "DNS is down: no resolver is answering",
		Message: "Every upstream resolver has stopped answering (the last was " + last + "), so devices " +
			"cannot look up websites. Check this server's internet connection, or add a backup " +
			"resolver in Settings.",
		Link:    "#/settings?tab=dns",
		Actions: []Action{{Label: "Open resolver settings", URL: "#/settings?tab=dns"}},
	}
}

// BreakageEvent: a device is retrying a name a blocklist blocks, the way an
// app does when blocking has broken it. Links to the dashboard, where the
// suggestion offers Allow, Allow for 1 hour and Dismiss.
func BreakageEvent(dev DeviceInfo, domain, list string, count int) Event {
	who := dev.label()
	return Event{
		Type:     TypeBreakage,
		Priority: PriorityDefault,
		Title:    "Something may be broken on " + who,
		Message: fmt.Sprintf("%s asked for %s %d times in two minutes, and %s blocks it — an app on it has "+
			"probably stopped working. If so, allow the domain from the Minos dashboard, for an hour or for good.",
			who, domain, count, list),
		Link:    "#/",
		Actions: []Action{{Label: "Open Minos", URL: "#/"}},
	}
}

// UpdateEvent announces a newer release.
func UpdateEvent(current, latest string) Event {
	latest = strings.TrimPrefix(latest, "v")
	notes := "https://github.com/DanDreadless/minos/releases/tag/v" + latest
	return Event{
		Type:     TypeUpdateAvailable,
		Priority: PriorityLow,
		Title:    "Minos v" + latest + " is available",
		Message: fmt.Sprintf("This server runs v%s. Read what changed in the release notes, "+
			"then upgrade the way you installed Minos.", strings.TrimPrefix(current, "v")),
		Link:    notes,
		Actions: []Action{{Label: "Release notes", URL: notes}, {Label: "Open Minos", URL: "#/settings?tab=system"}},
	}
}

// UpdateResultEvent reports how a one-click update ended: state is the
// updater's done, failed or rolled_back. It shares the update_available
// type, so muting "Minos updates" silences both.
func UpdateResultEvent(state, from, to, message string) Event {
	to = strings.TrimPrefix(to, "v")
	from = strings.TrimPrefix(from, "v")
	e := Event{
		Type:    TypeUpdateAvailable,
		Link:    "#/settings?tab=system",
		Actions: []Action{{Label: "Open Minos", URL: "#/settings?tab=system"}},
	}
	switch state {
	case "done":
		e.Priority = PriorityLow
		e.Title = "Minos updated to v" + to
		e.Message = fmt.Sprintf("The update from v%s finished and the new version is answering.", from)
	case "rolled_back":
		e.Priority = PriorityHigh
		e.Title = "Minos update to v" + to + " was rolled back"
		e.Message = fmt.Sprintf("The new version did not start, so v%s was put back and is running. "+
			"Check the Logs, and report the problem if it happens again.", from)
	default:
		e.Priority = PriorityDefault
		e.Title = "Minos update to v" + to + " did not happen"
		e.Message = message + " Minos is still running v" + from + "."
	}
	return e
}

// ACMEEvent: the DoT/DoH certificate cannot be issued or renewed.
func ACMEEvent(domain string, err error) Event {
	return Event{
		Type:     TypeACMEFailed,
		Priority: PriorityHigh,
		Title:    "Certificate renewal failing for " + domain,
		Message: "Minos cannot get a certificate for " + domain + " and will retry hourly: " + err.Error() +
			". Encrypted DNS (DoT/DoH) keeps the current certificate until it expires.",
		Link: "#/settings?tab=dns",
	}
}

// upstreamCooldown is how long after announcing an upstream failure a
// repeat failure stays quiet — the breaker trips and recovers on a 30 s
// cycle, and a flapping resolver must not buzz a phone every minute.
const upstreamCooldown = 30 * time.Minute

// UpstreamHealth is what the notifier needs to ask the resolver about.
type UpstreamHealth interface {
	// Working lists the configured upstreams, other than name, still answering.
	Working(name string) []string
	// Failing reports whether name is failing right now.
	Failing(name string) bool
}

// UpstreamChanged announces an upstream failing or recovering, with flap
// control: a failure within the cooldown of the last announced one stays
// quiet (and so does its recovery), but is re-checked when the cooldown
// ends, so a resolver that really did stay down is still reported.
func (n *Notifier) UpstreamChanged(h UpstreamHealth, name string, sick bool) {
	if n.Transition("upstream:"+name, sick, upstreamCooldown) {
		n.Publish(UpstreamEvent(name, sick, h.Working(name)))
		return
	}
	if sick {
		time.AfterFunc(upstreamCooldown, func() {
			if h.Failing(name) && n.Transition("upstream:"+name, true, 0) {
				e := UpstreamEvent(name, true, h.Working(name))
				e.Message = "Still failing after repeated attempts. " + e.Message
				n.Publish(e)
			}
		})
	}
}

// DeviceBatcher collects new devices for a short while before announcing
// them. The wait does two jobs: discovery usually finds a device's name
// within a minute of its first query, so the message can name it; and a
// burst (every device reconnecting after a router reboot) becomes one
// message instead of twenty.
type DeviceBatcher struct {
	delay   time.Duration
	resolve func(ips []string) []DeviceInfo
	publish func(Event)

	mu      sync.Mutex
	pending []string
	timer   *time.Timer
}

// NewDeviceBatcher announces through publish, resolving each IP's current
// identity at send time.
func NewDeviceBatcher(delay time.Duration, resolve func(ips []string) []DeviceInfo, publish func(Event)) *DeviceBatcher {
	return &DeviceBatcher{delay: delay, resolve: resolve, publish: publish}
}

// Add queues a device's IP; the batch goes out delay after its first entry.
func (b *DeviceBatcher) Add(ip string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, ip)
	if b.timer == nil {
		b.timer = time.AfterFunc(b.delay, b.flush)
	}
}

func (b *DeviceBatcher) flush() {
	b.mu.Lock()
	ips := b.pending
	b.pending, b.timer = nil, nil
	b.mu.Unlock()
	if devs := b.resolve(ips); len(devs) > 0 {
		b.publish(NewDevicesEvent(devs))
	}
}
