// Package fakenetlify is an in-memory stand-in for the parts of the Netlify API
// setup-netlify, and the Netlify CLI it runs, call.
package fakenetlify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

// Token is the only bearer token the server accepts.
const Token = "testtoken"

// Deploy is a deploy the server knows. States are reported in turn, one per
// lookup, and the last one sticks; empty means ready.
type Deploy struct {
	ID           string
	Context      string
	Title        string
	CommitRef    string
	States       []string
	ErrorMessage string
	PublishedAt  time.Time
	// Alias is the subdomain prefix a preview was deployed at, if any.
	Alias string

	lookups int
}

func (d *Deploy) state() string {
	if len(d.States) == 0 {
		return "ready"
	}
	i := min(d.lookups, len(d.States)-1)
	return d.States[i]
}

// Server is a fake Netlify API for one site.
type Server struct {
	// URL is the API base, /api/v1 included, as setup-netlify takes it.
	URL    string
	SiteID string
	Name   string

	mu        sync.Mutex
	deploys   []*Deploy
	published string
	repoURL   string
	created   []map[string]any
	updates   []map[string]any
	requests  []string
	unhandled []string
	seq       int
	clock     time.Time
}

// New starts a server for siteID, registering cleanup with t.
func New(t *testing.T, siteID string) *Server {
	t.Helper()
	s := &Server{SiteID: siteID, Name: "my-site", clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sites/{site}", s.site)
	mux.HandleFunc("GET /api/v1/sites/{site}/deploys", s.list)
	mux.HandleFunc("GET /api/v1/deploys/{id}", s.deploy)
	mux.HandleFunc("GET /api/v1/sites/{site}/deploys/{id}", s.deploy)
	mux.HandleFunc("POST /api/v1/sites/{site}/deploys/{id}/restore", s.restore)
	// What the Netlify CLI calls to deploy.
	mux.HandleFunc("POST /api/v1/sites/{site}/deploys", s.create)
	mux.HandleFunc("PUT /api/v1/deploys/{id}/files/{path...}", s.upload)
	mux.HandleFunc("PUT /api/v1/sites/{site}/deploys/{id}", s.update)
	mux.HandleFunc("GET /api/v1/user", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"id": "user_1", "email": "user@example.com"})
	})
	mux.HandleFunc("GET /api/v1/accounts", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, []map[string]any{{"id": "acct_1", "slug": "team"}})
	})
	mux.HandleFunc("GET /api/v1/accounts/{account}/env", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, []any{}) })
	mux.HandleFunc("GET /api/v1/sites/{site}/service-instances", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, []any{}) })
	mux.HandleFunc("GET /api/v1/sites/{site}/build_hooks", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, []any{}) })
	mux.HandleFunc("GET /team/{account}/integrations/installations/meta/{site}", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, []any{}) })
	mux.HandleFunc("/", s.notFound)
	srv := httptest.NewServer(s.auth(mux))
	t.Cleanup(srv.Close)
	s.URL = srv.URL + "/api/v1"
	return s
}

// Add registers a deploy.
func (s *Server) Add(d *Deploy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deploys = append([]*Deploy{d}, s.deploys...)
}

// SetPublished makes id the deploy production serves.
func (s *Server) SetPublished(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked(id)
}

// SetRepoURL connects the site to a Git repository.
func (s *Server) SetRepoURL(u string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repoURL = u
}

// Published returns the ID of the deploy production serves.
func (s *Server) Published() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.published
}

// Created returns the bodies of the deploys the CLI created.
func (s *Server) Created() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.created)
}

// Requests returns every request's method and path, in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Unhandled returns the requests no route matched.
func (s *Server) Unhandled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.unhandled)
}

func (s *Server) publishLocked(id string) {
	s.clock = s.clock.Add(time.Minute)
	s.published = id
	if d := s.findLocked(id); d != nil {
		d.PublishedAt = s.clock
	}
}

func (s *Server) findLocked(id string) *Deploy {
	for _, d := range s.deploys {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		// The build's extension lookup sends the token in a header of its own.
		if r.Header.Get("Authorization") != "Bearer "+Token && r.Header.Get("Netlify-Sdk-Build-Bot-Token") != Token {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]any{"code": 401, "message": "Access Denied"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.unhandled = append(s.unhandled, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNotFound)
	writeJSON(w, map[string]any{"code": 404, "message": "Not Found"})
}

func (s *Server) site(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("site") != s.SiteID {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"code": 404, "message": "Not Found"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	body := map[string]any{
		"id":             s.SiteID,
		"name":           s.Name,
		"account_id":     "acct_1",
		"account_slug":   "team",
		"ssl_url":        "https://" + s.Name + ".netlify.app",
		"url":            "http://" + s.Name + ".netlify.app",
		"admin_url":      "https://app.netlify.com/projects/" + s.Name,
		"build_settings": map[string]any{"repo_url": s.repoURL},
	}
	if d := s.findLocked(s.published); d != nil {
		body["published_deploy"] = s.jsonLocked(d)
	}
	writeJSON(w, body)
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.deploys))
	for _, d := range s.deploys {
		out = append(out, s.jsonLocked(d))
	}
	writeJSON(w, out)
}

func (s *Server) deploy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.findLocked(r.PathValue("id"))
	if d == nil {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"code": 404, "message": "Not Found"})
		return
	}
	body := s.jsonLocked(d)
	d.lookups++
	writeJSON(w, body)
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.findLocked(r.PathValue("id"))
	if d == nil {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"code": 404, "message": "Not Found"})
		return
	}
	s.publishLocked(d.ID)
	writeJSON(w, s.jsonLocked(d))
}

// create answers the CLI's deploy creation. Every file is reported as already
// uploaded, so the deploy is ready at once.
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &body)
	if body == nil {
		body = map[string]any{}
	}
	if t := r.URL.Query().Get("title"); t != "" {
		body["title"] = t
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.created = append(s.created, body)
	d := &Deploy{ID: fmt.Sprintf("dep%03d", s.seq), Context: "deploy-preview"}
	if t, ok := body["title"].(string); ok {
		d.Title = t
	}
	d.Alias, _ = body["branch"].(string)
	draft, _ := body["draft"].(bool)
	if !draft && d.Alias == "" {
		d.Context = "production"
	}
	s.deploys = append([]*Deploy{d}, s.deploys...)
	if d.Context == "production" {
		s.publishLocked(d.ID)
	}
	resp := s.jsonLocked(d)
	resp["required"] = []string{}
	resp["required_functions"] = []string{}
	writeJSON(w, resp)
}

// update answers the CLI's file and function digest for a deploy, recording it.
// Nothing is reported missing, so no upload follows.
func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &body)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, body)
	d := s.findLocked(r.PathValue("id"))
	if d == nil {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"code": 404, "message": "Not Found"})
		return
	}
	writeJSON(w, s.jsonLocked(d))
}

// Updates returns the file and function digests the CLI sent.
func (s *Server) Updates() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.updates)
}

func (s *Server) upload(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{})
}

func (s *Server) jsonLocked(d *Deploy) map[string]any {
	prefix := d.ID
	if d.Alias != "" {
		prefix = d.Alias
	}
	body := map[string]any{
		"id":             d.ID,
		"site_id":        s.SiteID,
		"name":           s.Name,
		"state":          d.state(),
		"context":        d.Context,
		"title":          d.Title,
		"commit_ref":     nilIfEmpty(d.CommitRef),
		"deploy_ssl_url": "https://" + prefix + "--" + s.Name + ".netlify.app",
		"deploy_url":     "http://" + prefix + "--" + s.Name + ".netlify.app",
		"ssl_url":        "https://" + s.Name + ".netlify.app",
		"url":            "http://" + s.Name + ".netlify.app",
		"admin_url":      "https://app.netlify.com/projects/" + s.Name,
		"error_message":  nilIfEmpty(d.ErrorMessage),
		"required":       []string{},
		"published_at":   nil,
	}
	if !d.PublishedAt.IsZero() {
		body["published_at"] = d.PublishedAt.Format(time.RFC3339Nano)
	}
	return body
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
