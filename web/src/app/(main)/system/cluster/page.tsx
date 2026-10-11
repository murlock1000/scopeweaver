"use client";

import { useCallback, useEffect, useState } from "react";

import { Boxes, Circle, Loader2, Pause, Play, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api";
import type { ClusterNode, ClusterWorkerRun } from "@/lib/types";

export default function ClusterPage() {
  const [nodes, setNodes] = useState<ClusterNode[]>([]);
  const [runs, setRuns] = useState<ClusterWorkerRun[]>([]);
  const [loading, setLoading] = useState(true);
  const load = useCallback(async () => {
    try {
      const [n, r] = await Promise.all([api.clusterNodes(), api.clusterRuns()]);
      setNodes(n);
      setRuns(r);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Could not load cluster");
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 10000);
    return () => clearInterval(timer);
  }, [load]);
  async function drain(n: ClusterNode) {
    await api.updateClusterNode(n.id, { draining: !n.draining });
    await load();
  }
  const active = runs.filter((r) => r.state === "running");
  return (
    <div className="flex flex-1 flex-col gap-5 p-4 md:p-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-xl">
            <Boxes className="size-5" />
            Cluster
          </h1>
          <p className="text-muted-foreground text-sm">WireGuard connected worker nodes and their containers.</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()}>
          <RefreshCw className="size-4" />
          Refresh
        </Button>
      </div>
      {loading ? (
        <div className="flex items-center gap-2 text-muted-foreground text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading…
        </div>
      ) : null}
      <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {nodes.map((n) => (
          <article key={n.id} className="rounded-lg border p-4">
            <div className="flex items-start gap-2">
              <Circle
                className={`mt-1 size-3 fill-current ${n.online ? "text-emerald-500" : "text-muted-foreground"}`}
              />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <strong>{n.display_name || n.id}</strong>
                  <Badge variant="outline">{n.online ? "online" : "offline"}</Badge>
                  {n.draining ? <Badge variant="secondary">draining</Badge> : null}
                </div>
                <p className="truncate font-mono text-muted-foreground text-xs">
                  {n.id} · {n.wireguard_ip || "no WireGuard IP"}
                </p>
              </div>
            </div>
            <dl className="mt-3 grid grid-cols-2 gap-x-3 gap-y-1 text-sm">
              <dt className="text-muted-foreground">Owner</dt>
              <dd>{n.owner_name || "—"}</dd>
              <dt className="text-muted-foreground">Slots</dt>
              <dd>{n.capacity}</dd>
              <dt className="text-muted-foreground">LLM</dt>
              <dd className="truncate">{n.llm_model || n.llm_provider || "—"}</dd>
              <dt className="text-muted-foreground">LLM proxy</dt>
              <dd>{n.proxy_configured ? "configured" : "direct"}</dd>
              <dt className="text-muted-foreground">Egress IP</dt>
              <dd className="font-mono text-xs">{n.egress_ip || "—"}</dd>
              <dt className="text-muted-foreground">Workers</dt>
              <dd>{active.filter((r) => r.execution_node_id === n.id).length}</dd>
            </dl>
            <div className="mt-3 flex flex-wrap gap-1">
              {(n.capabilities ?? []).map((c) => (
                <Badge key={c} variant="outline">
                  {c}
                </Badge>
              ))}
            </div>
            <Button className="mt-3" variant="outline" size="sm" onClick={() => void drain(n)}>
              {n.draining ? (
                <>
                  <Play />
                  Accept work
                </>
              ) : (
                <>
                  <Pause />
                  Drain
                </>
              )}
            </Button>
          </article>
        ))}
        {!loading && nodes.length === 0 ? (
          <div className="rounded-lg border border-dashed p-8 text-center text-muted-foreground text-sm">
            No executor nodes have joined.
          </div>
        ) : null}
      </section>
      <section>
        <h2 className="mb-2 font-semibold">Recent worker containers</h2>
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-left text-sm">
            <thead className="bg-muted/50 text-muted-foreground">
              <tr>
                <th className="p-2">State</th>
                <th className="p-2">Node</th>
                <th className="p-2">Container</th>
                <th className="p-2">Task</th>
                <th className="p-2">Intent</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id} className="border-t">
                  <td className="p-2">{r.state}</td>
                  <td className="p-2">{r.execution_node_id}</td>
                  <td className="p-2 font-mono text-xs">{r.container_id}</td>
                  <td className="p-2">#{r.task_id}</td>
                  <td className="p-2">#{r.intent_id}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
