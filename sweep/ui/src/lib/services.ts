import { OneSatServices } from "@1sat/client";

let _services: OneSatServices | null = null;

export function getServices(): OneSatServices {
  if (!_services) {
    const baseUrl = import.meta.env.VITE_API_URL || window.location.origin;
    _services = new OneSatServices("main", baseUrl);
  }
  return _services;
}
