package collector

import "time"

type Health int

const (
	Healthy  Health = iota
	Warning
	Critical
)

// Nomad API doesn't export these constants.
const (
	TaskStatePending = "pending"
	TaskStateRunning = "running"
	TaskStateDead    = "dead"
)

const (
	JobStatusPending = "pending"
	JobStatusRunning = "running"
	JobStatusDead    = "dead"
)

func (h Health) String() string {
	switch h {
	case Warning:
		return "warning"
	case Critical:
		return "critical"
	default:
		return "healthy"
	}
}

// Snapshot is the full state produced by each poll cycle.
type Snapshot struct {
	Groups    []GroupStatus
	UpdatedAt time.Time
}

type GroupStatus struct {
	Name   string
	Jobs   []JobStatus
	Health Health

	DCHealth map[string]Health

	TotalJobs   int
	HealthyJobs int
	TotalAllocs int
}

type JobStatus struct {
	ID        string
	Name      string
	Namespace string
	Type      string // service, system, batch
	Status    string // running, dead, pending
	DC        string
	Allocs    []AllocStatus
	Health    Health

	Periodic        bool
	Crons           []string
	NextRun         time.Time
	ChildrenPending int
	ChildrenRunning int
	ChildrenDead    int

	// Restart counts within the display window (shown in UI).
	TotalRestarts int
	MaxRestarts   int

	// Restart counts within the alert window (used for health decisions).
	TotalAlertRestarts int
	MaxAlertRestarts   int
}

type AllocStatus struct {
	ID        string
	TaskGroup string
	NodeName  string
	Status    string // running, complete, failed
	Tasks     []TaskStatus
}

type TaskStatus struct {
	Name        string
	State       string // running, dead, pending
	Failed      bool
	Restarts    int
	LastRestart time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	Events      []TaskEvent
}

type TaskEvent struct {
	Type    string
	Time    time.Time
	Message string
}
