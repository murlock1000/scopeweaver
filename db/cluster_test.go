package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func clusterTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		t.Skip("ARTEX_PG_DSN is required")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.Contains(name, "test") && !strings.Contains(name, "disposable") {
		t.Skip("cluster integration test requires a disposable test database")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestClusterNodeRunCompletionAndRecovery(t *testing.T) {
	d := clusterTestDB(t)
	ctx := context.Background()
	nodeID := "cluster-test-node"
	if err := d.UpsertClusterNode(ctx, ClusterNode{ID: nodeID, DisplayName: "fixture", Capacity: 2, Capabilities: []byte(`["worker"]`)}); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTask("cluster fixture", "local only", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	store := d.Exploration(task.ExplorationID)
	intent, err := store.AddIntent(map[string]any{"summary": "fixture"}, 1, nil, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimIntent(intent, "fixture-worker"); err != nil || !ok {
		t.Fatalf("claim=%v err=%v", ok, err)
	}
	run, err := d.StartClusterWorkerRun(ctx, task.ID, intent, nodeID, "container-1", "fixture-worker")
	if err != nil || run == 0 {
		t.Fatalf("run=%d err=%v", run, err)
	}
	if err := d.FinishClusterWorkerRun(ctx, run, "completed", ""); err != nil {
		t.Fatal(err)
	}
	events, err := d.ClusterEventsAfter(ctx, 0)
	if err != nil || len(events) == 0 {
		t.Fatalf("events=%v err=%v", events, err)
	}

	staleIntent, err := store.AddIntent(map[string]any{"summary": "stale"}, 1, nil, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimIntent(staleIntent, "fixture-worker"); err != nil || !ok {
		t.Fatalf("stale claim=%v err=%v", ok, err)
	}
	staleRun, err := d.StartClusterWorkerRun(ctx, task.ID, staleIntent, nodeID, "container-2", "fixture-worker")
	if err != nil || staleRun == 0 {
		t.Fatalf("stale run=%d err=%v", staleRun, err)
	}
	if _, err := d.Exec(`UPDATE cluster_worker_runs SET heartbeat_at=now()-interval '2 minutes' WHERE id=$1`, staleRun); err != nil {
		t.Fatal(err)
	}
	if n, err := d.RecoverStaleClusterRuns(ctx, 45*time.Second); err != nil || n != 1 {
		t.Fatalf("recovered=%d err=%v", n, err)
	}
	frontier, err := store.Frontier(20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range frontier {
		if item.ID == staleIntent {
			found = true
		}
	}
	if !found {
		t.Fatal("stale intent was not reopened")
	}
}
