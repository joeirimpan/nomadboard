package server

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/knadh/paginator"
	"github.com/joeirimpan/nomadboard/internal/collector"
	"github.com/joeirimpan/nomadboard/internal/config"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server serves the status dashboard over HTTP.
type Server struct {
	cfg       config.Config
	collector *collector.Collector
	log       *slog.Logger
	tpls      map[string]*template.Template
	frags     map[string]*template.Template // content-only fragment templates for SSE
	pg        *paginator.Paginator
	mux       *http.ServeMux
	sseSem    chan struct{} // limits concurrent SSE connections
}

func baseFuncMap(cfg config.Config) template.FuncMap {
	return template.FuncMap{
		"add":         func(a, b int) int { return a + b },
		"subtract":    func(a, b int) int { return a - b },
		"healthClass": healthClass,
		"healthIcon":  healthIcon,
		"statusClass": statusClass,
		"typeBadge":   typeBadge,
		"timeAgo":     timeAgo,
		"fmtTime":     fmtTimeIn(cfg.Location()),
		"slugify":     slugify,
		"sortedDCs": func() []string {
			dcs := make([]string, len(cfg.Clusters))
			for i, cl := range cfg.Clusters {
				dcs[i] = cl.Name
			}
			return dcs
		},
		"dcShort":     dcShort,
		"restartClass": func(n int) string {
			if n >= cfg.RestartCrit {
				return "danger"
			}
			if n >= cfg.RestartWarn {
				return "warning"
			}
			return ""
		},
		"jobTypes": func(jobs []collector.JobStatus) string {
			types := make(map[string]bool)
			for _, j := range jobs {
				types[j.Type] = true
			}
			var out []string
			for t := range types {
				out = append(out, t)
			}
			return strings.Join(out, ", ")
		},
		"jobTypesList": func(jobs []collector.JobStatus) []string {
			seen := make(map[string]bool)
			var out []string
			for _, j := range jobs {
				if !seen[j.Type] {
					seen[j.Type] = true
					out = append(out, j.Type)
				}
			}
			return out
		},
		"groupRestarts": func(jobs []collector.JobStatus) int {
			total := 0
			for _, j := range jobs {
				total += j.TotalRestarts
			}
			return total
		},
		"groupRestartClass": func(n int) string {
			if n >= cfg.RestartCrit {
				return "danger"
			}
			if n >= cfg.RestartWarn {
				return "warning"
			}
			return ""
		},
		"overallHealth": func(groups []collector.GroupStatus) string {
			worst := collector.Healthy
			for _, g := range groups {
				if g.Health > worst {
					worst = g.Health
				}
			}
			return healthClass(worst)
		},
		"overallIcon": func(groups []collector.GroupStatus) string {
			worst := collector.Healthy
			for _, g := range groups {
				if g.Health > worst {
					worst = g.Health
				}
			}
			switch worst {
			case collector.Critical:
				return "✖"
			case collector.Warning:
				return "⚠"
			default:
				return "✓"
			}
		},
		"overallText": func(groups []collector.GroupStatus) string {
			worst := collector.Healthy
			for _, g := range groups {
				if g.Health > worst {
					worst = g.Health
				}
			}
			switch worst {
			case collector.Critical:
				return "Issues detected"
			case collector.Warning:
				return "Some services need attention"
			default:
				return "All systems operational"
			}
		},
		"healthyCount": func(groups []collector.GroupStatus) int {
			n := 0
			for _, g := range groups {
				if g.Health == collector.Healthy {
					n++
				}
			}
			return n
		},
	}
}

// parsePage parses a page template with the layout wrapper.
func parsePage(name string, funcMap template.FuncMap) (*template.Template, error) {
	return template.New("layout.html").Funcs(funcMap).ParseFS(templateFS, "templates/layout.html", "templates/"+name)
}

// parseFragment parses a content-only template (no layout) for SSE updates.
// fragment.html defines `{{ block "content" . }}{{ end }}` which is overridden
// by the page template's own `{{ define "content" }}` block. This relies on
// each page template being parsed individually — never combined with another page.
func parseFragment(name string, funcMap template.FuncMap) (*template.Template, error) {
	return template.New("fragment.html").Funcs(funcMap).ParseFS(templateFS, "templates/fragment.html", "templates/"+name)
}

// New creates an HTTP server.
func New(cfg config.Config, coll *collector.Collector, log *slog.Logger) (*Server, error) {
	funcMap := baseFuncMap(cfg)

	pages := []string{"dashboard.html", "group.html", "job.html"}
	tpls := make(map[string]*template.Template, len(pages))
	frags := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t, err := parsePage(p, funcMap)
		if err != nil {
			return nil, fmt.Errorf("parsing template %s: %w", p, err)
		}
		tpls[p] = t

		f, err := parseFragment(p, funcMap)
		if err != nil {
			return nil, fmt.Errorf("parsing fragment %s: %w", p, err)
		}
		frags[p] = f
	}

	pg := paginator.New(paginator.Opt{
		DefaultPerPage: cfg.PerPage,
		MaxPerPage:     cfg.PerPage,
		NumPageNums:    5,
	})

	s := &Server{
		cfg:       cfg,
		collector: coll,
		log:       log,
		tpls:      tpls,
		frags:     frags,
		pg:        pg,
		mux:       http.NewServeMux(),
		sseSem:    make(chan struct{}, cfg.MaxSSEConns),
	}

	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /", s.handleDashboard)
	s.mux.HandleFunc("GET /group/{slug}", s.handleGroup)
	s.mux.HandleFunc("GET /job/{ns}/{id}", s.handleJob)
	s.mux.HandleFunc("GET /events/dashboard", s.handleSSEDashboard)
	s.mux.HandleFunc("GET /events/group/{slug}", s.handleSSEGroup)
	s.mux.HandleFunc("GET /events/job/{ns}/{id}", s.handleSSEJob)

	return s, nil
}

// ServeHTTP dispatches requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// renderFragment renders a content-only fragment for SSE.
func (s *Server) renderFragment(name string, data map[string]any) (string, error) {
	tpl, ok := s.frags[name]
	if !ok {
		return "", fmt.Errorf("fragment template %s not found", name)
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "fragment.html", data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	tpl, ok := s.tpls[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		s.log.Error("rendering template", "name", name, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// dashboardData builds paginated template data.
func (s *Server) dashboardData(query url.Values) map[string]any {
	snap := s.collector.Snapshot()

	p := s.pg.NewFromURL(query)
	p.SetTotal(len(snap.Groups))

	start := p.Offset
	end := p.Offset + p.Limit
	if start > len(snap.Groups) {
		start = len(snap.Groups)
	}
	if end > len(snap.Groups) {
		end = len(snap.Groups)
	}

	return map[string]any{
		"Name":         s.cfg.DisplayName(),
		"AllGroups":    snap.Groups,
		"Groups":       snap.Groups[start:end],
		"Pgn":          p,
		"UpdatedAt":    snap.UpdatedAt,
		"PollInterval": int(s.cfg.PollDuration().Seconds()),
		"Clusters":     s.cfg.Clusters,
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	snap := s.collector.Snapshot()
	if snap.UpdatedAt.IsZero() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok\n")
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	s.render(w, "dashboard.html", s.dashboardData(r.URL.Query()))
}

// groupData builds paginated template data for a group. Returns nil if not found.
func (s *Server) groupData(slug string, query url.Values) map[string]any {
	snap := s.collector.Snapshot()
	var group *collector.GroupStatus
	for i := range snap.Groups {
		if slugify(snap.Groups[i].Name) == slug {
			group = &snap.Groups[i]
			break
		}
	}
	if group == nil {
		return nil
	}


	p := s.pg.NewFromURL(query)
	p.SetTotal(len(group.Jobs))

	start := p.Offset
	end := p.Offset + p.Limit
	if start > len(group.Jobs) {
		start = len(group.Jobs)
	}
	if end > len(group.Jobs) {
		end = len(group.Jobs)
	}

	return map[string]any{
		"Name":         s.cfg.DisplayName(),
		"Group":        group,
		"Jobs":         group.Jobs[start:end],
		"Pgn":          p,
		"UpdatedAt":    snap.UpdatedAt,
		"PollInterval": int(s.cfg.PollDuration().Seconds()),
	}
}

func (s *Server) handleGroup(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	data := s.groupData(slug, r.URL.Query())
	if data == nil {
		http.NotFound(w, r)
		return
	}

	s.render(w, "group.html", data)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("ns")
	id := r.PathValue("id")
	dc := r.URL.Query().Get("dc")

	snap := s.collector.Snapshot()

	var job *collector.JobStatus
	var groupName string
	for _, g := range snap.Groups {
		for i := range g.Jobs {
			j := &g.Jobs[i]
			if j.ID == id && j.Namespace == ns && (dc == "" || j.DC == dc) {
				job = j
				groupName = g.Name
				break
			}
		}
		if job != nil {
			break
		}
	}

	if job == nil {
		http.NotFound(w, r)
		return
	}

	s.render(w, "job.html", map[string]any{
		"Name":         s.cfg.DisplayName(),
		"Job":          job,
		"GroupName":    groupName,
		"GroupSlug":    slugify(groupName),
		"UpdatedAt":    snap.UpdatedAt,
		"PollInterval": int(s.cfg.PollDuration().Seconds()),
	})
}



// sseStream sends an initial SSE fragment, then streams updates on each poll.
func (s *Server) sseStream(w http.ResponseWriter, r *http.Request, tplName string, dataFunc func() map[string]any) {
	// Limit concurrent SSE connections.
	select {
	case s.sseSem <- struct{}{}:
		defer func() { <-s.sseSem }()
	default:
		http.Error(w, "too many SSE connections", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	data := dataFunc()
	if data == nil {
		s.log.Error("SSE stream data not found", "template", tplName)
		return
	}
	html, err := s.renderFragment(tplName, data)
	if err != nil {
		s.log.Error("rendering SSE fragment", "err", err)
		return
	}
	writeSSEEvent(w, html)
	lastHash := contentHash(html)

	ch := s.collector.Subscribe()
	defer s.collector.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			data := dataFunc()
			if data == nil {
				continue
			}
			html, err := s.renderFragment(tplName, data)
			if err != nil {
				s.log.Error("rendering SSE fragment", "err", err)
				continue
			}
			hash := contentHash(html)
			if hash == lastHash {
				continue
			}
			lastHash = hash
			writeSSEEvent(w, html)
		}
	}
}

func (s *Server) handleSSEDashboard(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	s.sseStream(w, r, "dashboard.html", func() map[string]any {
		return s.dashboardData(query)
	})
}

func (s *Server) handleSSEGroup(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	query := r.URL.Query()

	s.sseStream(w, r, "group.html", func() map[string]any {
		return s.groupData(slug, query)
	})
}

func (s *Server) handleSSEJob(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("ns")
	id := r.PathValue("id")
	dc := r.URL.Query().Get("dc")

	s.sseStream(w, r, "job.html", func() map[string]any {
		snap := s.collector.Snapshot()
		for _, g := range snap.Groups {
			for i := range g.Jobs {
				j := &g.Jobs[i]
				if j.ID == id && j.Namespace == ns && (dc == "" || j.DC == dc) {
					return map[string]any{
						"Name":         s.cfg.DisplayName(),
						"Job":          j,
						"GroupName":    g.Name,
						"GroupSlug":    slugify(g.Name),
						"UpdatedAt":    snap.UpdatedAt,
						"PollInterval": int(s.cfg.PollDuration().Seconds()),
					}
				}
			}
		}
		return nil
	})
}


