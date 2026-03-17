import { useState } from "react";
import { KeyRound, Loader2, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

interface Props {
  onScan: (payWif: string, ordWif: string) => void;
  scanning: boolean;
  disabled: boolean;
}

export function WifInput({ onScan, scanning, disabled }: Props) {
  const [payWif, setPayWif] = useState("");
  const [ordWif, setOrdWif] = useState("");
  const [sameKey, setSameKey] = useState(true);

  function handleScan() {
    const pay = payWif.trim();
    const ord = sameKey ? pay : ordWif.trim();
    if (!pay) return;
    onScan(pay, ord);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="h-5 w-5" />
          Legacy Keys
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <label className="text-sm font-medium">
            {sameKey ? "Private Key (WIF)" : "Pay Key (WIF)"}
          </label>
          <Input
            type="password"
            placeholder="Enter WIF private key..."
            value={payWif}
            onChange={(e) => setPayWif(e.target.value)}
            disabled={disabled || scanning}
          />
        </div>

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={sameKey}
            onChange={(e) => setSameKey(e.target.checked)}
            disabled={disabled || scanning}
          />
          Same key for pay and ordinals
        </label>

        {!sameKey && (
          <div className="space-y-2">
            <label className="text-sm font-medium">Ordinals Key (WIF)</label>
            <Input
              type="password"
              placeholder="Enter ordinals WIF..."
              value={ordWif}
              onChange={(e) => setOrdWif(e.target.value)}
              disabled={disabled || scanning}
            />
          </div>
        )}

        <Button
          onClick={handleScan}
          disabled={disabled || scanning || !payWif.trim()}
          className="w-full"
        >
          {scanning ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <Search className="h-4 w-4 mr-2" />}
          {scanning ? "Scanning..." : "Scan for Assets"}
        </Button>
      </CardContent>
    </Card>
  );
}
