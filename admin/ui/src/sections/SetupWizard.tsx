import { useState } from "react";
import { performSetup, getIdentityKey } from "../api";

interface Props {
  onComplete: () => void;
  showToast: (msg: string, type?: "success" | "error") => void;
}

export default function SetupWizard({ onComplete, showToast }: Props) {
  const [submitting, setSubmitting] = useState(false);
  const identityKey = getIdentityKey();

  async function handleSetup() {
    setSubmitting(true);
    try {
      await performSetup();
      showToast("Admin identity configured");
      onComplete();
    } catch (e: any) {
      showToast(e.message || "Setup failed", "error");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="card" style={{ maxWidth: 520, margin: "2rem auto" }}>
      <div className="card-header">
        <h2>Admin Setup</h2>
      </div>
      <p className="card-description">
        No admin has been configured yet. Confirm your identity to become the initial admin.
      </p>
      {identityKey && (
        <div style={{ margin: "1rem 0", wordBreak: "break-all", fontFamily: "monospace", fontSize: "0.85rem", padding: "0.75rem", background: "var(--bg-secondary, #1a1a2e)", borderRadius: 6 }}>
          {identityKey}
        </div>
      )}
      <button onClick={handleSetup} disabled={submitting} style={{ width: "100%" }}>
        {submitting ? "Configuring..." : "Confirm Admin Identity"}
      </button>
    </div>
  );
}
