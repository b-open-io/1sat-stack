import { useEffect, useState, useCallback } from "react";
import { Outlet } from "react-router-dom";
import {
  isWalletAvailable,
  connectWallet,
  getIdentityKey,
  getSetupStatus,
  checkAccess,
  requestAccess,
} from "./api";
import { toast } from "sonner";
import { toastError } from "@/lib/utils";
import { SidebarProvider, SidebarInset, SidebarTrigger } from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import { AppSidebar } from "@/components/app-sidebar";
import SetupWizard from "./sections/SetupWizard";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

type SetupStatus = "loading" | "needed" | "complete";
type AccessStatus = "unknown" | "checking" | "approved" | "none" | "requested";

export default function Layout({ children }: { children?: React.ReactNode }) {
  const [walletDetected, setWalletDetected] = useState(false);
  const [walletChecked, setWalletChecked] = useState(false);
  const [connected, setConnected] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [identityKey, setIdentityKey] = useState<string | null>(null);
  const [setupStatus, setSetupStatus] = useState<SetupStatus>("loading");
  const [accessStatus, setAccessStatus] = useState<AccessStatus>("unknown");
  const [requestName, setRequestName] = useState("");
  const [requestSubmitting, setRequestSubmitting] = useState(false);

  useEffect(() => {
    getSetupStatus()
      .then((s) => setSetupStatus(s.configured ? "complete" : "needed"))
      .catch((e) => {
        console.error("Setup status check failed:", e);
        setSetupStatus("needed");
      });
  }, []);

  useEffect(() => {
    if (isWalletAvailable()) {
      setWalletDetected(true);
      setWalletChecked(true);
      return;
    }
    let attempts = 0;
    const interval = setInterval(() => {
      attempts++;
      if (isWalletAvailable()) {
        clearInterval(interval);
        setWalletDetected(true);
        setWalletChecked(true);
      } else if (attempts >= 20) {
        clearInterval(interval);
        setWalletChecked(true);
      }
    }, 250);
    return () => clearInterval(interval);
  }, []);

  const handleConnect = useCallback(async () => {
    setConnecting(true);
    try {
      const key = await connectWallet();
      setIdentityKey(key);
      setConnected(true);
    } catch (e: any) {
      toastError(e.message || "Failed to connect wallet");
    } finally {
      setConnecting(false);
    }
  }, []);

  useEffect(() => {
    if (!connected || setupStatus !== "complete") return;
    setAccessStatus("checking");
    checkAccess()
      .then((result) => setAccessStatus(result.status === "approved" ? "approved" : "none"))
      .catch(() => setAccessStatus("none"));
  }, [connected, setupStatus]);

  const copyIdentity = useCallback(() => {
    const key = getIdentityKey();
    if (key) {
      navigator.clipboard.writeText(key);
      toast.success("Identity key copied to clipboard");
    }
  }, []);

  const handleRequestAccess = useCallback(async () => {
    const name = requestName.trim();
    if (!name) {
      toastError("Please enter your name");
      return;
    }
    setRequestSubmitting(true);
    try {
      await requestAccess(name);
      setAccessStatus("requested");
      toast.success("Access request submitted");
    } catch (e: any) {
      toastError(e.message || "Failed to submit request");
    } finally {
      setRequestSubmitting(false);
    }
  }, [requestName]);

  // Setup not complete — show only the setup flow, no sidebar/nav
  if (setupStatus !== "complete") {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center p-6">
        <div className="w-full max-w-lg space-y-4">
          <h1 className="text-2xl font-semibold text-center">
            <span className="text-primary">1Sat</span> Stack
          </h1>

          {setupStatus === "loading" && (
            <p className="text-center text-sm text-muted-foreground">
              Checking setup status...
            </p>
          )}

          {setupStatus === "needed" && !connected && (
            <div className="text-center space-y-4">
              <p className="text-sm text-muted-foreground">
                No admin has been configured. Connect your wallet to set up this server.
              </p>
              {walletChecked && !walletDetected && (
                <p className="text-sm text-destructive">
                  No compatible wallet detected. Install Yours Wallet to continue.
                </p>
              )}
              {walletDetected && (
                <Button onClick={handleConnect} disabled={connecting} size="lg">
                  {connecting ? "Connecting..." : "Connect Wallet"}
                </Button>
              )}
            </div>
          )}

          {setupStatus === "needed" && connected && (
            <SetupWizard onComplete={() => setSetupStatus("complete")} />
          )}
        </div>
      </div>
    );
  }

  // Setup complete — normal admin layout
  return (
    <SidebarProvider>
      <AppSidebar
        walletDetected={walletDetected}
        walletChecked={walletChecked}
        connected={connected}
        connecting={connecting}
        identityKey={identityKey}
        onConnect={handleConnect}
        onCopyIdentity={copyIdentity}
      />
      <SidebarInset>
        <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mr-2 !h-4" />
          <span className="text-sm text-muted-foreground">1Sat Stack Admin</span>
        </header>

        <main className="flex-1 p-6">
          {walletChecked && !walletDetected && (
            <div className="mb-4 rounded-lg border border-destructive bg-destructive/10 px-4 py-3 text-sm">
              No compatible wallet detected. Install Yours Wallet to authenticate.
            </div>
          )}

          {walletChecked && walletDetected && !connected && !connecting && (
            <div className="mb-4 rounded-lg border border-destructive bg-destructive/10 px-4 py-3 text-sm">
              Connect your wallet to access admin functions.
            </div>
          )}

          {connected && accessStatus === "checking" && (
            <p className="text-sm text-muted-foreground">Checking access...</p>
          )}

          {connected && (accessStatus === "none" || accessStatus === "requested") && (
            <div className="flex items-center justify-center pt-12">
              <Card className="max-w-md w-full">
                <CardHeader>
                  <CardTitle>
                    {accessStatus === "requested" ? "Request Submitted" : "Request Access"}
                  </CardTitle>
                  <CardDescription>
                    {accessStatus === "requested"
                      ? "Your request has been submitted. An admin will review it."
                      : "You don't have access to this admin panel. Enter your name to request access."}
                  </CardDescription>
                </CardHeader>
                {accessStatus === "none" && (
                  <CardContent>
                    <div className="space-y-3">
                      <div className="text-xs font-mono text-muted-foreground break-all">
                        {identityKey}
                      </div>
                      <Input
                        value={requestName}
                        onChange={(e) => setRequestName(e.target.value)}
                        onKeyDown={(e) => e.key === "Enter" && handleRequestAccess()}
                        placeholder="Your name"
                        autoFocus
                      />
                      <Button
                        onClick={handleRequestAccess}
                        disabled={requestSubmitting}
                        className="w-full"
                      >
                        {requestSubmitting ? "Submitting..." : "Request Access"}
                      </Button>
                    </div>
                  </CardContent>
                )}
              </Card>
            </div>
          )}

          {connected && accessStatus === "approved" && (children || <Outlet />)}
        </main>
      </SidebarInset>
    </SidebarProvider>
  );
}
