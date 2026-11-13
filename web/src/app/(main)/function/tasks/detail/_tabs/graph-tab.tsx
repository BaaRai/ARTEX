"use client";

import * as React from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { ExplorationGraph } from "@/components/exploration-graph";
import { api } from "@/lib/api";
import { setVisibleInterval } from "@/lib/polling";
import type { Edge, TaskNode } from "@/lib/types";

export function GraphTab({ taskId }: { taskId: string }) {
  const [nodes, setNodes] = React.useState<TaskNode[]>([]);
  const [edges, setEdges] = React.useState<Edge[]>([]);
  const [mode, setMode] = React.useState<"recent" | "inherited">("recent");
  const [revision, setRevision] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [recentCount, setRecentCount] = React.useState(0);
  const [total, setTotal] = React.useState(0);
  const sigRef = React.useRef("");

  React.useEffect(() => {
    let cancelled = false;
    let running = false;
    sigRef.current = "";
    setNodes([]);
    setEdges([]);
    setLoading(true);
    const load = async () => {
      if (running || cancelled) return;
      running = true;
      try {
        // The existing paged endpoint includes immediate neighbours and their
        // edges. Do not present this local snapshot as the full inherited graph.
        const data = mode === "recent"
          ? await api.explorationNodes(taskId, { page: 1, size: 100 })
          : await api.explorationGraph(taskId);
        if (cancelled) return;
        const ns = "items" in data
          ? [...new Map([...Object.values(data.refs), ...data.items].map((node) => [node.id, node])).values()]
          : (data.nodes ?? []);
        const es = data.edges ?? [];
        if ("items" in data) {
          setRecentCount(data.items.length);
          setTotal(data.total);
        }
        const sig = JSON.stringify([ns, es]);
        if (sig !== sigRef.current) {
          sigRef.current = sig;
          setNodes(ns);
          setEdges(es);
        }
        setError("");
      } catch (cause) {
        if (!cancelled) setError((cause as Error).message || "Failed to load graph");
      } finally {
        running = false;
        if (!cancelled) setLoading(false);
      }
    };
    void load();
    // Large inherited snapshots are user-triggered, not repeatedly downloaded.
    const stop = mode === "recent" ? setVisibleInterval(load, 20_000) : () => {};
    return () => {
      cancelled = true;
      stop();
    };
  }, [taskId, mode, revision]);

  return (
    <Card>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
        <div className="text-sm text-muted-foreground" aria-live="polite">
          {loading ? "Loading graph…" : mode === "recent"
            ? `Recent activity: ${recentCount} of ${total} task nodes, plus direct neighbours`
            : "Task and inherited graph · refresh manually"}
          {error && <span className="ml-2 text-destructive">{error}</span>}
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={loading} onClick={() => setRevision((value) => value + 1)}>
            Refresh
          </Button>
          <Button variant="outline" size="sm" disabled={loading}
            onClick={() => setMode(mode === "recent" ? "inherited" : "recent")}>
            {mode === "recent" ? "Load task + inherited graph" : "Show recent activity"}
          </Button>
        </div>
      </div>
      <CardContent className="p-0">
        <ExplorationGraph nodes={nodes} edges={edges} className="h-[72vh]" />
      </CardContent>
    </Card>
  );
}
