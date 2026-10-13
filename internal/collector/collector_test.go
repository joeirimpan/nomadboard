package collector

import (
	"io"
	"log/slog"
	"testing"
	"time"

	nomad "github.com/hashicorp/nomad/api"
	"github.com/joeirimpan/nomadboard/internal/config"
)

// testCollector builds a Collector with no Nomad clients. buildJobStatus skips
// the periodic Jobs().Info enrichment when the client is absent.
func testCollector() *Collector {
	return &Collector{
		cfg: config.Config{
			RestartWarn: 1,
			RestartCrit: 5,
		},
		clients: map[string]*nomad.Client{},
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func periodicParent() *nomad.JobListStub {
	return &nomad.JobListStub{
		ID:        "batch-job",
		Name:      "batch-job",
		Namespace: "default",
		Type:      "batch",
		Status:    JobStatusRunning,
		Periodic:  true,
	}
}

func TestBuildJobStatusPeriodicPendingChild(t *testing.T) {
	const alertWindow = 30 * time.Minute
	now := time.Now()

	child := func(status string, age time.Duration) *nomad.JobListStub {
		return &nomad.JobListStub{
			ID:         "batch-job/periodic-1",
			ParentID:   "batch-job",
			Namespace:  "default",
			Type:       "batch",
			Status:     status,
			SubmitTime: now.Add(-age).UnixNano(),
		}
	}

	tests := []struct {
		name     string
		children []*nomad.JobListStub
		want     Health
	}{
		{
			name:     "no children is healthy",
			children: nil,
			want:     Healthy,
		},
		{
			name:     "completed child is healthy",
			children: []*nomad.JobListStub{child(JobStatusDead, 6*time.Hour)},
			want:     Healthy,
		},
		{
			name:     "running child is healthy",
			children: []*nomad.JobListStub{child(JobStatusRunning, 2*time.Minute)},
			want:     Healthy,
		},
		{
			// Within the alert window a queued child is still normal scheduling lag.
			name:     "recently queued child is healthy",
			children: []*nomad.JobListStub{child(JobStatusPending, 2*time.Minute)},
			want:     Healthy,
		},
		{
			name:     "queued past alert window is critical",
			children: []*nomad.JobListStub{child(JobStatusPending, 45*time.Minute)},
			want:     Critical,
		},
		{
			name:     "long-queued child is critical",
			children: []*nomad.JobListStub{child(JobStatusPending, 8*time.Hour+30*time.Minute)},
			want:     Critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testCollector()
			js := c.buildJobStatus("dc1", "default", periodicParent(), nil, tt.children, 4*time.Hour, alertWindow)
			if js.Health != tt.want {
				t.Errorf("Health = %v, want %v", js.Health, tt.want)
			}
		})
	}
}

// Service jobs must not be affected by the queued-child logic.
func TestBuildJobStatusServiceUnaffected(t *testing.T) {
	c := testCollector()
	stub := &nomad.JobListStub{
		ID:        "web",
		Name:      "web",
		Namespace: "default",
		Type:      "service",
		Status:    JobStatusRunning,
	}
	allocs := []*nomad.AllocationListStub{{
		ID:           "alloc-1",
		TaskGroup:    "app",
		ClientStatus: nomad.AllocClientStatusRunning,
		TaskStates: map[string]*nomad.TaskState{
			"app": {State: TaskStateRunning},
		},
	}}

	js := c.buildJobStatus("dc1", "default", stub, allocs, nil, 4*time.Hour, 30*time.Minute)
	if js.Health != Healthy {
		t.Errorf("Health = %v, want Healthy", js.Health)
	}
	if !js.StuckSince.IsZero() {
		t.Errorf("StuckSince = %v, want zero for non-periodic job", js.StuckSince)
	}
}

// A stuck queued child outranks a lesser restart-based warning.
func TestBuildJobStatusStuckOutranksRestartWarning(t *testing.T) {
	now := time.Now()
	c := testCollector()

	children := []*nomad.JobListStub{{
		ID:         "batch-job/periodic-1",
		ParentID:   "batch-job",
		Namespace:  "default",
		Type:       "batch",
		Status:     JobStatusPending,
		SubmitTime: now.Add(-3 * time.Hour).UnixNano(),
	}}

	// One restart in the alert window would normally yield Warning.
	allocs := []*nomad.AllocationListStub{{
		ID:           "alloc-1",
		TaskGroup:    "app",
		ClientStatus: nomad.AllocClientStatusComplete,
		TaskStates: map[string]*nomad.TaskState{
			"app": {
				State:    TaskStateDead,
				Restarts: 1,
				Events: []*nomad.TaskEvent{
					{Type: nomad.TaskRestarting, Time: now.Add(-5 * time.Minute).UnixNano()},
				},
			},
		},
	}}

	js := c.buildJobStatus("dc1", "default", periodicParent(), allocs, children, 4*time.Hour, 30*time.Minute)
	if js.Health != Critical {
		t.Errorf("Health = %v, want Critical (queued child must not be downgraded)", js.Health)
	}
}

func TestBuildCrons(t *testing.T) {
	c := testCollector()

	grouped := periodicParent()
	ungrouped := periodicParent()
	ungrouped.ID, ungrouped.Name, ungrouped.Namespace = "a-ops-cron", "a-ops-cron", "ops"
	service := &nomad.JobListStub{ID: "web", Name: "web", Namespace: "default", Type: "service", Status: JobStatusRunning}

	jobIndex := map[jobKey]*nomad.JobListStub{
		{ns: "default", name: "batch-job", dc: "dc1"}: grouped,
		{ns: "ops", name: "a-ops-cron", dc: "dc1"}:    ungrouped,
		{ns: "default", name: "web", dc: "dc1"}:       service,
	}
	groupedKey := jobKey{ns: "default", name: "batch-job", dc: "dc1"}
	inGroup := map[jobKey]GroupedJob{
		groupedKey: {Group: "Batch", Job: JobStatus{ID: "batch-job", Name: "batch-job", Namespace: "default", DC: "dc1", Periodic: true}},
	}

	got := c.buildCrons(jobIndex, nil, nil, inGroup, time.Hour, 30*time.Minute)
	if len(got) != 2 {
		t.Fatalf("got %d crons, want 2", len(got))
	}
	if got[0].Job.ID != "a-ops-cron" || got[0].Group != "" || got[0].Job.Namespace != "ops" {
		t.Errorf("got[0] = %+v, want ungrouped a-ops-cron in ops", got[0])
	}
	if got[1].Job.ID != "batch-job" || got[1].Group != "Batch" {
		t.Errorf("got[1] = %+v, want batch-job in group Batch", got[1])
	}
}

func TestUnmatchedJobs(t *testing.T) {
	stub := func(ns, id string) *nomad.JobListStub {
		return &nomad.JobListStub{ID: id, Name: id, Namespace: ns, Type: "service", Status: JobStatusRunning}
	}
	jobIndex := map[jobKey]*nomad.JobListStub{
		{ns: "kite", name: "web", dc: "dc1"}:    stub("kite", "web"),
		{ns: "ops", name: "restart", dc: "dc1"}: stub("ops", "restart"),
		{ns: "kite", name: "api", dc: "dc1"}:    stub("kite", "api"),
	}
	matched := map[jobKey]bool{{ns: "kite", name: "web", dc: "dc1"}: true}

	got := unmatchedJobs(jobIndex, matched)
	if len(got) != 2 {
		t.Fatalf("got %d jobs, want 2", len(got))
	}
	if got[0].ID != "api" || got[1].ID != "restart" {
		t.Errorf("got %s, %s; want api, restart", got[0].ID, got[1].ID)
	}
}
