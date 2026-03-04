import { useState } from "react";
import { performSetup, getIdentityKey } from "../api";
import { toast } from "sonner";
import { toastError } from "@/lib/utils";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";

interface Props {
  onComplete: () => void;
}

export default function SetupWizard({ onComplete }: Props) {
  const [submitting, setSubmitting] = useState(false);
  const identityKey = getIdentityKey();

  async function handleSetup() {
    setSubmitting(true);
    try {
      await performSetup();
      toast.success("Admin identity configured");
      onComplete();
    } catch (e: any) {
      toastError(e.message || "Setup failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card className="max-w-lg mx-auto mt-8">
      <CardHeader>
        <CardTitle>Admin Setup</CardTitle>
        <CardDescription>
          No admin has been configured yet. Confirm your identity to become the initial admin.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {identityKey && (
          <div className="my-4 break-all font-mono text-sm p-3 bg-secondary rounded-md">
            {identityKey}
          </div>
        )}
        <Button onClick={handleSetup} disabled={submitting} className="w-full">
          {submitting ? "Configuring..." : "Confirm Admin Identity"}
        </Button>
      </CardContent>
    </Card>
  );
}
