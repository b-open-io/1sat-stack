import { useEffect, useState, useCallback } from "react";
import { Link, useLocation, Outlet } from "react-router-dom";
import { isWalletAvailable, connectWallet, getIdentityKey, getSetupStatus } from "./api";
import { useToast } from "./useToast";
import SetupWizard from "./sections/SetupWizard";

interface LayoutProps {
  children?: React.ReactNode;
}

type SetupStatus = "loading" | "needed" | "complete";

export default function Layout({ children }: LayoutProps) {
  const location = useLocation();
  const [walletDetected, setWalletDetected] = useState(false);
  const [walletChecked, setWalletChecked] = useState(false);
  const [connected, setConnected] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [identityKey, setIdentityKey] = useState<string | null>(null);
  const [setupStatus, setSetupStatus] = useState<SetupStatus>("loading");
  const { toast, showToast } = useToast();

  // Check setup status on mount
  useEffect(() => {
    getSetupStatus()
      .then((s) => setSetupStatus(s.configured ? "complete" : "needed"))
      .catch(() => setSetupStatus("needed"));
  }, []);

  // Poll briefly for wallet injection
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
      showToast(e.message || "Failed to connect wallet", "error");
    } finally {
      setConnecting(false);
    }
  }, [showToast]);

  const copyIdentity = useCallback(() => {
    const key = getIdentityKey();
    if (key) {
      navigator.clipboard.writeText(key);
      showToast("Identity key copied to clipboard");
    }
  }, [showToast]);

  return (
    <div className="container">
      <header>
        <h1><span>1Sat</span> Stack Admin</h1>
        <nav className="nav-links">
          <Link to="/" className={location.pathname === "/" ? "active" : ""}>
            OpNS
          </Link>
          <Link to="/admin" className={location.pathname === "/admin" ? "active" : ""}>
            Admin
          </Link>
        </nav>
        <div className="identity-bar">
          {!walletChecked ? (
            <span>Detecting wallet...</span>
          ) : connected ? (
            <>
              <span className="status" />
              <span>Connected</span>
              {identityKey && (
                <span className="pubkey" title={`Click to copy: ${identityKey}`} onClick={copyIdentity}>
                  {identityKey}
                </span>
              )}
            </>
          ) : walletDetected ? (
            <button onClick={handleConnect} disabled={connecting}>
              {connecting ? "Connecting..." : "Connect Wallet"}
            </button>
          ) : (
            <>
              <span className="status disconnected" />
              <span>No wallet detected</span>
            </>
          )}
        </div>
      </header>

      {walletChecked && !walletDetected && (
        <div className="wallet-warning">
          No compatible wallet detected. Install Yours Wallet (or another wallet that injects <code>window.CWI</code>) to authenticate.
        </div>
      )}

      {walletChecked && walletDetected && !connected && !connecting && (
        <div className="wallet-warning">
          Connect your wallet to access admin functions.
        </div>
      )}

      {setupStatus === "loading" && (
        <div className="wallet-warning">Checking setup status...</div>
      )}

      {connected && setupStatus === "needed" && (
        <SetupWizard
          onComplete={() => setSetupStatus("complete")}
          showToast={showToast}
        />
      )}

      {connected && setupStatus === "complete" && (
        children || <Outlet />
      )}

      {toast && <div className={`toast ${toast.type}`}>{toast.message}</div>}
    </div>
  );
}
