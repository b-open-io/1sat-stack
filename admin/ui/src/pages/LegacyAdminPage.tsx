import { useToast } from "../useToast";
import Whitelist from "../sections/Whitelist";
import Blacklist from "../sections/Blacklist";
import Workers from "../sections/Workers";
import Topics from "../sections/Topics";
import Lookups from "../sections/Lookups";
import ZSetLookup from "../sections/ZSetLookup";
import Progress from "../sections/Progress";

export default function LegacyAdminPage() {
  const { showToast } = useToast();

  return (
    <div className="page">
      <div className="admin-header">
        <h2>Legacy Admin Settings</h2>
        <p>System configuration and monitoring</p>
      </div>

      <div className="grid">
        <Whitelist showToast={showToast} />
        <Blacklist showToast={showToast} />
        <Workers showToast={showToast} />
        <Topics showToast={showToast} />
        <Lookups showToast={showToast} />
        <ZSetLookup showToast={showToast} />
        <Progress showToast={showToast} />
      </div>
    </div>
  );
}
