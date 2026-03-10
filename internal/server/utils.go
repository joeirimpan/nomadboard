package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/joeirimpan/nomadboard/internal/collector"
)

// reRefreshTimestamp is stripped before hashing so identical data across polls
// doesn't push duplicate SSE events. Task-level timestamps are unaffected.
var reRefreshTimestamp = regexp.MustCompile(`(class="refresh-note">\s*)\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [A-Z]+`)

func contentHash(html string) [32]byte {
	return sha256.Sum256([]byte(reRefreshTimestamp.ReplaceAllString(html, "${1}")))
}

// writeSSEEvent writes a single SSE event, splitting multi-line data per spec.
func writeSSEEvent(w http.ResponseWriter, html string) {
	for _, line := range strings.Split(html, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprintf(w, "\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func healthClass(h collector.Health) string {
	switch h {
	case collector.Warning:
		return "warning"
	case collector.Critical:
		return "critical"
	default:
		return "healthy"
	}
}

func healthIcon(h collector.Health) string {
	switch h {
	case collector.Warning:
		return "⚠️"
	case collector.Critical:
		return "✖"
	default:
		return "✓"
	}
}

func statusClass(status string) string {
	switch status {
	case "running":
		return "healthy"
	case "pending":
		return "warning"
	default:
		return "critical"
	}
}

func typeBadge(t string) string {
	switch t {
	case "system":
		return "secondary"
	case "batch":
		return "outline"
	default:
		return ""
	}
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		s = fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		s = fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		s = fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		s = fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

func fmtTimeIn(loc *time.Location) func(time.Time) string {
	return func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.In(loc).Format("2006-01-02 15:04:05 MST")
	}
}

func slugify(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, " ", "-"))
}

func dcShort(dc string) string {
	return dc
}
