import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { isWalletAvailable, getIdentityKey, getSetupStatus } from "./api";
import { useEffect, useState } from "react";
import Layout from "./Layout";
import OpNSPage from "./pages/OpNSPage";
import LegacyAdminPage from "./pages/LegacyAdminPage";
import { useToast } from "./useToast";
import "./styles.css";

type SetupStatus = "loading" | "needed" | "complete";

function AppContent() {
  const [setupStatus, setSetupStatus] = useState<SetupStatus>("loading");
  const [identityKey, setIdentityKey] = useState<string | null>(null);
  const { toast } = useToast();

  useEffect(() => {
    getSetupStatus()
      .then((s) => setSetupStatus(s.configured ? "complete" : "needed"))
      .catch(() => setSetupStatus("needed"));
    
    // Get identity key if wallet is connected
    if (isWalletAvailable()) {
      const key = getIdentityKey();
      if (key) setIdentityKey(key);
    }
  }, []);

  return (
    <Layout>
      <Routes>
        <Route path="/" element={<OpNSPage identityKey={identityKey} />} />
        <Route path="/admin" element={<LegacyAdminPage />} />
        <Route path="*" element={<Navigate to="/" />} />
      </Routes>
      {toast && <div className={`toast ${toast.type}`}>{toast.message}</div>}
    </Layout>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <AppContent />
    </BrowserRouter>
  );
}

