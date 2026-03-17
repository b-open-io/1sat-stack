import { OneSatServices } from "@1sat/client";

let _services: OneSatServices | null = null;

export function getServices(): OneSatServices {
  if (!_services) {
    const sweepIdx = window.location.pathname.indexOf("/sweep");
    const basePath = sweepIdx >= 0
      ? window.location.pathname.substring(0, sweepIdx)
      : "";
    _services = new OneSatServices("main", `${window.location.origin}${basePath}`);
  }
  return _services;
}
