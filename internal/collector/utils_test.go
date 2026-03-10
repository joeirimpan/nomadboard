package collector

import "testing"

func TestMaskNodeIP(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Dash-separated IP.
		{"myapp-aux-777-123-123-123", "myapp-aux-***"},
		{"myapp-svc-10-0-1-5", "myapp-svc-***"},
		{"node-192-168-1-100", "node-***"},
		{"192-168-1-1", "***"},
		{"a-1-2-3-4", "a-***"},

		// Dot-separated IP.
		{"myapp-aux-10.0.1.5", "myapp-aux-***"},
		{"myapp.10.0.1.5", "myapp-***"},
		{"10.0.1.5", "***"},
		{"node-192.168.1.100", "node-***"},

		// FQDN with domain suffix after IP.
		{"ip-10-0-1-50.ap-south-1.compute.internal", "ip-***"},
		{"ip-10-0-1-50.ec2.internal", "ip-***"},
		{"node-10.0.1.5.example.com", "node-***"},

		// No IP suffix.
		{"myapp-aux", "myapp-aux"},
		{"simple-node", "simple-node"},
		{"node-with-3-segments-only", "node-with-3-segments-only"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := maskNodeIP(tt.input)
			if got != tt.want {
				t.Errorf("maskNodeIP(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
