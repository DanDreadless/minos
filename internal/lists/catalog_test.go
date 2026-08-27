//go:build catalog

// Catalog verification — run against the live internet, never in normal CI.
//
//	go test -tags catalog -timeout 20m -v ./internal/lists -run TestCatalog
//
// The curated catalog in web/src/lib/blocklists.ts is a set of one-click
// subscribes pointing at third-party URLs that move without warning. A
// HEAD probe (what this replaces) proves only that something answers 200.
// It cannot see the three failures that actually matter:
//
//   - the list still resolves but its content changed — repurposed,
//     emptied, or truncated. A list that compiles to zero rules is worse
//     than a 404: the user sees a healthy subscription and believes they
//     are protected.
//   - the publisher switched format (plain → AdBlock, say). Every line
//     then lands in the skipped counter instead of failing loudly.
//   - the advertised size drifted from reality, so the card lies about
//     what subscribing costs — which matters on a Pi's memory budget.
//
// So this compiles each list through the real pipeline (lists.Parse into
// a filter.Builder) and holds it to the standard the catalog header sets:
// it parses, nothing is skipped, and the rule count is near the hint.
package lists

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"minos/internal/filter"
)

// sizeTolerance is how far the compiled rule count may drift from the
// catalog's hint before it counts as rot. Blocklists move by a few percent
// a month, so this is loose enough not to cry wolf weekly and tight enough
// that a list quietly halving or doubling is caught.
const sizeTolerance = 0.25

type catalogEntry struct {
	id, size, name, url, format string
}

// entryRe pulls the fields this check needs out of the TypeScript catalog.
// Parsing TS from Go is not elegant, but the alternative — a second copy of
// the catalog in another format — is a source of truth that can disagree
// with the one the UI actually ships.
var entryRe = regexp.MustCompile(`(?s)id:\s*'([^']*)'.*?size:\s*'([^']*)'.*?list:\s*\{\s*name:\s*'([^']*)',\s*url:\s*'([^']*)',\s*format:\s*'([^']*)'`)

func loadCatalog(t *testing.T) []catalogEntry {
	t.Helper()
	path := filepath.Join("..", "..", "web", "src", "lib", "blocklists.ts")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	matches := entryRe.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatal("no catalog entries parsed — has the file's shape changed?")
	}
	// A non-greedy scan can silently pair fields across entries if one is
	// missing, so hold the count to the number of ids actually present.
	if want := strings.Count(string(src), "\n    id: "); want != len(matches) {
		t.Fatalf("parsed %d entries but the catalog declares %d — the regex "+
			"and the file have drifted apart", len(matches), want)
	}
	out := make([]catalogEntry, 0, len(matches))
	for _, m := range matches {
		out = append(out, catalogEntry{id: m[1], size: m[2], name: m[3], url: m[4], format: m[5]})
	}
	return out
}

// parseSizeHint turns "≈181k domains" or "<1k domains" into the expected
// rule count. The bool reports whether the hint is an upper bound only
// ("<1k"), where anything non-empty below the cap is fine.
func parseSizeHint(s string) (n int, upperBoundOnly bool, err error) {
	upperBoundOnly = strings.Contains(s, "<")
	m := regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*([kKmM]?)`).FindStringSubmatch(s)
	if m == nil {
		return 0, false, fmt.Errorf("no number in size hint %q", s)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false, fmt.Errorf("size hint %q: %w", s, err)
	}
	switch strings.ToLower(m[2]) {
	case "k":
		v *= 1_000
	case "m":
		v *= 1_000_000
	}
	return int(v), upperBoundOnly, nil
}

func fetchList(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxListBytes {
		return nil, fmt.Errorf("exceeds the %d byte cap", maxListBytes)
	}
	return body, nil
}

func TestCatalogListsCompile(t *testing.T) {
	entries := loadCatalog(t)
	t.Logf("verifying %d catalog entries", len(entries))
	for _, e := range entries {
		t.Run(e.id, func(t *testing.T) {
			body, err := fetchList(t.Context(), e.url)
			if err != nil {
				t.Fatalf("fetch %s: %v\n  URL: %s", e.name, err, e.url)
			}

			b := filter.NewBuilder()
			stats, err := Parse(e.format, e.name, false, strings.NewReader(string(body)), b)
			if err != nil {
				t.Fatalf("parse %s as %q: %v", e.name, e.format, err)
			}

			want, upperOnly, err := parseSizeHint(e.size)
			if err != nil {
				t.Fatalf("%s: %v", e.name, err)
			}
			t.Logf("%-22s %8d rules  %6d skipped  %9d bytes  hint %s",
				e.name, stats.Rules, stats.Skipped, len(body), e.size)

			if stats.Rules == 0 {
				t.Errorf("%s compiled to ZERO rules — the subscription would silently "+
					"protect nothing. Content or format has changed.\n  URL: %s", e.name, e.url)
			}
			// The catalog header requires a clean parse. Skipped lines mean
			// the publisher's format no longer matches the recorded one.
			if stats.Skipped > 0 {
				t.Errorf("%s skipped %d lines parsing as %q — the format has probably "+
					"changed.\n  URL: %s", e.name, stats.Skipped, e.format, e.url)
			}

			switch {
			case upperOnly:
				if stats.Rules > want {
					t.Errorf("%s has %d rules but the catalog says %q — update the hint",
						e.name, stats.Rules, e.size)
				}
			case want > 0:
				drift := math.Abs(float64(stats.Rules)-float64(want)) / float64(want)
				if drift > sizeTolerance {
					t.Errorf("%s has %d rules, %.0f%% off the catalog's %q — update the "+
						"hint in web/src/lib/blocklists.ts", e.name, stats.Rules, drift*100, e.size)
				}
			}
		})
	}
}
