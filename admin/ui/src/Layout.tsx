import { useEffect, useState, useCallback } from "react";
import { Outlet } from "react-router-dom";
import {
  isWalletAvailable,
  connectWallet,
  getIdentityKey,
  getSetupStatus,
} from "./api";
import { toast } from "sonner";
import { SidebarProvider, SidebarInset, SidebarTrigger } from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import { AppSidebar } from "@/components/app-sidebar";
import SetupWizard from "./sections/SetupWizard";

type SetupStatus = "loading" | "needed" | "complete";

export default function Layout({ children }: { children?: React.ReactNode }) {
  const [walletDetected, setWalletDetected] = useState(false);
  const [walletChecked, setWalletChecked] = useState(false);
  const [connected, setConnected] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [identityKey, setIdentityKey] = useState<string | null>(null);
  const [setupStatus, setSetupStatus] = useState<SetupStatus>("loading");

  useEffect(() => {
    getSetupStatus()
      .then((s) => setSetupStatus(s.configured ? "complete" : "needed"))
      .catch(() => setSetupStatus("needed"));
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
      toast.error(e.message || "Failed to connect wallet");
    } finally {
      setConnecting(false);
    }
  }, []);

  const copyIdentity = useCallback(() => {
    const key = getIdentityKey();
    if (key) {
      navigator.clipboard.writeText(key);
      toast.success("Identity key copied to clipboard");
    }
  }, []);

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
              No compatible wallet detected. Install Yours Wallet (or another
              wallet that injects <code>window.CWI</code>) to authenticate.
            </div>
          )}

          {walletChecked && walletDetected && !connected && !connecting && (
            <div className="mb-4 rounded-lg border border-destructive bg-destructive/10 px-4 py-3 text-sm">
              Connect your wallet to access admin functions.
            </div>
          )}

          {setupStatus === "loading" && (
            <div className="mb-4 rounded-lg border border-border bg-muted/20 px-4 py-3 text-sm text-muted-foreground">
              Checking setup status...
            </div>
          )}

          {connected && setupStatus === "needed" && (
            <SetupWizard onComplete={() => setSetupStatus("complete")} />
          )}

          {connected && setupStatus === "complete" && (children || <Outlet />)}
        </main>
      </SidebarInset>
    </SidebarProvider>
  );
}
