import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { isWalletAvailable, getIdentityKey } from "./api";
import { useEffect, useState } from "react";
import Layout from "./Layout";
import OpNSPage from "./pages/OpNSPage";
import BSV21Page from "./pages/BSV21Page";
import SystemPage from "./pages/SystemPage";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import "./styles.css";

function AppContent() {
  const [identityKey, setIdentityKey] = useState<string | null>(null);

  useEffect(() => {
    if (isWalletAvailable()) {
      const key = getIdentityKey();
      if (key) setIdentityKey(key);
    }
  }, []);

  return (
    <Layout>
      <Routes>
        <Route path="/" element={<OpNSPage identityKey={identityKey} />} />
        <Route path="/bsv21" element={<BSV21Page />} />
        <Route path="/system" element={<SystemPage />} />
        <Route path="*" element={<Navigate to="/" />} />
      </Routes>
    </Layout>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <TooltipProvider>
        <AppContent />
        <Toaster position="bottom-right" theme="dark" />
      </TooltipProvider>
    </BrowserRouter>
  );
}
