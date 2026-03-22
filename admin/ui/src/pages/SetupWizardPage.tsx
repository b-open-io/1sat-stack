import { useState, useEffect } from "react";
import { Eye, EyeOff, ChevronDown, ChevronRight, Check, Server, Laptop, Database, Link, HardDrive, Wallet } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { useWallet } from "@1sat/react";

type AuthMode = "local" | "authenticated" | null;
type WalletBackend = "sqlite" | "postgres";
type ChaintracksBackend = "embedded" | "remote";
type ArcadeBackend = "embedded" | "remote";

interface SetupState {
  authMode: AuthMode;
  wif: string;
  adminIdentityKey: string;
  walletBackend: WalletBackend;
  walletSqlitePath: string;
  walletPostgresUrl: string;
  chaintracksBackend: ChaintracksBackend;
  chaintracksPath: string;
  chaintracksUrl: string;
  arcadeBackend: ArcadeBackend;
  arcadePath: string;
  arcadeUrl: string;
  messageboxPath: string;
}

function StepIndicator({ step, current }: { step: number; current: number }) {
  const done = step < current;
  const active = step === current;
  return (
    <div className="flex items-center gap-2">
      <div
        className={cn(
          "w-7 h-7 rounded-full flex items-center justify-center text-xs font-semibold transition-colors",
          done && "bg-success text-white",
          active && "bg-primary text-white",
          !done && !active && "bg-muted text-muted-foreground"
        )}
      >
        {done ? <Check className="w-3.5 h-3.5" /> : step}
      </div>
      <span className={cn("text-sm", active ? "text-foreground font-medium" : "text-muted-foreground")}>
        {step === 1 && "Mode & Identity"}
        {step === 2 && "Databases"}
        {step === 3 && "Review"}
      </span>
    </div>
  );
}

function CollapsibleSection({
  label,
  summary,
  children,
  defaultOpen = false,
}: {
  label: string;
  summary: string;
  children: React.ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <button
        type="button"
        className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-muted/40 transition-colors"
        onClick={() => setOpen((o) => !o)}
      >
        <div>
          <span className="text-sm font-medium text-foreground">{label}</span>
          {!open && (
            <span className="ml-3 text-xs text-muted-foreground font-mono">{summary}</span>
          )}
        </div>
        {open ? (
          <ChevronDown className="w-4 h-4 text-muted-foreground shrink-0" />
        ) : (
          <ChevronRight className="w-4 h-4 text-muted-foreground shrink-0" />
        )}
      </button>
      {open && <div className="px-4 pb-4 pt-1 space-y-3 border-t border-border">{children}</div>}
    </div>
  );
}

function RadioCard({
  selected,
  onClick,
  icon: Icon,
  title,
  description,
}: {
  selected: boolean;
  onClick: () => void;
  icon: React.ElementType;
  title: string;
  description: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "w-full text-left rounded-xl border-2 p-5 transition-all",
        selected
          ? "border-primary bg-primary/8"
          : "border-border bg-card hover:border-muted-foreground/40"
      )}
    >
      <div className="flex items-start gap-3">
        <div
          className={cn(
            "mt-0.5 p-2 rounded-lg",
            selected ? "bg-primary/20 text-primary" : "bg-muted text-muted-foreground"
          )}
        >
          <Icon className="w-4 h-4" />
        </div>
        <div>
          <div className={cn("text-sm font-semibold", selected ? "text-foreground" : "text-foreground/80")}>
            {title}
          </div>
          <div className="text-xs text-muted-foreground mt-0.5 leading-relaxed">{description}</div>
        </div>
        <div
          className={cn(
            "ml-auto mt-0.5 w-4 h-4 rounded-full border-2 shrink-0 transition-colors",
            selected ? "border-primary bg-primary" : "border-muted-foreground/40"
          )}
        />
      </div>
    </button>
  );
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between py-2.5 border-b border-border last:border-0">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="text-sm font-mono text-foreground">{value}</span>
    </div>
  );
}

export default function SetupWizardPage() {
  const { connect, disconnect, identityKey: walletIdentityKey, status: walletStatus } = useWallet();
  const [step, setStep] = useState(1);
  const [showWif, setShowWif] = useState(false);
  const [completing, setCompleting] = useState(false);
  const [walletError, setWalletError] = useState<string | null>(null);
  const [state, setState] = useState<SetupState>({
    authMode: null,
    wif: "",
    adminIdentityKey: "",
    walletBackend: "sqlite",
    walletSqlitePath: "wallet.sqlite",
    walletPostgresUrl: "",
    chaintracksBackend: "embedded",
    chaintracksPath: "chaintracks",
    chaintracksUrl: "",
    arcadeBackend: "embedded",
    arcadePath: "arcade/arcade.db",
    arcadeUrl: "",
    messageboxPath: "messagebox.db",
  });

  function set<K extends keyof SetupState>(key: K, value: SetupState[K]) {
    setState((s) => ({ ...s, [key]: value }));
  }

  // Sync wallet identity key into setup state
  useEffect(() => {
    if (walletIdentityKey) {
      set("adminIdentityKey", walletIdentityKey);
    }
  }, [walletIdentityKey]);

  function setAuthMode(mode: AuthMode) {
    if (mode !== "authenticated" && state.adminIdentityKey) {
      disconnect();
      set("adminIdentityKey", "");
    }
    set("authMode", mode);
    setWalletError(null);
  }

  async function handleConnectWallet() {
    setWalletError(null);
    try {
      await connect();
    } catch (e: any) {
      setWalletError(e.message || "Failed to connect wallet");
    }
  }

  function handleDisconnectWallet() {
    disconnect();
    set("adminIdentityKey", "");
  }

  function walletSummary() {
    if (state.walletBackend === "postgres") return state.walletPostgresUrl || "postgres (no URL set)";
    return state.walletSqlitePath;
  }

  function chaintracksSummary() {
    if (state.chaintracksBackend === "remote") return state.chaintracksUrl || "remote (no URL set)";
    return state.chaintracksPath;
  }

  function arcadeSummary() {
    if (state.arcadeBackend === "remote") return state.arcadeUrl || "remote (no URL set)";
    return state.arcadePath;
  }

  async function handleComplete() {
    setCompleting(true);
    try {
      const adminIdx = window.location.pathname.indexOf("/admin");
      const setupBase =
        window.location.origin +
        (adminIdx >= 0 ? window.location.pathname.substring(0, adminIdx + "/admin".length) : "") +
        "/setup";

      const res = await fetch(setupBase + "/complete", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          authMode: state.authMode,
          wif: state.wif || undefined,
          adminIdentityKey: state.adminIdentityKey || undefined,
          walletBackend: state.walletBackend,
          walletSqlitePath: state.walletSqlitePath,
          walletPostgresUrl: state.walletPostgresUrl || undefined,
          chaintracksBackend: state.chaintracksBackend,
          chaintracksPath: state.chaintracksPath,
          chaintracksUrl: state.chaintracksUrl || undefined,
          arcadeBackend: state.arcadeBackend,
          arcadePath: state.arcadePath,
          arcadeUrl: state.arcadeUrl || undefined,
          messageboxPath: state.messageboxPath,
        }),
      });

      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || "Setup failed");
      }

      window.location.href = window.location.pathname.replace(/\/setup$/, "") || "/";
    } catch (e: any) {
      alert(e.message || "Failed to complete setup");
    } finally {
      setCompleting(false);
    }
  }

  return (
    <div className="min-h-screen bg-background flex items-center justify-center p-6">
      <div className="w-full max-w-xl">
        {/* Header */}
        <div className="mb-8 text-center">
          <div className="inline-flex items-center gap-1.5 mb-4">
            <div className="w-2 h-2 rounded-full bg-primary" />
            <span className="text-xs font-mono text-muted-foreground tracking-widest uppercase">
              1sat stack
            </span>
          </div>
          <h1 className="text-3xl font-semibold tracking-tight text-foreground">
            Set up your node
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Takes about a minute. Most settings can be changed later.
          </p>
        </div>

        {/* Step indicators */}
        <div className="flex items-center gap-6 mb-8 px-1">
          <StepIndicator step={1} current={step} />
          <div className="flex-1 h-px bg-border" />
          <StepIndicator step={2} current={step} />
          <div className="flex-1 h-px bg-border" />
          <StepIndicator step={3} current={step} />
        </div>

        {/* Step 1 */}
        {step === 1 && (
          <div className="space-y-4">
            <div>
              <h2 className="text-base font-semibold mb-1">How will you use this node?</h2>
              <p className="text-xs text-muted-foreground mb-4">
                This determines how admin access is protected.
              </p>
              <div className="space-y-3">
                <RadioCard
                  selected={state.authMode === "local"}
                  onClick={() => setAuthMode("local")}
                  icon={Laptop}
                  title="Local mode"
                  description="Personal node on your machine. No wallet required for admin — access is unrestricted on localhost."
                />
                <RadioCard
                  selected={state.authMode === "authenticated"}
                  onClick={() => setAuthMode("authenticated")}
                  icon={Server}
                  title="Authenticated mode"
                  description="Remote server or shared infrastructure. Wallet-based admin login required for all admin operations."
                />
              </div>
            </div>

            {state.authMode === "local" && (
              <div className="rounded-xl border border-border bg-card p-4 space-y-3">
                <div>
                  <p className="text-sm font-medium mb-0.5">Import existing identity</p>
                  <p className="text-xs text-muted-foreground">
                    Optional. Leave blank to generate a new identity automatically.
                  </p>
                </div>
                <div className="relative">
                  <Input
                    type={showWif ? "text" : "password"}
                    value={state.wif}
                    onChange={(e) => set("wif", e.target.value)}
                    placeholder="WIF private key"
                    className="font-mono text-xs pr-10"
                  />
                  <button
                    type="button"
                    onClick={() => setShowWif((v) => !v)}
                    className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  >
                    {showWif ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                  </button>
                </div>
              </div>
            )}

            {state.authMode === "authenticated" && (
              <div className="rounded-xl border border-border bg-card p-4 space-y-3">
                <div>
                  <p className="text-sm font-medium mb-0.5">Connect admin wallet</p>
                  <p className="text-xs text-muted-foreground">
                    Connect your wallet to register as the first admin.
                  </p>
                </div>
                {state.adminIdentityKey ? (
                  <div className="space-y-2">
                    <div className="flex items-center gap-2 p-2.5 rounded-md bg-success/10 border border-success/20">
                      <div className="w-2 h-2 rounded-full bg-success shrink-0" />
                      <span className="text-xs font-mono text-foreground truncate">{state.adminIdentityKey}</span>
                    </div>
                    <button
                      type="button"
                      onClick={handleDisconnectWallet}
                      className="text-xs text-muted-foreground hover:text-foreground transition-colors"
                    >
                      Disconnect and use a different wallet
                    </button>
                  </div>
                ) : (
                  <div className="space-y-2">
                    <Button
                      variant="secondary"
                      className="w-full"
                      onClick={handleConnectWallet}
                      disabled={walletStatus === "connecting"}
                    >
                      <Wallet className="w-4 h-4 mr-2" />
                      {walletStatus === "connecting" ? "Connecting..." : "Connect Wallet"}
                    </Button>
                    {walletError && (
                      <p className="text-xs text-destructive">{walletError}</p>
                    )}
                  </div>
                )}
              </div>
            )}

            <Button
              className="w-full mt-2"
              disabled={!state.authMode || (state.authMode === "authenticated" && !state.adminIdentityKey)}
              onClick={() => setStep(2)}
            >
              Continue
            </Button>
          </div>
        )}

        {/* Step 2 */}
        {step === 2 && (
          <div className="space-y-4">
            <div>
              <h2 className="text-base font-semibold mb-1">Database configuration</h2>
              <p className="text-xs text-muted-foreground mb-4">
                Defaults work for most users. Expand a section to change it.
              </p>
            </div>

            <CollapsibleSection label="Wallet" summary={walletSummary()}>
              <div className="space-y-3">
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() => set("walletBackend", "sqlite")}
                    className={cn(
                      "flex-1 py-1.5 px-3 text-xs rounded-md border transition-colors",
                      state.walletBackend === "sqlite"
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border text-muted-foreground hover:border-muted-foreground/50"
                    )}
                  >
                    <HardDrive className="w-3 h-3 inline mr-1.5" />SQLite
                  </button>
                  <button
                    type="button"
                    onClick={() => set("walletBackend", "postgres")}
                    className={cn(
                      "flex-1 py-1.5 px-3 text-xs rounded-md border transition-colors",
                      state.walletBackend === "postgres"
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border text-muted-foreground hover:border-muted-foreground/50"
                    )}
                  >
                    <Database className="w-3 h-3 inline mr-1.5" />Postgres
                  </button>
                </div>
                {state.walletBackend === "sqlite" ? (
                  <Input
                    value={state.walletSqlitePath}
                    onChange={(e) => set("walletSqlitePath", e.target.value)}
                    placeholder="wallet.sqlite"
                    className="font-mono text-xs"
                  />
                ) : (
                  <Input
                    value={state.walletPostgresUrl}
                    onChange={(e) => set("walletPostgresUrl", e.target.value)}
                    placeholder="postgres://user:pass@host:5432/wallet"
                    className="font-mono text-xs"
                  />
                )}
              </div>
            </CollapsibleSection>

            <CollapsibleSection label="Chaintracks" summary={chaintracksSummary()}>
              <div className="space-y-3">
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() => set("chaintracksBackend", "embedded")}
                    className={cn(
                      "flex-1 py-1.5 px-3 text-xs rounded-md border transition-colors",
                      state.chaintracksBackend === "embedded"
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border text-muted-foreground hover:border-muted-foreground/50"
                    )}
                  >
                    <HardDrive className="w-3 h-3 inline mr-1.5" />Embedded
                  </button>
                  <button
                    type="button"
                    onClick={() => set("chaintracksBackend", "remote")}
                    className={cn(
                      "flex-1 py-1.5 px-3 text-xs rounded-md border transition-colors",
                      state.chaintracksBackend === "remote"
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border text-muted-foreground hover:border-muted-foreground/50"
                    )}
                  >
                    <Link className="w-3 h-3 inline mr-1.5" />Remote
                  </button>
                </div>
                {state.chaintracksBackend === "embedded" ? (
                  <Input
                    value={state.chaintracksPath}
                    onChange={(e) => set("chaintracksPath", e.target.value)}
                    placeholder="chaintracks"
                    className="font-mono text-xs"
                  />
                ) : (
                  <Input
                    value={state.chaintracksUrl}
                    onChange={(e) => set("chaintracksUrl", e.target.value)}
                    placeholder="https://chaintracks.example.com"
                    className="font-mono text-xs"
                  />
                )}
              </div>
            </CollapsibleSection>

            <CollapsibleSection label="Arcade" summary={arcadeSummary()}>
              <div className="space-y-3">
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() => set("arcadeBackend", "embedded")}
                    className={cn(
                      "flex-1 py-1.5 px-3 text-xs rounded-md border transition-colors",
                      state.arcadeBackend === "embedded"
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border text-muted-foreground hover:border-muted-foreground/50"
                    )}
                  >
                    <HardDrive className="w-3 h-3 inline mr-1.5" />Embedded
                  </button>
                  <button
                    type="button"
                    onClick={() => set("arcadeBackend", "remote")}
                    className={cn(
                      "flex-1 py-1.5 px-3 text-xs rounded-md border transition-colors",
                      state.arcadeBackend === "remote"
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border text-muted-foreground hover:border-muted-foreground/50"
                    )}
                  >
                    <Link className="w-3 h-3 inline mr-1.5" />Remote
                  </button>
                </div>
                {state.arcadeBackend === "embedded" ? (
                  <Input
                    value={state.arcadePath}
                    onChange={(e) => set("arcadePath", e.target.value)}
                    placeholder="arcade/arcade.db"
                    className="font-mono text-xs"
                  />
                ) : (
                  <Input
                    value={state.arcadeUrl}
                    onChange={(e) => set("arcadeUrl", e.target.value)}
                    placeholder="https://arcade.example.com"
                    className="font-mono text-xs"
                  />
                )}
              </div>
            </CollapsibleSection>

            <CollapsibleSection label="MessageBox" summary={state.messageboxPath}>
              <div className="space-y-3">
                <Input
                  value={state.messageboxPath}
                  onChange={(e) => set("messageboxPath", e.target.value)}
                  placeholder="messagebox.db"
                  className="font-mono text-xs"
                />
              </div>
            </CollapsibleSection>

            <div className="flex gap-3 pt-1">
              <Button variant="secondary" className="flex-1" onClick={() => setStep(1)}>
                Back
              </Button>
              <Button className="flex-1" onClick={() => setStep(3)}>
                Continue
              </Button>
            </div>
          </div>
        )}

        {/* Step 3 */}
        {step === 3 && (
          <div className="space-y-4">
            <div>
              <h2 className="text-base font-semibold mb-1">Review your setup</h2>
              <p className="text-xs text-muted-foreground mb-4">
                The server will restart once to apply these settings.
              </p>
            </div>

            <Card>
              <CardContent className="p-0">
                <div className="px-4">
                  <SummaryRow
                    label="Mode"
                    value={state.authMode === "local" ? "Local (unrestricted)" : "Authenticated (wallet required)"}
                  />
                  {state.authMode === "local" && (
                    <SummaryRow
                      label="Identity"
                      value={state.wif ? "Imported from WIF" : "Auto-generated"}
                    />
                  )}
                  {state.authMode === "authenticated" && state.adminIdentityKey && (
                    <SummaryRow
                      label="Admin"
                      value={state.adminIdentityKey.slice(0, 8) + "..." + state.adminIdentityKey.slice(-8)}
                    />
                  )}
                  <SummaryRow
                    label="Wallet"
                    value={walletSummary()}
                  />
                  <SummaryRow
                    label="Chaintracks"
                    value={chaintracksSummary()}
                  />
                  <SummaryRow
                    label="Arcade"
                    value={arcadeSummary()}
                  />
                  <SummaryRow
                    label="MessageBox"
                    value={state.messageboxPath}
                  />
                </div>
              </CardContent>
            </Card>

            <div className="flex gap-3 pt-1">
              <Button variant="secondary" className="flex-1" onClick={() => setStep(2)}>
                Back
              </Button>
              <Button className="flex-1" onClick={handleComplete} disabled={completing}>
                {completing ? "Applying..." : "Complete Setup"}
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
