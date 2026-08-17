package collector

import (
	"regexp"
	"time"

	nomad "github.com/hashicorp/nomad/api"
)

// summarizeChildren returns the newest child's submit time and the oldest
// still-pending child's submit time (zero if none are pending).
func summarizeChildren(children []*nomad.JobListStub, now time.Time) (lastRun, stuckSince time.Time) {
	for _, ch := range children {
		if ch.SubmitTime == 0 {
			continue
		}
		submitted := time.Unix(0, ch.SubmitTime)
		if submitted.After(lastRun) {
			lastRun = submitted
		}
		// Clock skew would otherwise yield a negative age and mask the alert.
		if ch.Status == JobStatusPending && !submitted.After(now) {
			if stuckSince.IsZero() || submitted.Before(stuckSince) {
				stuckSince = submitted
			}
		}
	}
	return lastRun, stuckSince
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// reNodeIP matches a trailing IP-like pattern (4 numeric segments separated by
// dots or dashes), with an optional domain suffix.
// "myapp-10-0-1-5" -> "myapp-***", "ip-10-0-1-50.ec2.internal" -> "ip-***"
var reNodeIP = regexp.MustCompile(`[.\-]?\d+[.\-]\d+[.\-]\d+[.\-]\d+([.\-][a-zA-Z][\w.\-]*)?$`)

// maskNodeIP strips trailing IP-like octets from a node name.
func maskNodeIP(name string) string {
	loc := reNodeIP.FindStringIndex(name)
	if loc == nil {
		return name
	}
	prefix := name[:loc[0]]
	if prefix == "" {
		return "***"
	}
	return prefix + "-***"
}
