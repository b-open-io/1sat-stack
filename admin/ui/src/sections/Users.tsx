import { useState, useEffect, useCallback } from "react";
import { apiFetch } from "../api";
import { toast } from "sonner";
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

interface AdminUser {
  pubkey: string;
  name: string;
  admin: boolean;
}

export default function Users() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [pubkeyInput, setPubkeyInput] = useState("");
  const [nameInput, setNameInput] = useState("");
  const [adminInput, setAdminInput] = useState(true);
  const [editingPubkey, setEditingPubkey] = useState<string | null>(null);
  const [editName, setEditName] = useState("");

  const load = useCallback(async () => {
    try {
      const res = await apiFetch("/users");
      if (!res.ok) throw new Error((await res.json()).error || res.statusText);
      setUsers(await res.json());
    } catch {
      toastError("Failed to load users");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function addUser() {
    const pubkey = pubkeyInput.trim();
    if (!pubkey) {
      toastError("Pubkey is required");
      return;
    }
    try {
      const res = await apiFetch("/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          pubkey,
          name: nameInput.trim(),
          admin: adminInput,
        }),
      });
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      setPubkeyInput("");
      setNameInput("");
      toast.success("User added");
      load();
    } catch (e: any) {
      toastError(e.message);
    }
  }

  async function toggleAdmin(user: AdminUser) {
    try {
      const res = await apiFetch(
        `/users/${encodeURIComponent(user.pubkey)}`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ admin: !user.admin }),
        },
      );
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      toast.success(
        `${user.name || user.pubkey.slice(0, 8)} is ${!user.admin ? "now" : "no longer"} an admin`,
      );
      load();
    } catch (e: any) {
      toastError(e.message);
    }
  }

  async function saveName(pubkey: string) {
    try {
      const res = await apiFetch(
        `/users/${encodeURIComponent(pubkey)}`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: editName.trim() }),
        },
      );
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      setEditingPubkey(null);
      toast.success("Name updated");
      load();
    } catch (e: any) {
      toastError(e.message);
    }
  }

  async function removeUser(pubkey: string) {
    try {
      const res = await apiFetch(
        `/users/${encodeURIComponent(pubkey)}`,
        { method: "DELETE" },
      );
      if (!res.ok) throw new Error((await res.json()).error || "Failed");
      toast.success("User removed");
      load();
    } catch (e: any) {
      toastError(e.message);
    }
  }

  function copyPubkey(pubkey: string) {
    navigator.clipboard.writeText(pubkey);
    toast.success("Pubkey copied");
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle>Users</CardTitle>
        <Badge className="bg-success/20 text-success border-0">
          {users.length} user{users.length !== 1 ? "s" : ""}
        </Badge>
      </CardHeader>
      <CardContent>
        <CardDescription className="mb-4">
          Manage admin users by public key
        </CardDescription>

        <div className="flex gap-2 mb-4">
          <Input
            value={pubkeyInput}
            onChange={(e) => setPubkeyInput(e.target.value)}
            placeholder="Public key"
            className="flex-1"
          />
          <Input
            value={nameInput}
            onChange={(e) => setNameInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && addUser()}
            placeholder="Name (optional)"
            className="w-40"
          />
          <label className="flex items-center gap-1.5 text-sm whitespace-nowrap cursor-pointer">
            <input
              type="checkbox"
              checked={adminInput}
              onChange={(e) => setAdminInput(e.target.checked)}
              className="rounded"
            />
            Admin
          </label>
          <Button onClick={addUser}>Add</Button>
        </div>

        <ScrollArea className="max-h-[500px]">
          {loading ? (
            <p className="text-center py-8 text-muted-foreground">
              Loading...
            </p>
          ) : users.length === 0 ? (
            <p className="text-center py-8 text-muted-foreground">No users</p>
          ) : (
            <div className="space-y-2">
              {users.map((user) => (
                <div
                  key={user.pubkey}
                  className="flex items-center gap-3 rounded-md bg-secondary p-3"
                >
                  <div className="flex-1 min-w-0">
                    {editingPubkey === user.pubkey ? (
                      <div className="flex gap-2 mb-1">
                        <Input
                          value={editName}
                          onChange={(e) => setEditName(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") saveName(user.pubkey);
                            if (e.key === "Escape") setEditingPubkey(null);
                          }}
                          placeholder="Name"
                          className="h-7 text-sm"
                          autoFocus
                        />
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-7 px-2"
                          onClick={() => saveName(user.pubkey)}
                        >
                          Save
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="h-7 px-2"
                          onClick={() => setEditingPubkey(null)}
                        >
                          Cancel
                        </Button>
                      </div>
                    ) : (
                      <span
                        className="text-sm font-medium cursor-pointer hover:text-primary"
                        onClick={() => {
                          setEditingPubkey(user.pubkey);
                          setEditName(user.name);
                        }}
                      >
                        {user.name || "(no name)"}
                      </span>
                    )}
                    <span
                      className="block font-mono text-xs text-muted-foreground truncate cursor-pointer hover:text-foreground"
                      title={`Click to copy: ${user.pubkey}`}
                      onClick={() => copyPubkey(user.pubkey)}
                    >
                      {user.pubkey}
                    </span>
                  </div>

                  <Badge
                    className={`cursor-pointer shrink-0 ${
                      user.admin
                        ? "bg-primary/20 text-primary border-0"
                        : "bg-secondary text-muted-foreground border"
                    }`}
                    onClick={() => toggleAdmin(user)}
                  >
                    {user.admin ? "Admin" : "User"}
                  </Badge>

                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => removeUser(user.pubkey)}
                    className="text-destructive hover:text-destructive shrink-0"
                  >
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
