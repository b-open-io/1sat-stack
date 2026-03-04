import Whitelist from "../sections/Whitelist";
import Blacklist from "../sections/Blacklist";
import Workers from "../sections/Workers";

export default function BSV21Page() {
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold">BSV21 Tokens</h2>
        <p className="text-sm text-muted-foreground">Token management and worker monitoring</p>
      </div>
      <Workers />
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <Whitelist />
        <Blacklist />
      </div>
    </div>
  );
}
