package collector

import "regexp"

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
