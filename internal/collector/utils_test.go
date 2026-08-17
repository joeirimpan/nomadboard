package collector

import (
	"testing"
	"time"

	nomad "github.com/hashicorp/nomad/api"
)

func TestSummarizeChildren(t *testing.T) {
	now := time.Date(2026, 8, 17, 8, 30, 0, 0, time.UTC)
	child := func(status string, submittedAgo time.Duration) *nomad.JobListStub {
		return &nomad.JobListStub{
			Status:     status,
			SubmitTime: now.Add(-submittedAgo).UnixNano(),
		}
	}

	t.Run("no children", func(t *testing.T) {
		last, stuck := summarizeChildren(nil, now)
		if !last.IsZero() || !stuck.IsZero() {
			t.Errorf("got (%v, %v), want zero times", last, stuck)
		}
	})

	t.Run("all dead reports last run and no stuck", func(t *testing.T) {
		children := []*nomad.JobListStub{
			child(JobStatusDead, 48*time.Hour),
			child(JobStatusDead, 24*time.Hour),
		}
		last, stuck := summarizeChildren(children, now)
		if want := now.Add(-24 * time.Hour); !last.Equal(want) {
			t.Errorf("lastRun = %v, want %v", last, want)
		}
		if !stuck.IsZero() {
			t.Errorf("stuckSince = %v, want zero", stuck)
		}
	})

	t.Run("pending child reports oldest pending submit", func(t *testing.T) {
		children := []*nomad.JobListStub{
			child(JobStatusDead, 30*time.Hour),
			child(JobStatusPending, 8*time.Hour+30*time.Minute),
			child(JobStatusPending, 2*time.Hour),
		}
		last, stuck := summarizeChildren(children, now)
		if want := now.Add(-2 * time.Hour); !last.Equal(want) {
			t.Errorf("lastRun = %v, want %v", last, want)
		}
		if want := now.Add(-8*time.Hour - 30*time.Minute); !stuck.Equal(want) {
			t.Errorf("stuckSince = %v, want %v", stuck, want)
		}
	})

	t.Run("running child is not stuck", func(t *testing.T) {
		children := []*nomad.JobListStub{child(JobStatusRunning, 5*time.Minute)}
		_, stuck := summarizeChildren(children, now)
		if !stuck.IsZero() {
			t.Errorf("stuckSince = %v, want zero", stuck)
		}
	})

	t.Run("future submit ignored for stuck", func(t *testing.T) {
		children := []*nomad.JobListStub{child(JobStatusPending, -10*time.Minute)}
		_, stuck := summarizeChildren(children, now)
		if !stuck.IsZero() {
			t.Errorf("stuckSince = %v, want zero for future-dated submit", stuck)
		}
	})

	t.Run("zero submit time skipped", func(t *testing.T) {
		children := []*nomad.JobListStub{{Status: JobStatusPending, SubmitTime: 0}}
		last, stuck := summarizeChildren(children, now)
		if !last.IsZero() || !stuck.IsZero() {
			t.Errorf("got (%v, %v), want zero times", last, stuck)
		}
	})
}

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
