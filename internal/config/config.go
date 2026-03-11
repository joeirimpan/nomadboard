package config

import (
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	huml "github.com/huml-lang/go-huml"
)

// Cluster is a Nomad cluster endpoint.
type Cluster struct {
	Name     string `huml:"name"`
	Address  string `huml:"address"`
	TokenEnv string `huml:"token_env"`
}

// Group is a logical grouping of Nomad jobs.
// Lower priority numbers appear first; nil sorts to the end.
type Group struct {
	Name       string   `huml:"name"`
	Namespace  string   `huml:"namespace"`
	Namespaces []string `huml:"namespaces"`
	Jobs       []string `huml:"jobs"`
	Priority   *int     `huml:"priority"`
}

// EffectiveNamespaces resolves the namespace list.
// Plural field takes precedence; falls back to singular, then "default".
func (g Group) EffectiveNamespaces() []string {
	if len(g.Namespaces) > 0 {
		return g.Namespaces
	}
	if g.Namespace != "" {
		return []string{g.Namespace}
	}
	return []string{"default"}
}

// effectivePriority returns the sort key. Nil priority sorts to the end.
func (g Group) effectivePriority() int {
	if g.Priority == nil {
		return math.MaxInt
	}
	return *g.Priority
}

// Config is the top-level application configuration.
type Config struct {
	Name            string    `huml:"name"`
	Clusters        []Cluster `huml:"clusters"`
	PollInterval    int64     `huml:"poll_interval"`
	RestartWin      string    `huml:"restart_window"`
	RestartAlertWin string    `huml:"restart_alert_window"`
	RestartWarn     int       `huml:"restart_warn"`
	RestartCrit     int       `huml:"restart_crit"`
	PerPage         int       `huml:"per_page"`
	MaskNodeIP      bool      `huml:"mask_node_ip"`
	Timezone        string    `huml:"timezone"`
	MaxSSEConns     int       `huml:"max_sse_conns"`
	Listen          string    `huml:"listen"`
	Groups          []Group   `huml:"groups"`

	tz *time.Location
}

// Location returns the configured timezone. Defaults to UTC.
func (c Config) Location() *time.Location {
	if c.tz != nil {
		return c.tz
	}
	return time.UTC
}

// DisplayName returns the configured name, falling back to "Nomad Pulse".
func (c Config) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return "Nomad Pulse"
}

// RestartWindow returns the display restart window. Defaults to 24h.
func (c Config) RestartWindow() time.Duration {
	d, err := time.ParseDuration(c.RestartWin)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}

// RestartAlertWindow returns the shorter window used for health decisions. Defaults to 30m.
func (c Config) RestartAlertWindow() time.Duration {
	if c.RestartAlertWin == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(c.RestartAlertWin)
	if err != nil {
		return 30 * time.Minute
	}
	return d
}

// PollDuration returns the poll interval. Defaults to 30s.
func (c Config) PollDuration() time.Duration {
	if c.PollInterval <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.PollInterval) * time.Second
}

// Load reads and parses a config file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := huml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}

	if len(cfg.Clusters) == 0 {
		return Config{}, fmt.Errorf("no clusters defined in config")
	}
	if len(cfg.Groups) == 0 {
		return Config{}, fmt.Errorf("no groups defined in config")
	}
	if cfg.Listen == "" {
		cfg.Listen = ":9999"
	}
	if cfg.PerPage <= 0 {
		cfg.PerPage = 20
	}
	if cfg.MaxSSEConns <= 0 {
		cfg.MaxSSEConns = 128
	}
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return Config{}, fmt.Errorf("invalid timezone %q: %w", cfg.Timezone, err)
		}
		cfg.tz = loc
	}

	// Sort by priority, preserving config order for ties.
	sort.SliceStable(cfg.Groups, func(i, j int) bool {
		return cfg.Groups[i].effectivePriority() < cfg.Groups[j].effectivePriority()
	})

	return cfg, nil
}
