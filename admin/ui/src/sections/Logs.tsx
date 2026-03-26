import { useState, useEffect, useCallback, useRef } from "react";
import { queryLogs, getLogComponents, type LogEntry } from "../api";
import { toastError } from "@/lib/utils";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";

const LEVEL_STYLES: Record<string, string> = {
  ERROR: "bg-red-500/20 text-red-600 dark:text-red-400 border-0",
  WARN: "bg-yellow-500/20 text-yellow-600 dark:text-yellow-400 border-0",
  INFO: "bg-blue-500/20 text-blue-600 dark:text-blue-400 border-0",
  DEBUG: "bg-muted text-muted-foreground border-0",
};

function useDebounce<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}

export default function Logs() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [components, setComponents] = useState<string[]>([]);

  const [searchInput, setSearchInput] = useState("");
  const [level, setLevel] = useState("");
  const [component, setComponent] = useState("");
  const [offset, setOffset] = useState(0);
  const limit = 100;

  const search = useDebounce(searchInput, 300);

  const intervalRef = useRef<ReturnType<typeof setInterval> | undefined>(
    undefined,
  );

  // Load available components on mount
  useEffect(() => {
    getLogComponents()
      .then(setComponents)
      .catch(() => {});
  }, []);

  const load = useCallback(async () => {
    try {
      const result = await queryLogs({
        search: search || undefined,
        level: level || undefined,
        component: component || undefined,
        limit,
        offset,
      });
      setLogs(result.entries || []);
      setTotal(result.total);
    } catch (e: unknown) {
      toastError(e instanceof Error ? e.message : "Failed to load logs");
    } finally {
      setLoading(false);
    }
  }, [search, level, component, offset]);

  // Reset offset when filters change
  useEffect(() => {
    setOffset(0);
  }, [search, level, component]);

  useEffect(() => {
    setLoading(true);
    load();
  }, [load]);

  // Auto-refresh
  useEffect(() => {
    if (autoRefresh) {
      intervalRef.current = setInterval(load, 5000);
    }
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [autoRefresh, load]);

  const toggleExpand = (i: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(i)) next.delete(i);
      else next.add(i);
      return next;
    });
  };

  const formatTime = (time: string) => {
    try {
      const d = new Date(time);
      return d.toLocaleTimeString(undefined, {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      });
    } catch {
      return time;
    }
  };

  const formatDate = (time: string) => {
    try {
      return new Date(time).toLocaleDateString();
    } catch {
      return "";
    }
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle>System Logs</CardTitle>
        <div className="flex items-center gap-2">
          <Badge variant="secondary">{total} total</Badge>
          <Button
            variant={autoRefresh ? "default" : "secondary"}
            size="sm"
            onClick={() => setAutoRefresh(!autoRefresh)}
          >
            {autoRefresh ? "Auto ●" : "Auto"}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setLoading(true);
              load();
            }}
          >
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">
          Persistent logs stored in SQLite with 7-day retention
        </CardDescription>

        {/* Filters */}
        <div className="mb-4 flex gap-2 flex-wrap">
          <Input
            placeholder="Search messages..."
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            className="h-8 text-xs flex-1 min-w-[150px]"
          />
          <select
            value={level}
            onChange={(e) => setLevel(e.target.value)}
            className="h-8 text-xs px-2 rounded border border-border bg-background"
          >
            <option value="">All levels</option>
            <option value="DEBUG">Debug</option>
            <option value="INFO">Info</option>
            <option value="WARN">Warn</option>
            <option value="ERROR">Error</option>
          </select>
          <select
            value={component}
            onChange={(e) => setComponent(e.target.value)}
            className="h-8 text-xs px-2 rounded border border-border bg-background"
          >
            <option value="">All components</option>
            {components.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
        </div>

        {/* Log list */}
        <ScrollArea className="max-h-[600px]">
          {loading ? (
            <p className="text-center py-8 text-muted-foreground">
              Loading...
            </p>
          ) : logs.length === 0 ? (
            <p className="text-center py-8 text-muted-foreground">No logs</p>
          ) : (
            <div className="space-y-1">
              {logs.map((log, i) => (
                <div
                  key={`${log.time_ns}-${i}`}
                  className="rounded-md bg-secondary p-2 text-xs font-mono cursor-pointer hover:bg-secondary/80"
                  onClick={() =>
                    log.attrs &&
                    Object.keys(log.attrs).length > 0 &&
                    toggleExpand(i)
                  }
                >
                  <div className="flex items-center gap-2 flex-wrap">
                    <span
                      className="text-muted-foreground shrink-0"
                      title={formatDate(log.time)}
                    >
                      {formatTime(log.time)}
                    </span>
                    <Badge
                      className={
                        LEVEL_STYLES[log.level] ||
                        "bg-secondary text-muted-foreground"
                      }
                    >
                      {log.level}
                    </Badge>
                    {log.component && (
                      <span className="text-muted-foreground">
                        [{log.component}]
                      </span>
                    )}
                    <span className="text-foreground break-all">{log.msg}</span>
                  </div>
                  {expanded.has(i) &&
                    log.attrs &&
                    Object.keys(log.attrs).length > 0 && (
                      <div className="mt-1 pl-4 text-[10px] text-muted-foreground border-l border-border ml-2">
                        {Object.entries(log.attrs).map(([k, v]) => (
                          <div key={k}>
                            <span className="text-foreground/70">{k}</span>:{" "}
                            {typeof v === "object"
                              ? JSON.stringify(v)
                              : String(v)}
                          </div>
                        ))}
                      </div>
                    )}
                </div>
              ))}
            </div>
          )}
        </ScrollArea>

        {/* Pagination */}
        {total > limit && (
          <div className="flex items-center justify-between mt-4 text-xs">
            <span className="text-muted-foreground">
              Showing {offset + 1}-{Math.min(offset + limit, total)} of {total}
            </span>
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setOffset(Math.max(0, offset - limit))}
                disabled={offset === 0}
              >
                Previous
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setOffset(offset + limit)}
                disabled={offset + limit >= total}
              >
                Next
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
