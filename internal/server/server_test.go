package server

import (
	"testing"

	"github.com/joeirimpan/nomadboard/internal/collector"
)

func TestCronSections(t *testing.T) {
	groups := []collector.GroupStatus{{Name: "Trade"}, {Name: "OMS"}}
	cron := func(group, ns, id string, h collector.Health) collector.CronStatus {
		return collector.CronStatus{Group: group, Job: collector.JobStatus{ID: id, Namespace: ns, Health: h}}
	}
	crons := []collector.CronStatus{
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
