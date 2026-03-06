import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import Layout from "./Layout";
import OpNSPage from "./pages/OpNSPage";
import BSV21Page from "./pages/BSV21Page";
import SystemPage from "./pages/SystemPage";
import UsersPage from "./pages/UsersPage";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import "./styles.css";

function AppContent() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<OpNSPage />} />
        <Route path="/bsv21" element={<BSV21Page />} />
        <Route path="/system" element={<SystemPage />} />
        <Route path="/users" element={<UsersPage />} />
        <Route path="*" element={<Navigate to="/" />} />
      </Routes>
    </Layout>
  );
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
        <AppContent />
        <Toaster position="bottom-right" theme="dark" />
      </TooltipProvider>
    </BrowserRouter>
  );
}
