import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";
import { toast } from "sonner";
import { toastError } from "@/lib/utils";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";

export default function Blacklist() {
  const [tokens, setTokens] = useState<string[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/blacklist");
      if (!res.ok) throw new Error((await res.json()).error || res.statusText);
      setTokens(await res.json());
    } catch {
      toastError("Failed to load blacklist");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  async function add() {
    const val = input.trim();
    if (!val) { toastError("Please enter a token ID"); return; }
    try {
      const res = await apiFetch("/blacklist", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ topic: val }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      setInput("");
      toast.success(`Added "${val}" to blacklist`);
      load();
    } catch (e: any) {
      toastError(e.message);
    }
  }

  async function remove(tokenId: string) {
    try {
      const res = await apiFetch(`/blacklist/${encodeURIComponent(tokenId)}`, { method: "DELETE" });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      toast.success(`Removed "${tokenId}" from blacklist`);
      load();
    } catch (e: any) {
      toastError(e.message);
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle>Token Blacklist</CardTitle>
        <Badge className="bg-warning/20 text-warning border-0">
          {tokens.length} token{tokens.length !== 1 ? "s" : ""}
        </Badge>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">Tokens that will not be activated, even with positive balance</CardDescription>
        <div className="flex gap-2 mb-4">
          <Input
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={e => e.key === "Enter" && add()}
            placeholder="Enter token ID (e.g., abc123...)"
          />
          <Button onClick={add}>Add</Button>
        </div>
        <ScrollArea className="max-h-[400px]">
          {loading ? (
            <p className="text-center py-8 text-muted-foreground">Loading...</p>
          ) : tokens.length === 0 ? (
            <p className="text-center py-8 text-muted-foreground">No tokens</p>
          ) : (
            <div className="space-y-2">
              {tokens.map(t => (
                <div key={t} className="flex items-center justify-between rounded-md bg-secondary p-3">
                  <span className="font-mono text-sm truncate mr-2">{t}</span>
                  <Button variant="ghost" size="sm" onClick={() => remove(t)} className="text-destructive hover:text-destructive shrink-0">
                    Remove
                  </Button>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </CardContent>
    </Card>
  );
}
