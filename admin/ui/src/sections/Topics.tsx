import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";
import { toastError } from "@/lib/utils";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";

export default function Topics() {
  const [topics, setTopics] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/topics/active");
      if (!res.ok) throw new Error((await res.json()).error || res.statusText);
      setTopics(await res.json());
    } catch {
      toastError("Failed to load topics");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle>Active Topics</CardTitle>
        <div className="flex items-center gap-2">
          <Badge variant="secondary">{topics.length} topic{topics.length !== 1 ? "s" : ""}</Badge>
          <Button variant="secondary" size="sm" onClick={() => { setLoading(true); load(); }}>
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">Currently active topic managers in the overlay engine</CardDescription>
        <ScrollArea className="max-h-[400px]">
          {loading ? (
            <p className="text-center py-8 text-muted-foreground">Loading...</p>
          ) : topics.length === 0 ? (
            <p className="text-center py-8 text-muted-foreground">No topics</p>
          ) : (
            <div className="space-y-2">
              {topics.map(t => (
                <div key={t} className="rounded-md bg-secondary p-3">
                  <span className="font-mono text-sm">{t}</span>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </CardContent>
    </Card>
  );
}
