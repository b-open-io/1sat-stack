import { useState, useEffect } from "react";
import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import SetupWizardPage from "./pages/SetupWizardPage";
import SettingsPage from "./pages/SettingsPage";
import { connectWallet, getIdentityKey } from "@/api";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { Button } from "@/components/ui/button";
import "./styles.css";

type AppState = "loading" | "setup" | "connect" | "ready";

function WalletGate({ children }: { children: React.ReactNode }) {
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleConnect() {
    setConnecting(true);
    setError(null);
    try {
      await connectWallet();
    } catch (e: any) {
      setError(e.message || "Failed to connect wallet");
    } finally {
      setConnecting(false);
    }
  }

  if (getIdentityKey()) {
    return <>{children}</>;
  }

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-6">
      <div className="w-full max-w-sm text-center space-y-6">
        <div>
          <div className="inline-flex items-center gap-1.5 mb-4">
            <div className="w-2 h-2 rounded-full bg-primary" />
            <span className="text-xs font-mono text-muted-foreground tracking-widest uppercase">
              1sat stack
            </span>
          </div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Connect wallet
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Admin access requires wallet authentication.
          </p>
        </div>
        <Button
          className="w-full"
          onClick={handleConnect}
          disabled={connecting}
        >
          {connecting ? "Connecting..." : "Connect Wallet"}
        </Button>
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
    </div>
  );
}

function AppContent() {
  const [state, setState] = useState<AppState>("loading");

  useEffect(() => {
    const adminIdx = window.location.pathname.indexOf("/admin");
    const setupBase =
      window.location.origin +
      (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
      "/setup";

    fetch(setupBase + "/status")
      .then((res) => res.json())
      .then((data) => {
        if (!data.configured) {
          setState("setup");
          return;
        }
        // Check auth mode
        return fetch(setupBase + "/config")
          .then((res) => res.json())
          .then((config) => {
            if (config.authMode === "authenticated") {
              setState("connect");
            } else {
              setState("ready");
            }
          });
      })
      .catch(() => setState("setup"));
  }, []);

  if (state === "loading") return null;

  if (state === "setup") {
    return <SetupWizardPage />;
  }

  if (state === "connect") {
    return (
      <WalletGate>
        <SettingsPage />
      </WalletGate>
    );
  }

  return <SettingsPage />;
}

const adminIdx = window.location.pathname.indexOf("/admin");
const basename =
  adminIdx >= 0
    ? window.location.pathname.substring(0, adminIdx + "/admin".length)
    : "/";

export default function App() {
  return (
    <BrowserRouter basename={basename}>
      <TooltipProvider>
        <Routes>
          <Route path="*" element={<AppContent />} />
        </Routes>
        <Toaster position="bottom-right" theme="dark" />
      </TooltipProvider>
    </BrowserRouter>
  );
}
