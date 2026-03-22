import { useState, useEffect } from "react";
import { BrowserRouter, Routes, Route } from "react-router-dom";
import type { WalletProviderConfig } from "@1sat/connect";
import { WalletProvider, ConnectDialogProvider, useWallet } from "@1sat/react";
import SetupWizardPage from "./pages/SetupWizardPage";
import SettingsPage from "./pages/SettingsPage";
import { setWallet } from "@/api";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { Button } from "@/components/ui/button";
import "./styles.css";

type AppState = "loading" | "setup" | "connect" | "ready";

const providers: WalletProviderConfig[] = [
  {
    type: "onesat",
    name: "OneSat Wallet",
  },
];

function WalletGate({ children }: { children: React.ReactNode }) {
  const { wallet, status, connect } = useWallet();

  // Set wallet synchronously so authFetch is ready before children render
  setWallet(wallet);

  if (status === "detecting") return null;

  if (status === "connected" && wallet) {
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
          onClick={() => connect()}
          disabled={status === "connecting" || status === "selecting"}
        >
          {status === "connecting" ? "Connecting..." : "Connect Wallet"}
        </Button>
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
      <WalletProvider autoReconnect providers={providers}>
        <ConnectDialogProvider>
          <TooltipProvider>
            <Routes>
              <Route path="*" element={<AppContent />} />
            </Routes>
            <Toaster position="bottom-right" theme="dark" />
          </TooltipProvider>
        </ConnectDialogProvider>
      </WalletProvider>
    </BrowserRouter>
  );
}
