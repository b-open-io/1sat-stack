import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";
import { toastError } from "@/lib/utils";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";

export default function Lookups() {
  const [lookups, setLookups] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/lookups/active");
      if (!res.ok) throw new Error((await res.json()).error || res.statusText);
      setLookups(await res.json());
    } catch {
      toastError("Failed to load lookups");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle>Lookup Services</CardTitle>
        <div className="flex items-center gap-2">
          <Badge variant="secondary">{lookups.length} service{lookups.length !== 1 ? "s" : ""}</Badge>
          <Button variant="secondary" size="sm" onClick={() => { setLoading(true); load(); }}>
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">Currently active lookup service providers in the overlay engine</CardDescription>
        <ScrollArea className="max-h-[400px]">
          {loading ? (
            <p className="text-center py-8 text-muted-foreground">Loading...</p>
          ) : lookups.length === 0 ? (
            <p className="text-center py-8 text-muted-foreground">No services</p>
          ) : (
            <div className="space-y-2">
              {lookups.map(l => (
                <div key={l} className="rounded-md bg-secondary p-3">
                  <span className="font-mono text-sm">{l}</span>
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </CardContent>
    </Card>
  );
}
