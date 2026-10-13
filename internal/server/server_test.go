package server

import (
	"slices"
	"testing"
	"time"

	"github.com/joeirimpan/nomadboard/internal/collector"
)

func TestCronSections(t *testing.T) {
	groups := []collector.GroupStatus{{Name: "Trade"}, {Name: "OMS"}}
	cron := func(group, ns, id string, h collector.Health) collector.GroupedJob {
		return collector.GroupedJob{Group: group, Job: collector.JobStatus{ID: id, Namespace: ns, Health: h}}
	}
	crons := []collector.GroupedJob{
		cron("OMS", "kite", "oms-a", collector.Critical),
		cron("", "ops", "restart", collector.Healthy),
		cron("Trade", "kite", "trade-a", collector.Healthy),
		cron("", "kite", "stray", collector.Healthy),
		cron("OMS", "kite", "oms-b", collector.Healthy),
	}

	got := cronSections(groups, crons)
	want := []struct {
		name, slug string
		n, healthy int
		health     collector.Health
	}{
		{"Trade", "trade", 1, 1, collector.Healthy},
		{"OMS", "oms", 2, 1, collector.Critical},
		{"kite", "", 1, 1, collector.Healthy},
		{"ops", "", 1, 1, collector.Healthy},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sections, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Name != w.name || g.GroupSlug != w.slug || len(g.Crons) != w.n || g.Healthy != w.healthy || g.Health != w.health {
			t.Errorf("section %d = {%s %q n=%d healthy=%d %v}, want %+v", i, g.Name, g.GroupSlug, len(g.Crons), g.Healthy, g.Health, w)
		}
	}
}

func TestAllJobsIssuesRestarts(t *testing.T) {
	job := func(id string, h collector.Health, restarts int) collector.JobStatus {
		return collector.JobStatus{ID: id, Name: id, Namespace: "kite", DC: "dc1", Health: h, TotalRestarts: restarts}
	}
	snap := collector.Snapshot{
		Groups: []collector.GroupStatus{
			{Name: "A", Jobs: []collector.JobStatus{job("web", collector.Warning, 2), job("api", collector.Healthy, 0)}},
			{Name: "B", Jobs: []collector.JobStatus{job("web", collector.Warning, 2), job("db", collector.Critical, 7)}},
		},
		Crons: []collector.GroupedJob{{Job: job("cron", collector.Warning, 2)}},
	}

	all := allJobs(snap)
	if len(all) != 4 {
		t.Fatalf("allJobs: got %d, want 4", len(all))
	}
	if all[0].Job.ID != "web" || all[0].Group != "A" {
		t.Errorf("allJobs[0] = %s/%s, want web/A", all[0].Job.ID, all[0].Group)
	}

	ids := func(jobs []collector.GroupedJob) []string {
		var out []string
		for _, gj := range jobs {
			out = append(out, gj.Job.ID)
		}
		return out
	}
	if got, want := ids(issueJobs(all)), []string{"db", "cron", "web"}; !slices.Equal(got, want) {
		t.Errorf("issueJobs = %v, want %v", got, want)
	}
	if got, want := ids(restartJobs(all)), []string{"db", "cron", "web"}; !slices.Equal(got, want) {
		t.Errorf("restartJobs = %v, want %v", got, want)
	}
}

func TestShortDuration(t *testing.T) {
	tests := map[time.Duration]string{
		24 * time.Hour:   "24h",
		30 * time.Minute: "30m",
		90 * time.Minute: "1h30m",
		45 * time.Second: "45s",
	}
	for d, want := range tests {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
