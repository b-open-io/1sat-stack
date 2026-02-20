import { useEffect, useState, useCallback } from "react";
import { isWalletAvailable, initAuthFetch, fetchIdentityKey, getIdentityKey } from "./api";
import { useToast } from "./useToast";
import Whitelist from "./sections/Whitelist";
import Blacklist from "./sections/Blacklist";
import Workers from "./sections/Workers";
import Topics from "./sections/Topics";
import Lookups from "./sections/Lookups";
import ZSetLookup from "./sections/ZSetLookup";
import Progress from "./sections/Progress";
import "./styles.css";

export default function App() {
  const [walletReady, setWalletReady] = useState(false);
  const [identityKey, setIdentityKey] = useState<string | null>(null);
  const [walletChecked, setWalletChecked] = useState(false);
  const { toast, showToast } = useToast();

  useEffect(() => {
    // Yours Wallet may inject window.CWI asynchronously after page load.
    // Poll briefly to give it time to appear.
    let attempts = 0;
    const interval = setInterval(async () => {
      attempts++;
      if (isWalletAvailable()) {
        clearInterval(interval);
        if (initAuthFetch()) {
          setWalletReady(true);
          const key = await fetchIdentityKey();
          setIdentityKey(key);
        }
        setWalletChecked(true);
      } else if (attempts >= 20) {
        clearInterval(interval);
        setWalletChecked(true);
      }
    }, 250);
    return () => clearInterval(interval);
  }, []);

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
        <div className="identity-bar">
          {!walletChecked ? (
            <span>Detecting wallet...</span>
          ) : walletReady ? (
            <>
              <span className="status" />
              <span>Authenticated</span>
              {identityKey && (
                <span className="pubkey" title={`Click to copy: ${identityKey}`} onClick={copyIdentity}>
                  {identityKey}
                </span>
              )}
            </>
          ) : (
            <>
              <span className="status disconnected" />
              <span>No wallet detected</span>
            </>
          )}
        </div>
      </header>

      {walletChecked && !walletReady && (
        <div className="wallet-warning">
          No compatible wallet detected. Install Yours Wallet (or another wallet that injects <code>window.CWI</code>) to authenticate.
          Requests will be sent without authentication.
        </div>
      )}

      <div className="grid">
        <Whitelist showToast={showToast} />
        <Blacklist showToast={showToast} />
        <Workers showToast={showToast} />
        <Topics showToast={showToast} />
        <Lookups showToast={showToast} />
        <ZSetLookup showToast={showToast} />
        <Progress showToast={showToast} />
      </div>

      {toast && <div className={`toast ${toast.type}`}>{toast.message}</div>}
    </div>
  );
}
