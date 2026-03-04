import { useState } from "react";
import { apiFetch } from "../api";
import { toastError } from "@/lib/utils";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";

interface ZSetItem {
  value: string;
  score: number;
}

export default function ZSetLookup() {
  const [input, setInput] = useState("");
  const [key, setKey] = useState("");
  const [items, setItems] = useState<ZSetItem[]>([]);
  const [count, setCount] = useState(0);
  const [visible, setVisible] = useState(false);
  const [loading, setLoading] = useState(false);

  async function lookup() {
    const val = input.trim();
    if (!val) { toastError("Please enter a sorted set key"); return; }
    setVisible(true);
    setLoading(true);
    try {
      const res = await apiFetch(`/zset/${encodeURIComponent(val)}`);
      if (!res.ok) throw new Error((await res.json()).error || "Failed to lookup");
      const data = await res.json();
      setKey(data.key);
      setCount(data.count);
      setItems(data.items || []);
    } catch (e: any) {
      setItems([]);
      setCount(0);
      toastError(e.message);
    } finally {
      setLoading(false);
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle>Sorted Set Lookup</CardTitle>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">Look up any sorted set by key (e.g., q:tok:abc123, z:ev:own:address)</CardDescription>
        <div className="flex gap-2 mb-4">
          <Input
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={e => e.key === "Enter" && lookup()}
            placeholder="Enter sorted set key (e.g., q:tok:abc123...)"
          />
          <Button onClick={lookup}>Lookup</Button>
        </div>
        {visible && (
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="font-mono text-sm">{key}</span>
              <Badge variant="secondary">{count} item{count !== 1 ? "s" : ""}</Badge>
            </div>
            <ScrollArea className="max-h-[400px]">
              {loading ? (
                <p className="text-center py-8 text-muted-foreground">Loading...</p>
              ) : items.length === 0 ? (
                <p className="text-center py-8 text-muted-foreground">No items found</p>
              ) : (
                <div className="space-y-2">
                  {items.map((item, i) => (
                    <div key={i} className="flex items-center justify-between rounded-md bg-secondary p-3">
                      <span className="font-mono text-sm break-all mr-2">{item.value}</span>
                      <Badge variant="secondary" className="shrink-0">score: {item.score}</Badge>
                    </div>
                  ))}
                </div>
              )}
            </ScrollArea>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
