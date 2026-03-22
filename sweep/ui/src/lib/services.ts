import { OneSatServices } from "@1sat/client";

let _services: OneSatServices | null = null;

export function getServices(): OneSatServices {
  if (!_services) {
    _services = new OneSatServices("main");
  }
  return _services;
}
