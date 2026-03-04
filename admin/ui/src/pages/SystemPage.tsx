import Topics from "../sections/Topics";
import Lookups from "../sections/Lookups";
import ZSetLookup from "../sections/ZSetLookup";
import Progress from "../sections/Progress";

export default function SystemPage() {
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold">System</h2>
        <p className="text-sm text-muted-foreground">Overlay engine status and debug tools</p>
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <Topics />
        <Lookups />
      </div>
      <ZSetLookup />
      <Progress />
    </div>
  );
}
