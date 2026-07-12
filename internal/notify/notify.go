// Package notify delivers curated, low-volume events — a new device on the
// network, an upstream tripping the breaker, an available update — to a
// generic webhook and/or an ntfy topic. Delivery is best-effort and always
// off the query hot path: publishers drop into a buffered channel and a
// single worker does the HTTP. Nothing is ever sent unless the user has
// configured a destination.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"minos/internal/config"
)

const (
	queueSize      = 64
	deliverTimeout = 10 * time.Second
)

// Event is one notification. Type is a stable machine-readable key; Title
// and Message are human wording (plain and literal, like all error text).
type Event struct {
	Type    string    `json:"type"`
	Title   string    `json:"title"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// ntfyTags maps event types to ntfy emoji tags.
var ntfyTags = map[string]string{
	"device_new":         "new",
	"upstream_sick":      "warning",
	"upstream_recovered": "white_check_mark",
	"update_available":   "arrow_up",
	"digest":             "bar_chart",
}

// Notifier fans events out to the configured sinks. Safe for concurrent use.
type Notifier struct {
	store  *config.Store
	ch     chan Event
	client *http.Client
}

func New(store *config.Store) *Notifier {
	return &Notifier{
		store:  store,
		ch:     make(chan Event, queueSize),
		client: &http.Client{Timeout: deliverTimeout},
	}
}

// Publish queues an event. Never blocks: with no sink configured or a full
// queue the event is dropped — notifications are a convenience, not a log.
func (n *Notifier) Publish(typ, title, message string) {
	cfg := n.store.Get().Notifications
	if cfg.WebhookURL == "" && cfg.NtfyURL == "" {
		return
	}
	select {
	case n.ch <- Event{Type: typ, Title: title, Message: message, Time: time.Now()}:
	default:
		slog.Debug("notification queue full, event dropped", "type", typ)
	}
}

// Run delivers queued events until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-n.ch:
			// Config is re-read per event so Settings changes apply live.
			cfg := n.store.Get().Notifications
			if cfg.WebhookURL != "" {
				n.deliverWebhook(ctx, cfg.WebhookURL, e)
			}
			if cfg.NtfyURL != "" {
				n.deliverNtfy(ctx, cfg, e)
			}
		}
	}
}

// deliverWebhook POSTs the event as JSON. Best-effort: failures are logged
// at debug and not retried.
func (n *Notifier) deliverWebhook(ctx context.Context, url string, e Event) {
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	n.send(req, "webhook", e.Type)
}

// deliverNtfy POSTs the message body with ntfy's Title/Tags headers.
func (n *Notifier) deliverNtfy(ctx context.Context, cfg config.NotificationsConfig, e Event) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.NtfyURL,
		strings.NewReader(e.Message))
	if err != nil {
		return
	}
	req.Header.Set("Title", e.Title)
	if tag, ok := ntfyTags[e.Type]; ok {
		req.Header.Set("Tags", tag)
	}
	if cfg.NtfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)
	}
	n.send(req, "ntfy", e.Type)
}

func (n *Notifier) send(req *http.Request, sink, typ string) {
	resp, err := n.client.Do(req)
	if err != nil {
		slog.Debug("notification delivery failed", "sink", sink, "type", typ, "err", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Debug("notification delivery rejected",
			"sink", sink, "type", typ, "status", resp.Status)
	}
}
