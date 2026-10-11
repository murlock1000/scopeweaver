package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	pgdb "github.com/Autumn-27/artex/db"
)

func (s *Server) registerClusterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cluster/nodes", s.clusterNodes)
	mux.HandleFunc("PATCH /api/cluster/nodes/{id}", s.clusterNodeState)
	mux.HandleFunc("DELETE /api/cluster/nodes/{id}", s.clusterNodeDelete)
	mux.HandleFunc("GET /api/cluster/runs", s.clusterRuns)
}

func (s *Server) clusterNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.m.pg.ListClusterNodes(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type nodeView struct {
		pgdb.ClusterNode
		Online bool `json:"online"`
	}
	out := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeView{ClusterNode: n, Online: time.Since(n.LastHeartbeat) < 45*time.Second})
	}
	writeJSON(w, 200, map[string]any{"nodes": out})
}

func (s *Server) clusterNodeState(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, 400, "node id is required")
		return
	}
	var body struct {
		Enabled  *bool `json:"enabled"`
		Draining *bool `json:"draining"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if err := s.m.pg.SetClusterNodeState(r.Context(), id, body.Enabled, body.Draining); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) clusterNodeDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, 400, "node id is required")
		return
	}
	if err := s.m.pg.DeleteClusterNode(r.Context(), id); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) clusterRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.m.pg.ListClusterWorkerRuns(r.Context(), 100)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}
