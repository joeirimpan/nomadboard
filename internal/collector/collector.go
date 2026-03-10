package collector

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	nomad "github.com/hashicorp/nomad/api"
	"github.com/joeirimpan/nomadboard/internal/config"
)

// Collector periodically polls Nomad clusters and builds a Snapshot.
type Collector struct {
	cfg     config.Config
	clients map[string]*nomad.Client
	log     *slog.Logger

	mu       sync.RWMutex
	snapshot Snapshot

	subMu sync.Mutex
	subs  map[chan struct{}]struct{}
}

// New creates a Collector from the given config.
func New(cfg config.Config, log *slog.Logger) (*Collector, error) {
	clients := make(map[string]*nomad.Client, len(cfg.Clusters))

	for _, cl := range cfg.Clusters {
		ncfg := nomad.DefaultConfig()
		ncfg.Address = cl.Address

		if cl.TokenEnv != "" {
			if tok := os.Getenv(cl.TokenEnv); tok != "" {
				ncfg.SecretID = tok
			}
		}

		client, err := nomad.NewClient(ncfg)
		if err != nil {
			return nil, fmt.Errorf("creating nomad client for %s: %w", cl.Name, err)
		}
		clients[cl.Name] = client
	}

	return &Collector{
		cfg:     cfg,
		clients: clients,
		log:     log,
		subs:    make(map[chan struct{}]struct{}),
	}, nil
}

// Snapshot returns the current snapshot.
func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

// Subscribe returns a channel signalled on each new snapshot.
func (c *Collector) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	c.subMu.Lock()
	c.subs[ch] = struct{}{}
	c.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber.
func (c *Collector) Unsubscribe(ch chan struct{}) {
	c.subMu.Lock()
	delete(c.subs, ch)
	c.subMu.Unlock()
}

// notify signals all subscribers.
func (c *Collector) notify() {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for ch := range c.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Poll fetches data from all clusters immediately and updates the snapshot.
func (c *Collector) Poll() {
	c.poll()
}

// Run starts the polling loop, blocking until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.PollDuration())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.poll()
		}
	}
}

func (c *Collector) poll() {
	start := time.Now()

	type dcResult struct {
		dc     string
		jobs   []*nomad.JobListStub
		allocs []*nomad.AllocationListStub
		jobErr error
		alcErr error
	}

	var wg sync.WaitGroup
	results := make([]dcResult, len(c.cfg.Clusters))
	for i, cl := range c.cfg.Clusters {
		wg.Add(1)
		go func(idx int, dcName string, client *nomad.Client) {
			defer wg.Done()
			r := dcResult{dc: dcName}
			r.jobs, _, r.jobErr = client.Jobs().List(&nomad.QueryOptions{
				Namespace: "*",
			})
			r.allocs, _, r.alcErr = client.Allocations().List(&nomad.QueryOptions{
				Namespace: "*",
			})
			results[idx] = r
		}(i, cl.Name, c.clients[cl.Name])
	}
	wg.Wait()

	type jobKey struct {
		ns, name, dc string
	}
	type allocKey struct {
		ns, jobID, dc string
	}
	jobIndex := make(map[jobKey]*nomad.JobListStub)
	allocIndex := make(map[allocKey][]*nomad.AllocationListStub)
	for _, r := range results {
		if r.jobErr != nil {
			c.log.Error("failed to list jobs", "dc", r.dc, "err", r.jobErr)
			continue
		}
		for _, j := range r.jobs {
			jobIndex[jobKey{ns: j.Namespace, name: j.ID, dc: r.dc}] = j
		}
		if r.alcErr != nil {
			c.log.Error("failed to list allocations", "dc", r.dc, "err", r.alcErr)
			continue
		}
		for _, a := range r.allocs {
			k := allocKey{ns: a.Namespace, jobID: a.JobID, dc: r.dc}
			allocIndex[k] = append(allocIndex[k], a)
		}
	}

	restartWindow := c.cfg.RestartWindow()
	alertWindow := c.cfg.RestartAlertWindow()

	groups := make([]GroupStatus, 0, len(c.cfg.Groups))
	for _, grp := range c.cfg.Groups {
		gs := GroupStatus{
			Name:     grp.Name,
			DCHealth: make(map[string]Health),
		}

		for _, cl := range c.cfg.Clusters {
			gs.DCHealth[cl.Name] = Healthy
		}

		for _, pattern := range grp.Jobs {
			for _, cl := range c.cfg.Clusters {
				for key, stub := range jobIndex {
					if key.dc != cl.Name || key.ns != grp.Namespace {
						continue
					}
					matched, _ := filepath.Match(pattern, key.name)
					if !matched {
						continue
					}

					ak := allocKey{ns: grp.Namespace, jobID: key.name, dc: cl.Name}
				js := c.buildJobStatus(cl.Name, grp.Namespace, stub, allocIndex[ak], restartWindow, alertWindow)
					gs.Jobs = append(gs.Jobs, js)
					gs.TotalJobs++
					gs.TotalAllocs += len(js.Allocs)
					if js.Health == Healthy {
						gs.HealthyJobs++
					}

					// Update per-DC health (worst wins).
					if js.Health > gs.DCHealth[cl.Name] {
						gs.DCHealth[cl.Name] = js.Health
					}
				}
			}
		}

		sort.Slice(gs.Jobs, func(i, j int) bool {
			if gs.Jobs[i].DC != gs.Jobs[j].DC {
				return gs.Jobs[i].DC < gs.Jobs[j].DC
			}
			return gs.Jobs[i].Name < gs.Jobs[j].Name
		})

		gs.Health = Healthy
		for _, h := range gs.DCHealth {
			if h > gs.Health {
				gs.Health = h
			}
		}

		groups = append(groups, gs)
	}

	snap := Snapshot{
		Groups:    groups,
		UpdatedAt: time.Now(),
	}

	c.mu.Lock()
	c.snapshot = snap
	c.mu.Unlock()

	c.notify()

	c.log.Info("poll complete", "groups", len(groups), "duration", time.Since(start).Round(time.Millisecond))
}

// buildJobStatus computes health for a job using pre-fetched allocations.
func (c *Collector) buildJobStatus(dc, ns string, stub *nomad.JobListStub, allocs []*nomad.AllocationListStub, restartWindow, alertWindow time.Duration) JobStatus {
	js := JobStatus{
		ID:        stub.ID,
		Name:      stub.Name,
		Namespace: ns,
		Type:      stub.Type,
		Status:    stub.Status,
		DC:        dc,
		Periodic:  stub.Periodic,
		Health:    Healthy,
	}

	if stub.Periodic {
		if stub.JobSummary != nil && stub.JobSummary.Children != nil {
			js.ChildrenPending = int(stub.JobSummary.Children.Pending)
			js.ChildrenRunning = int(stub.JobSummary.Children.Running)
			js.ChildrenDead = int(stub.JobSummary.Children.Dead)
		}
		client := c.clients[dc]
		job, _, err := client.Jobs().Info(stub.ID, &nomad.QueryOptions{Namespace: ns})
		if err != nil {
			c.log.Error("failed to fetch periodic job info", "dc", dc, "job", stub.ID, "err", err)
		} else if job.Periodic != nil {
			if job.Periodic.Spec != nil && *job.Periodic.Spec != "" {
				js.Crons = []string{*job.Periodic.Spec}
			}
			if len(job.Periodic.Specs) > 0 {
				js.Crons = job.Periodic.Specs
			}
			if next, err := job.Periodic.Next(time.Now()); err == nil && !next.IsZero() {
				js.NextRun = next
			}
		}
	}

	now := time.Now()
	for _, alloc := range allocs {
		// Skip old/replaced allocations.
		if alloc.ClientStatus == nomad.AllocClientStatusComplete && stub.Type != "batch" {
			continue
		}

		nodeName := alloc.NodeName
		if c.cfg.MaskNodeIP {
			nodeName = maskNodeIP(nodeName)
		}

		as := AllocStatus{
			ID:        shortID(alloc.ID),
			TaskGroup: alloc.TaskGroup,
			NodeName:  nodeName,
			Status:    alloc.ClientStatus,
		}

		for taskName, ts := range alloc.TaskStates {
			task := TaskStatus{
				Name:       taskName,
				State:      ts.State,
				Failed:     ts.Failed,
				Restarts:   int(ts.Restarts),
				StartedAt:  ts.StartedAt,
				FinishedAt: ts.FinishedAt,
			}

			if !ts.LastRestart.IsZero() {
				task.LastRestart = ts.LastRestart
			}

			restartsInWindow := 0
			restartsInAlertWindow := 0
			for _, ev := range ts.Events {
				evTime := time.Unix(0, ev.Time)
				msg := ev.DisplayMessage
				if msg == "" {
					msg = ev.Message
				}
				task.Events = append(task.Events, TaskEvent{
					Type:    ev.Type,
					Time:    evTime,
					Message: msg,
				})

				if ev.Type == nomad.TaskRestarting {
					age := now.Sub(evTime)
					if age <= restartWindow {
						restartsInWindow++
					}
					if age <= alertWindow {
						restartsInAlertWindow++
					}
				}
			}

			// Newest first, capped at 50.
			for i, j := 0, len(task.Events)-1; i < j; i, j = i+1, j-1 {
				task.Events[i], task.Events[j] = task.Events[j], task.Events[i]
			}
			const maxEvents = 50
			if len(task.Events) > maxEvents {
				remaining := len(task.Events) - maxEvents
				task.Events = task.Events[:maxEvents]
				task.Events = append(task.Events, TaskEvent{
					Type:    "Truncated",
					Message: fmt.Sprintf("… %d more events", remaining),
				})
			}

			js.TotalRestarts += restartsInWindow
			if restartsInWindow > js.MaxRestarts {
				js.MaxRestarts = restartsInWindow
			}

			js.TotalAlertRestarts += restartsInAlertWindow
			if restartsInAlertWindow > js.MaxAlertRestarts {
				js.MaxAlertRestarts = restartsInAlertWindow
			}

			as.Tasks = append(as.Tasks, task)
		}

		// Stable display order.
		sort.Slice(as.Tasks, func(i, j int) bool {
			return as.Tasks[i].Name < as.Tasks[j].Name
		})

		js.Allocs = append(js.Allocs, as)
	}

	if stub.Status == JobStatusDead && stub.Type != "batch" {
		js.Health = Critical
	} else if js.MaxAlertRestarts >= c.cfg.RestartCrit {
		js.Health = Critical
	} else if js.MaxAlertRestarts >= c.cfg.RestartWarn {
		js.Health = Warning
	}

	for _, a := range js.Allocs {
		switch a.Status {
		case nomad.AllocClientStatusFailed, nomad.AllocClientStatusLost:
			js.Health = Critical
		case nomad.AllocClientStatusUnknown:
			if js.Health < Warning {
				js.Health = Warning
			}
		case nomad.AllocClientStatusPending:
			if js.Health < Warning {
				js.Health = Warning
			}
		case nomad.AllocClientStatusRunning:
			for _, t := range a.Tasks {
				if t.Failed {
					js.Health = Critical
				} else if t.State == TaskStatePending && t.Restarts > 0 {
					if js.Health < Warning {
						js.Health = Warning
					}
				} else if t.State == TaskStateDead && t.Restarts > 0 {
					if js.Health < Warning {
						js.Health = Warning
					}
				}
			}
		}
	}

	return js
}
