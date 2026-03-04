import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";
import { toast } from "sonner";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";

interface WorkerStatus {
  token_id: string;
  fee_address: string;
  queue_depth: number;
  started_at: string;
}

function formatUptime(startedAt: string): string {
  const diff = Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000);
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ${Math.floor((diff % 3600) / 60)}m`;
  return `${Math.floor(diff / 86400)}d ${Math.floor((diff % 86400) / 3600)}h`;
}

export default function Workers() {
  const [workers, setWorkers] = useState<WorkerStatus[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/bsv21/workers");
      if (!res.ok) throw new Error((await res.json()).error || res.statusText);
      setWorkers(await res.json());
    } catch {
      toast.error("Failed to load workers");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const interval = setInterval(load, 10000);
    return () => clearInterval(interval);
  }, [load]);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle>Token Workers</CardTitle>
        <div className="flex items-center gap-2">
          <Badge className="bg-success/20 text-success border-0">
            {workers.length} worker{workers.length !== 1 ? "s" : ""}
          </Badge>
          <Button variant="secondary" size="sm" onClick={() => { setLoading(true); load(); }}>
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">Active token workers processing BSV21 transactions</CardDescription>
        <ScrollArea className="max-h-[400px]">
          {loading ? (
            <p className="text-center py-8 text-muted-foreground">Loading...</p>
          ) : workers.length === 0 ? (
            <p className="text-center py-8 text-muted-foreground">No active workers</p>
          ) : (
            <div className="space-y-2">
              {workers.map(w => (
                <div key={w.token_id} className="flex items-center justify-between rounded-md bg-secondary p-3">
                  <div className="flex-1 min-w-0">
                    <div className="font-mono text-sm mb-1 truncate">{w.token_id}</div>
                    <div className="text-xs text-muted-foreground truncate">
                      Fee: {w.fee_address} · Up: {formatUptime(w.started_at)}
                    </div>
                  </div>
                  <Badge className={`ml-2 shrink-0 border-0 ${
                    w.queue_depth > 100 ? "bg-warning/20 text-warning" :
                    w.queue_depth > 0 ? "bg-secondary text-muted-foreground" :
                    "bg-success/20 text-success"
                  }`}>
                    {w.queue_depth} queued
                  </Badge>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </CardContent>
    </Card>
  );
}
