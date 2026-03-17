import { useState } from "react";
import { Loader2, Wallet, CheckCircle2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { connectWallet, disconnectWallet, getIdentityKey, getProvider } from "@/lib/wallet";

interface Props {
  onConnected: () => void;
  onDisconnected: () => void;
  connected: boolean;
}

export function ConnectWallet({ onConnected, onDisconnected, connected }: Props) {
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleConnect() {
    setConnecting(true);
    setError(null);
    try {
      await connectWallet();
      onConnected();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to connect wallet");
    } finally {
      setConnecting(false);
    }
  }

  function handleDisconnect() {
    disconnectWallet();
    onDisconnected();
  }

  if (connected) {
    return (
      <Card>
        <CardContent className="flex items-center justify-between py-4">
          <div className="flex items-center gap-3">
            <CheckCircle2 className="h-5 w-5 text-green-500" />
            <div>
              <div className="text-sm font-medium">Wallet Connected</div>
              <div className="text-xs text-muted-foreground">
                {getProvider() === "brc100" ? "BRC-100" : "OneSat"} · {getIdentityKey()?.slice(0, 12)}...
              </div>
            </div>
          </div>
          <Button variant="ghost" size="sm" onClick={handleDisconnect}>
            <X className="h-4 w-4" />
          </Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Wallet className="h-5 w-5" />
          Connect Destination Wallet
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Connect your BRC-100 wallet to receive swept assets.
        </p>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button onClick={handleConnect} disabled={connecting} className="w-full">
          {connecting ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : null}
          {connecting ? "Connecting..." : "Connect Wallet"}
        </Button>
      </CardContent>
    </Card>
  );
}
