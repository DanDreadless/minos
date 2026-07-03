// Package lists fetches remote blocklists, parses the supported formats,
// and rebuilds the filter matcher on a schedule. List content is untrusted
// input: parsers must survive junk bytes, absurd line lengths, and unicode
// without failing — bad lines are counted and skipped, never fatal.
package lists

import (
	"bufio"
	"io"
	"strings"

	"minos/internal/filter"
)

// maxLineBytes bounds a single parsed line; longer lines (a 10 MB "line" is
// hostile or garbage) are consumed, counted as skipped, and never buffered.
const maxLineBytes = 4096

// Stats reports what a parse pass did.
type Stats struct {
	Rules   int // entries handed to the builder
	Skipped int // lines dropped as invalid, unsupported, or oversized
}

// hostsLocalNames are the boilerplate entries every hosts file carries;
// blocking these would break the local machine, so they are ignored.
var hostsLocalNames = map[string]struct{}{
	"localhost":             {},
	"localhost.localdomain": {},
	"local":                 {},
	"broadcasthost":         {},
	"ip6-localhost":         {},
	"ip6-loopback":          {},
	"ip6-localnet":          {},
	"ip6-mcastprefix":       {},
	"ip6-allnodes":          {},
	"ip6-allrouters":        {},
	"ip6-allhosts":          {},
}

// Parse reads one list in the given format ("hosts", "plain", "adblock")
// and feeds entries into the builder under the list name. It only returns
// an error for a broken reader, never for bad content.
func Parse(format, list string, r io.Reader, b *filter.Builder) (Stats, error) {
	var stats Stats
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, tooLong, err := readLine(br)
		if tooLong {
			stats.Skipped++
		} else if line != "" || err == nil {
			switch format {
			case "hosts":
				parseHostsLine(list, line, b, &stats)
			case "adblock":
				parseAdblockLine(list, line, b, &stats)
			default: // plain
				parsePlainLine(list, line, b, &stats)
			}
		}
		if err == io.EOF {
			return stats, nil
		}
		if err != nil {
			return stats, err
		}
	}
}

// readLine reads one line of any length. Lines beyond maxLineBytes are
// consumed but reported tooLong with no content, so a hostile source cannot
// balloon memory.
func readLine(br *bufio.Reader) (string, bool, error) {
	var sb strings.Builder
	tooLong := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if !tooLong {
			if sb.Len()+len(chunk) > maxLineBytes {
				tooLong = true
				sb.Reset()
			} else {
				sb.Write(chunk)
			}
		}
		if err != nil || !isPrefix {
			return sb.String(), tooLong, err
		}
	}
}

func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

func parseHostsLine(list, line string, b *filter.Builder, stats *Stats) {
	line = stripComment(line)
	if line == "" {
		return
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		stats.Skipped++
		return
	}
	switch fields[0] {
	case "0.0.0.0", "127.0.0.1", "::", "::1", "0:0:0:0:0:0:0:0", "0:0:0:0:0:0:0:1":
	default:
		// A real hosts mapping (e.g. "192.168.1.5 nas.lan"), not a block entry.
		stats.Skipped++
		return
	}
	for _, host := range fields[1:] {
		if _, local := hostsLocalNames[strings.ToLower(host)]; local {
			continue
		}
		if filter.NormalizeDomain(host) == "" {
			stats.Skipped++
			continue
		}
		b.AddDeny(list, host)
		stats.Rules++
	}
}

func parsePlainLine(list, line string, b *filter.Builder, stats *Stats) {
	line = stripComment(line)
	if line == "" {
		return
	}
	if filter.NormalizeDomain(line) == "" {
		stats.Skipped++
		return
	}
	b.AddDeny(list, line)
	stats.Rules++
}

func parseAdblockLine(list, line string, b *filter.Builder, stats *Stats) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "[") {
		return
	}
	// The filter package owns AdBlock classification; infer the outcome
	// from its skip counter.
	skippedBefore := b.SkippedCount()
	b.ParseAdblockLine(list, trimmed)
	if b.SkippedCount() > skippedBefore {
		stats.Skipped++
	} else {
		stats.Rules++
	}
}
