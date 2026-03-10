package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	content := `
clusters::
  - ::
    name: "dc1"
    address: "http://localhost:4646"
    token_env: "TEST_TOKEN"

poll_interval: 15
restart_window: "12h"
restart_warn: 2
restart_crit: 10
listen: ":8080"

groups::
  - ::
    name: "My App"
    namespace: "default"
    jobs::
      - "web"
      - "api-*"
  - ::
    name: "Infra"
    namespace: "ops"
    jobs::
      - "consul"
      - "vault"
`
	f, err := os.CreateTemp("", "config-*.huml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(cfg.Clusters))
	}
	if cfg.Clusters[0].Name != "dc1" {
		t.Fatalf("expected cluster name dc1, got %s", cfg.Clusters[0].Name)
	}
	if cfg.Clusters[0].Address != "http://localhost:4646" {
		t.Fatalf("expected address http://localhost:4646, got %s", cfg.Clusters[0].Address)
	}

	if cfg.PollDuration() != 15*time.Second {
		t.Fatalf("expected 15s poll interval, got %s", cfg.PollDuration())
	}
	if cfg.RestartWindow() != 12*time.Hour {
		t.Fatalf("expected 12h restart window, got %s", cfg.RestartWindow())
	}
	if cfg.RestartWarn != 2 {
		t.Fatalf("expected restart_warn 2, got %d", cfg.RestartWarn)
	}
	if cfg.RestartCrit != 10 {
		t.Fatalf("expected restart_crit 10, got %d", cfg.RestartCrit)
	}
	if cfg.Listen != ":8080" {
		t.Fatalf("expected listen :8080, got %s", cfg.Listen)
	}

	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Groups))
	}
	if cfg.Groups[0].Name != "My App" {
		t.Fatalf("expected group name 'My App', got %s", cfg.Groups[0].Name)
	}
	if len(cfg.Groups[0].Jobs) != 2 {
		t.Fatalf("expected 2 jobs in group 0, got %d", len(cfg.Groups[0].Jobs))
	}
	if cfg.Groups[0].Jobs[0] != "web" {
		t.Fatalf("expected first job 'web', got %s", cfg.Groups[0].Jobs[0])
	}
	if cfg.Groups[0].Jobs[1] != "api-*" {
		t.Fatalf("expected second job 'api-*', got %s", cfg.Groups[0].Jobs[1])
	}
}

func TestLoadErrors(t *testing.T) {
	// Missing file.
	if _, err := Load("/nonexistent"); err == nil {
		t.Fatal("expected error for missing file")
	}

	// No clusters.
	f, _ := os.CreateTemp("", "config-*.huml")
	defer os.Remove(f.Name())
	f.WriteString(`groups::
  - ::
    name: "x"
    namespace: "y"
    jobs::
      - "z"
`)
	f.Close()
	if _, err := Load(f.Name()); err == nil {
		t.Fatal("expected error for missing clusters")
	}
}
