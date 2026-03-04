import { useState, useEffect, useCallback, useRef } from "react";
import { apiFetch } from "../api";
import { toast } from "sonner";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";

interface ProgressItem {
  id: string;
  block: number;
}

export default function Progress() {
  const [items, setItems] = useState<ProgressItem[]>([]);
  const [loading, setLoading] = useState(true);
  const inputRefs = useRef<Record<string, HTMLInputElement | null>>({});

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/progress");
      if (!res.ok) throw new Error((await res.json()).error || res.statusText);
      setItems(await res.json());
    } catch {
      toast.error("Failed to load progress");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function update(id: string) {
    const el = inputRefs.current[id];
    if (!el) return;
    const block = parseFloat(el.value);
    if (isNaN(block)) { toast.error("Please enter a valid block height"); return; }
    try {
      const res = await apiFetch(`/progress/${encodeURIComponent(id)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ block }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      toast.success(`Updated "${id}" to block ${Math.floor(block).toLocaleString()}`);
    } catch (e: any) {
      toast.error(e.message);
      load();
    }
  }

  async function remove(id: string) {
    if (!confirm(`Delete progress for "${id}"?`)) return;
    try {
      const res = await apiFetch(`/progress/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      toast.success(`Deleted "${id}"`);
      load();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle>Sync Progress</CardTitle>
        <div className="flex items-center gap-2">
          <Badge variant="secondary">{items.length} entr{items.length !== 1 ? "ies" : "y"}</Badge>
          <Button variant="secondary" size="sm" onClick={() => { setLoading(true); load(); }}>
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">Sync progress tracking for subscriptions and addresses</CardDescription>
        <ScrollArea className="max-h-[400px]">
          {loading ? (
            <p className="text-center py-8 text-muted-foreground">Loading...</p>
          ) : items.length === 0 ? (
            <p className="text-center py-8 text-muted-foreground">No progress entries</p>
          ) : (
            <div className="space-y-2">
              {items.map(item => (
                <div key={item.id} className="flex items-center justify-between rounded-md bg-secondary p-3">
                  <span className="font-mono text-sm truncate mr-2">{item.id}</span>
                  <div className="flex items-center gap-2 shrink-0">
                    <Input
                      type="number"
                      defaultValue={Math.floor(item.block)}
                      ref={el => { inputRefs.current[item.id] = el; }}
                      className="w-[120px] h-8 text-sm"
                      onClick={e => e.stopPropagation()}
                    />
                    <Button variant="secondary" size="sm" onClick={e => { e.stopPropagation(); update(item.id); }}>
                      Update
                    </Button>
                    <Button variant="destructive" size="sm" onClick={e => { e.stopPropagation(); remove(item.id); }}>
                      Delete
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </CardContent>
    </Card>
  );
}
