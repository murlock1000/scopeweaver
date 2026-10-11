package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type ClusterNode struct {
	ID              string          `json:"id"`
	DisplayName     string          `json:"display_name"`
	OwnerName       string          `json:"owner_name"`
	WireGuardIP     string          `json:"wireguard_ip"`
	Capacity        int             `json:"capacity"`
	Capabilities    json.RawMessage `json:"capabilities"`
	LLMProvider     string          `json:"llm_provider"`
	LLMModel        string          `json:"llm_model"`
	ProxyConfigured bool            `json:"proxy_configured"`
	EgressIP        string          `json:"egress_ip"`
	Enabled         bool            `json:"enabled"`
	Draining        bool            `json:"draining"`
	Version         string          `json:"version"`
	LastHeartbeat   time.Time       `json:"last_heartbeat"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type ClusterWorkerRun struct {
	ID              int64      `json:"id"`
	TaskID          int64      `json:"task_id"`
	IntentID        int64      `json:"intent_id"`
	ExecutionNodeID string     `json:"execution_node_id"`
	ContainerID     string     `json:"container_id"`
	WorkerName      string     `json:"worker_name"`
	State           string     `json:"state"`
	LeaseGeneration int64      `json:"lease_generation"`
	HeartbeatAt     time.Time  `json:"heartbeat_at"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Error           string     `json:"error"`
}

func (d *DB) UpsertClusterNode(ctx context.Context, n ClusterNode) error {
	if len(n.Capabilities) == 0 {
		n.Capabilities = json.RawMessage(`[]`)
	}
	if n.Capacity < 1 {
		n.Capacity = 1
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO cluster_nodes(id,display_name,owner_name,wireguard_ip,capacity,capabilities,llm_provider,llm_model,proxy_configured,egress_ip,version,last_heartbeat)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
ON CONFLICT(id) DO UPDATE SET
 display_name=EXCLUDED.display_name, owner_name=EXCLUDED.owner_name,
 wireguard_ip=EXCLUDED.wireguard_ip, capacity=EXCLUDED.capacity,
 capabilities=EXCLUDED.capabilities, llm_provider=EXCLUDED.llm_provider,
 llm_model=EXCLUDED.llm_model, proxy_configured=EXCLUDED.proxy_configured,
 egress_ip=EXCLUDED.egress_ip, version=EXCLUDED.version,
 last_heartbeat=now(), updated_at=now()`, n.ID, n.DisplayName, n.OwnerName,
		n.WireGuardIP, n.Capacity, n.Capabilities, n.LLMProvider, n.LLMModel,
		n.ProxyConfigured, n.EgressIP, n.Version)
	return err
}

func (d *DB) ListClusterNodes(ctx context.Context) ([]ClusterNode, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,display_name,owner_name,wireguard_ip,capacity,capabilities,
llm_provider,llm_model,proxy_configured,egress_ip,enabled,draining,version,last_heartbeat,created_at,updated_at
FROM cluster_nodes ORDER BY display_name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClusterNode
	for rows.Next() {
		var n ClusterNode
		if err := rows.Scan(&n.ID, &n.DisplayName, &n.OwnerName, &n.WireGuardIP, &n.Capacity, &n.Capabilities,
			&n.LLMProvider, &n.LLMModel, &n.ProxyConfigured, &n.EgressIP, &n.Enabled, &n.Draining, &n.Version,
			&n.LastHeartbeat, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (d *DB) SetClusterNodeState(ctx context.Context, id string, enabled, draining *bool) error {
	_, err := d.ExecContext(ctx, `UPDATE cluster_nodes SET
enabled=COALESCE($2,enabled), draining=COALESCE($3,draining), updated_at=now() WHERE id=$1`, id, enabled, draining)
	return err
}

func (d *DB) ClusterNodeRunnable(ctx context.Context, id string) bool {
	var runnable bool
	err := d.QueryRowContext(ctx, `SELECT enabled AND NOT draining FROM cluster_nodes WHERE id=$1`, id).Scan(&runnable)
	return err == nil && runnable
}

func (d *DB) ClusterWorkerCapacity(ctx context.Context) int {
	var capacity int
	err := d.QueryRowContext(ctx, `SELECT COALESCE(sum(capacity),0) FROM cluster_nodes
WHERE enabled AND NOT draining AND last_heartbeat > now()-interval '45 seconds'`).Scan(&capacity)
	if err != nil {
		return 0
	}
	return capacity
}

func (d *DB) DeleteClusterNode(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM cluster_nodes WHERE id=$1
AND NOT EXISTS(SELECT 1 FROM cluster_worker_runs WHERE execution_node_id=$1 AND state='running')`, id)
	return err
}

func (d *DB) StartClusterWorkerRun(ctx context.Context, taskID, intentID int64, nodeID, containerID, worker string) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `INSERT INTO cluster_worker_runs(task_id,intent_id,execution_node_id,container_id,worker_name)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(intent_id) WHERE state='running' DO NOTHING RETURNING id`,
		taskID, intentID, nodeID, containerID, worker).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (d *DB) FinishClusterWorkerRun(ctx context.Context, id int64, state, detail string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var taskID int64
	err = tx.QueryRowContext(ctx, `UPDATE cluster_worker_runs SET state=$2,error=$3,heartbeat_at=now(),finished_at=now()
WHERE id=$1 AND state='running' RETURNING task_id`, id, state, detail).Scan(&taskID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cluster_events(task_id,kind,detail)
VALUES($1,'worker_finished',jsonb_build_object('run_id',$2,'state',$3))`, taskID, id, state); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) HeartbeatClusterWorkerRun(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `UPDATE cluster_worker_runs SET heartbeat_at=now() WHERE id=$1 AND state='running'`, id)
	return err
}

func (d *DB) ListClusterWorkerRuns(ctx context.Context, limit int) ([]ClusterWorkerRun, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := d.QueryContext(ctx, `SELECT id,task_id,intent_id,execution_node_id,container_id,worker_name,state,
lease_generation,heartbeat_at,started_at,finished_at,error FROM cluster_worker_runs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClusterWorkerRun
	for rows.Next() {
		var r ClusterWorkerRun
		if err := rows.Scan(&r.ID, &r.TaskID, &r.IntentID, &r.ExecutionNodeID, &r.ContainerID,
			&r.WorkerName, &r.State, &r.LeaseGeneration, &r.HeartbeatAt, &r.StartedAt, &r.FinishedAt, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ClusterEvent struct {
	ID     int64
	TaskID int64
	Kind   string
}

func (d *DB) ClusterEventsAfter(ctx context.Context, after int64) ([]ClusterEvent, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,task_id,kind FROM cluster_events WHERE id>$1 ORDER BY id LIMIT 200`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClusterEvent
	for rows.Next() {
		var e ClusterEvent
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Kind); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (d *DB) LatestClusterEventID(ctx context.Context) int64 {
	var id int64
	if err := d.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM cluster_events`).Scan(&id); err != nil {
		return 0
	}
	return id
}

// RecoverStaleClusterRuns releases intents whose executor stopped heartbeating.
// The event gives the control-plane Planner an immediate reassessment edge.
func (d *DB) RecoverStaleClusterRuns(ctx context.Context, staleFor time.Duration) (int, error) {
	if staleFor < 15*time.Second {
		staleFor = 45 * time.Second
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `UPDATE cluster_worker_runs SET state='lost',finished_at=now(),error='executor heartbeat expired'
WHERE state='running' AND heartbeat_at < now()-make_interval(secs=>$1) RETURNING intent_id,task_id`, int(staleFor.Seconds()))
	if err != nil {
		return 0, err
	}
	type lost struct{ intent, task int64 }
	var items []lost
	for rows.Next() {
		var x lost
		if err := rows.Scan(&x.intent, &x.task); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, x)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, x := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE exploration_nodes SET state='open',owner=NULL,completed_at=NULL,blocked_reason=NULL WHERE id=$1 AND kind='intent' AND state='running'`, x.intent); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cluster_events(task_id,kind,detail) VALUES($1,'worker_lost',jsonb_build_object('intent_id',$2))`, x.task, x.intent); err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit()
}
