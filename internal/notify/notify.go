// Package notify delivers curated, low-volume events — a new device on the
// network, an upstream tripping the breaker, an available update — to a
// generic webhook and/or an ntfy topic. Delivery is best-effort and always
// off the query hot path: publishers drop into a buffered channel and a
// single worker does the HTTP. Nothing is ever sent unless the user has
// configured a destination.
//
// The standard every event meets (Phase 2b, October 2026): a specific,
// plain title; a body that says what happened, why it matters and what (if
// anything) to do; a link to the exact place in Minos to act on it; and a
// priority, so a failing list is never as loud as DNS being down.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"minos/internal/config"
)

const (
	queueSize      = 64
	deliverTimeout = 10 * time.Second
)

// Priorities, on ntfy's 1–5 scale (webhooks receive the same number).
const (
	PriorityMin     = 1
	PriorityLow     = 2
	PriorityDefault = 3
	PriorityHigh    = 4
	PriorityUrgent  = 5
)

// Event types — stable, machine-readable, and the names `notifications.mute`
// takes.
const (
	TypeDeviceNew         = "device_new"
	TypeUpstreamSick      = "upstream_sick"
	TypeUpstreamRecovered = "upstream_recovered"
	TypeDNSDown           = "dns_down"
	TypeListFailing       = "list_failing"
	TypeListRecovered     = "list_recovered"
	TypeUpdateAvailable   = "update_available"
	TypeACMEFailed        = "acme_renewal_failed"
	TypeDigest            = "digest"
	TypeTest              = "test"
)

// Event is one notification. Type is a stable machine-readable key; Title
// and Message are human wording (plain and literal, like all error text —
// notifications land outside the UI, where themed words have no hover
// text). Link is where to act on it in Minos: a UI path such as
// "#/device/aa:bb:…" (made absolute with the configured UI address at
// delivery) or an absolute URL.
type Event struct {
	Type     string    `json:"type"`
	Title    string    `json:"title"`
	Message  string    `json:"message"`
	Time     time.Time `json:"time"`
	URL      string    `json:"url,omitempty"`
	Priority int       `json:"priority"`
	Actions  []Action  `json:"actions,omitempty"`

	// Link and the action links before they are made absolute.
	Link string `json:"-"`
}

// Action is a button on the notification (ntfy "view" action).
type Action struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// ntfyTags maps event types to ntfy emoji tags.
var ntfyTags = map[string]string{
	TypeDeviceNew:         "new",
	TypeUpstreamSick:      "warning",
	TypeUpstreamRecovered: "white_check_mark",
	TypeDNSDown:           "rotating_light",
	TypeListFailing:       "warning",
	TypeListRecovered:     "white_check_mark",
	TypeUpdateAvailable:   "arrow_up",
	TypeACMEFailed:        "lock",
	TypeDigest:            "bar_chart",
	TypeTest:              "bell",
}

// Notifier fans events out to the configured sinks. Safe for concurrent use.
type Notifier struct {
	store  *config.Store
	ch     chan Event
	client *http.Client
	now    func() time.Time

	mu    sync.Mutex
	gates map[string]gate // Transition state per key

	// namer turns a client IP into the device's name for message text
	// (set by main from the device registry; nil leaves addresses as is).
	namer func(ip string) string
}

// SetDeviceNamer installs the IP → device-name lookup. Call before Run.
func (n *Notifier) SetDeviceNamer(fn func(ip string) string) { n.namer = fn }

func (n *Notifier) deviceName(ip string) string {
	if n.namer == nil {
		return ""
	}
	return n.namer(ip)
}

func New(store *config.Store) *Notifier {
	return &Notifier{
		store:  store,
		ch:     make(chan Event, queueSize),
		client: &http.Client{Timeout: deliverTimeout},
		now:    time.Now,
		gates:  make(map[string]gate),
	}
}

func configured(c config.NotificationsConfig) bool {
	return c.WebhookURL != "" || c.NtfyURL != ""
}

// Publish queues an event. Never blocks: with no sink configured, a muted
// type, or a full queue the event is dropped — notifications are a
// convenience, not a log.
func (n *Notifier) Publish(e Event) {
	cfg := n.store.Get().Notifications
	if !configured(cfg) || muted(cfg, e.Type) {
		return
	}
	if e.Time.IsZero() {
		e.Time = n.now()
	}
	if e.Priority == 0 {
		e.Priority = PriorityDefault
	}
	select {
	case n.ch <- e:
	default:
		slog.Debug("notification queue full, event dropped", "type", e.Type)
	}
}

func muted(c config.NotificationsConfig, typ string) bool {
	for _, m := range c.Mute {
		if m == typ {
			return true
		}
	}
	return false
}

// gate remembers the last state announced for a key and when.
type gate struct {
	bad bool
	at  time.Time
}

// Transition decides whether a state change for key is worth announcing,
// so a flapping upstream doesn't buzz a phone every few minutes: a "bad"
// transition is announced at most once per cooldown, and a recovery only
// when the failure before it was announced. Returns true to publish.
func (n *Notifier) Transition(key string, bad bool, cooldown time.Duration) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	g, seen := n.gates[key]
	now := n.now()
	switch {
	case bad && seen && g.bad:
		return false // already announced, still failing
	case bad && seen && now.Sub(g.at) < cooldown:
		return false // flapping: stay quiet, and so does its recovery
	case bad:
		n.gates[key] = gate{bad: true, at: now}
		return true
	case seen && g.bad:
		n.gates[key] = gate{bad: false, at: g.at} // keep the failure's time for the cooldown
		return true
	}
	return false // a recovery nobody was told about the failure of
}

// Run delivers queued events until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-n.ch:
			// Config is re-read per event so Settings changes apply live.
			cfg := n.store.Get()
			n.deliver(ctx, cfg, n.finish(cfg, e))
		}
	}
}

// finish makes links absolute and applies quiet hours.
func (n *Notifier) finish(cfg *config.Config, e Event) Event {
	base := UIBase(cfg)
	if e.Link != "" {
		e.URL = absolute(base, e.Link)
	}
	acts := make([]Action, 0, len(e.Actions))
	for _, a := range e.Actions {
		if u := absolute(base, a.URL); u != "" {
			acts = append(acts, Action{Label: a.Label, URL: u})
		}
	}
	e.Actions = acts
	// Quiet hours silence rather than drop: the event still arrives, just
	// without a sound — except the one that means the network is down.
	if e.Priority < PriorityUrgent && inQuietHours(cfg.Notifications.QuietHours, e.Time) && e.Priority > PriorityLow {
		e.Priority = PriorityLow
	}
	return e
}

// absolute resolves a UI path ("#/devices") against the UI base address;
// absolute URLs pass through. Without a base, a UI path has nowhere to go.
func absolute(base, link string) string {
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return link
	}
	if base == "" {
		return ""
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(link, "/")
}

// deliver sends to every configured sink and reports each failure.
func (n *Notifier) deliver(ctx context.Context, cfg *config.Config, e Event) map[string]error {
	out := map[string]error{}
	nc := cfg.Notifications
	if nc.WebhookURL != "" {
		out["webhook"] = n.deliverWebhook(ctx, nc.WebhookURL, e)
	}
	if nc.NtfyURL != "" {
		out["ntfy"] = n.deliverNtfy(ctx, nc, e)
	}
	for sink, err := range out {
		if err != nil {
			slog.Debug("notification delivery failed", "sink", sink, "type", e.Type, "err", err)
		}
	}
	return out
}

// SendTest delivers a test event right now, bypassing the queue and mutes,
// and reports each sink's outcome — Settings' "Send test notification".
func (n *Notifier) SendTest(ctx context.Context) (map[string]error, error) {
	cfg := n.store.Get()
	if !configured(cfg.Notifications) {
		return nil, errors.New("no webhook or ntfy topic is configured")
	}
	e := Event{
		Type:     TypeTest,
		Title:    "Minos test notification",
		Message:  "Notifications from Minos reach you here. Tap to open Minos.",
		Time:     n.now(),
		Priority: PriorityDefault,
		Link:     "#/settings",
	}
	return n.deliver(ctx, cfg, n.finish(cfg, e)), nil
}

// deliverWebhook POSTs the event as JSON.
func (n *Notifier) deliverWebhook(ctx context.Context, url string, e Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return n.send(req)
}

// ntfyMessage is ntfy's JSON publishing format. JSON rather than the
// header form: HTTP headers cannot carry UTF-8 reliably (titles hold device
// names and dashes), and the click link and actions fit naturally in it.
type ntfyMessage struct {
	Topic    string       `json:"topic"`
	Title    string       `json:"title,omitempty"`
	Message  string       `json:"message"`
	Tags     []string     `json:"tags,omitempty"`
	Priority int          `json:"priority,omitempty"`
	Click    string       `json:"click,omitempty"`
	Actions  []ntfyAction `json:"actions,omitempty"`
}

type ntfyAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
}

// splitNtfyURL turns a topic URL (https://ntfy.sh/my-topic, or a
// self-hosted server under a path) into the server to POST to and the topic.
func splitNtfyURL(raw string) (server, topic string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	path := strings.TrimSuffix(u.Path, "/")
	i := strings.LastIndex(path, "/")
	if i < 0 || i == len(path)-1 {
		return "", "", fmt.Errorf("ntfy URL %q has no topic", raw)
	}
	topic = path[i+1:]
	u.Path = path[:i] + "/"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), topic, nil
}

func (n *Notifier) deliverNtfy(ctx context.Context, cfg config.NotificationsConfig, e Event) error {
	server, topic, err := splitNtfyURL(cfg.NtfyURL)
	if err != nil {
		return err
	}
	m := ntfyMessage{
		Topic:    topic,
		Title:    e.Title,
		Message:  e.Message,
		Priority: e.Priority,
		Click:    e.URL,
	}
	if tag, ok := ntfyTags[e.Type]; ok {
		m.Tags = []string{tag}
	}
	for _, a := range e.Actions {
		if len(m.Actions) == 3 { // ntfy's limit
			break
		}
		m.Actions = append(m.Actions, ntfyAction{Action: "view", Label: a.Label, URL: a.URL})
	}
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.NtfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)
	}
	return n.send(req)
}

func (n *Notifier) send(req *http.Request) error {
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("rejected: %s", resp.Status)
	}
	return nil
}

// UIBase is the address notification links point at: notifications.ui_url
// when set, else derived from api.listen — its host when specific, else
// this machine's LAN address (the one its default route uses).
func UIBase(cfg *config.Config) string {
	if u := cfg.Notifications.UIURL; u != "" {
		return strings.TrimSuffix(u, "/")
	}
	return DefaultUIBase(cfg.API.Listen)
}

// DefaultUIBase derives the UI address from the API listen address.
func DefaultUIBase(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = lanAddress()
	}
	if host == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(host, port)
}

// lanAddress is the local address of the default route. A UDP "dial"
// sends nothing; it only asks the kernel which source address it would use.
func lanAddress() string {
	c, err := net.Dial("udp", "192.0.2.1:9") // TEST-NET-1: never routed anywhere real
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok && !a.IP.IsLoopback() {
		return a.IP.String()
	}
	return ""
}

// inQuietHours reports whether t falls in a "HH:MM-HH:MM" window (local
// time), which may wrap midnight. An empty or malformed window is never
// quiet — validation rejects malformed ones up front.
func inQuietHours(window string, t time.Time) bool {
	from, to, ok := strings.Cut(window, "-")
	if !ok {
		return false
	}
	fh, fm, err1 := config.ParseClock(strings.TrimSpace(from))
	th, tm, err2 := config.ParseClock(strings.TrimSpace(to))
	if err1 != nil || err2 != nil {
		return false
	}
	start, end, now := fh*60+fm, th*60+tm, t.Hour()*60+t.Minute()
	if start == end {
		return false
	}
	if start < end {
		return now >= start && now < end
	}
	return now >= start || now < end
}
