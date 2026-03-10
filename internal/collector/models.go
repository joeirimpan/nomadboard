package collector

import "time"

// Health represents the overall health of a group or job.
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

// Snapshot is the complete state of all groups, refreshed periodically.
type Snapshot struct {
	Groups    []GroupStatus
	UpdatedAt time.Time
}

// GroupStatus is the aggregated status for a logical group of jobs.
type GroupStatus struct {
	Name   string
	Jobs   []JobStatus
	Health Health

	DCHealth map[string]Health

	TotalJobs   int
	HealthyJobs int
	TotalAllocs int
}

// JobStatus is the status of a single Nomad job in a specific DC.
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

// AllocStatus is the status of a single allocation.
type AllocStatus struct {
	ID        string
	TaskGroup string
	NodeName  string
	Status    string // running, complete, failed
	Tasks     []TaskStatus
}

// TaskStatus is the status of a single task within an allocation.
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

// TaskEvent is a single lifecycle event for a task.
type TaskEvent struct {
	Type    string
	Time    time.Time
	Message string
}
