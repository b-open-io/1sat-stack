export const ECOSYSTEM_ALIAS_KEYS = {
  enabled: "overlay.ecosystemalias.enabled",
  syncEnabled: "overlay.ecosystemalias.sync_enabled",
  subscriptionId: "overlay.ecosystemalias.sub_id",
  concurrency: "overlay.ecosystemalias.concurrency",
  batchSize: "overlay.ecosystemalias.batch_size",
  routesEnabled: "overlay.ecosystemalias.routes_enabled",
  routePrefix: "overlay.ecosystemalias.route_prefix",
  logLevel: "overlay.ecosystemalias.log_level",
} as const;

export interface EcosystemAliasSettings {
  enabled: boolean;
  syncEnabled: boolean;
  subscriptionId: string;
  concurrency: string;
  batchSize: string;
  routesEnabled: boolean;
  routePrefix: string;
  logLevel: string;
}

export interface EcosystemAliasValidationErrors {
  concurrency?: string;
  batchSize?: string;
  routePrefix?: string;
}

export const ECOSYSTEM_ALIAS_CONCURRENCY_RANGE = { min: 1, max: 64 } as const;
export const ECOSYSTEM_ALIAS_BATCH_SIZE_RANGE = { min: 1, max: 10_000 } as const;

export const DEFAULT_ECOSYSTEM_ALIAS_SETTINGS: EcosystemAliasSettings = {
  enabled: false,
  syncEnabled: false,
  subscriptionId: "",
  concurrency: "8",
  batchSize: "1000",
  routesEnabled: true,
  routePrefix: "/ecosystemalias",
  logLevel: "info",
};

export function readEcosystemAliasSettings(config: Record<string, string>): EcosystemAliasSettings {
  const value = (key: string, fallback: string) => config[key] ?? fallback;
  const enabled = (key: string, fallback: boolean) =>
    config[key] === undefined ? fallback : config[key] === "true";

  return {
    enabled: enabled(ECOSYSTEM_ALIAS_KEYS.enabled, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.enabled),
    syncEnabled: enabled(ECOSYSTEM_ALIAS_KEYS.syncEnabled, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.syncEnabled),
    subscriptionId: value(ECOSYSTEM_ALIAS_KEYS.subscriptionId, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.subscriptionId),
    concurrency: value(ECOSYSTEM_ALIAS_KEYS.concurrency, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.concurrency),
    batchSize: value(ECOSYSTEM_ALIAS_KEYS.batchSize, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.batchSize),
    routesEnabled: enabled(ECOSYSTEM_ALIAS_KEYS.routesEnabled, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.routesEnabled),
    routePrefix: value(ECOSYSTEM_ALIAS_KEYS.routePrefix, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.routePrefix),
    logLevel: value(ECOSYSTEM_ALIAS_KEYS.logLevel, DEFAULT_ECOSYSTEM_ALIAS_SETTINGS.logLevel),
  };
}

export function writeEcosystemAliasSettings(settings: EcosystemAliasSettings): Record<string, string> {
  return {
    [ECOSYSTEM_ALIAS_KEYS.enabled]: String(settings.enabled),
    [ECOSYSTEM_ALIAS_KEYS.syncEnabled]: String(settings.syncEnabled),
    [ECOSYSTEM_ALIAS_KEYS.subscriptionId]: settings.subscriptionId,
    [ECOSYSTEM_ALIAS_KEYS.concurrency]: settings.concurrency,
    [ECOSYSTEM_ALIAS_KEYS.batchSize]: settings.batchSize,
    [ECOSYSTEM_ALIAS_KEYS.routesEnabled]: String(settings.routesEnabled),
    [ECOSYSTEM_ALIAS_KEYS.routePrefix]: settings.routePrefix,
    [ECOSYSTEM_ALIAS_KEYS.logLevel]: settings.logLevel,
  };
}

function validateBoundedInteger(
  raw: string,
  label: string,
  range: { min: number; max: number },
): string | undefined {
  if (!/^[0-9]+$/.test(raw)) {
    return `${label} must be a whole number from ${range.min} to ${range.max}.`;
  }
  const value = Number(raw);
  if (!Number.isSafeInteger(value) || value < range.min || value > range.max) {
    return `${label} must be from ${range.min} to ${range.max}.`;
  }
  return undefined;
}

export function normalizeEcosystemAliasRoutePrefix(prefix: string): string {
  if (prefix.length === 0) throw new Error("Route prefix is required.");
  if (prefix.trim() !== prefix || /\s/.test(prefix)) throw new Error("Route prefix cannot contain whitespace.");
  if (!prefix.startsWith("/")) throw new Error("Route prefix must start with /.");
  if (/[?#]/.test(prefix)) throw new Error("Route prefix cannot contain a query or fragment.");

  if (!/^[A-Za-z0-9/._~-]+$/.test(prefix)) throw new Error("Route prefix must contain literal URL path characters.");
  const normalized = prefix.replace(/\/+$/, "");
  if (normalized.length === 0) throw new Error("Route prefix cannot be the root path.");
  if (normalized.includes("//") || normalized.split("/").some((segment) => segment === "." || segment === "..")) {
    throw new Error("Route prefix must be a canonical path.");
  }
  return normalized;
}

export function validateEcosystemAliasSettings(settings: EcosystemAliasSettings): EcosystemAliasValidationErrors {
  const errors: EcosystemAliasValidationErrors = {};
  errors.concurrency = validateBoundedInteger(
    settings.concurrency,
    "Concurrency",
    ECOSYSTEM_ALIAS_CONCURRENCY_RANGE,
  );
  errors.batchSize = validateBoundedInteger(
    settings.batchSize,
    "Batch size",
    ECOSYSTEM_ALIAS_BATCH_SIZE_RANGE,
  );
  try {
    normalizeEcosystemAliasRoutePrefix(settings.routePrefix);
  } catch (error) {
    errors.routePrefix = error instanceof Error ? error.message : "Route prefix is invalid.";
  }
  return Object.fromEntries(Object.entries(errors).filter(([, value]) => value !== undefined));
}

export function normalizeEcosystemAliasSettings(settings: EcosystemAliasSettings): EcosystemAliasSettings {
  return { ...settings, routePrefix: normalizeEcosystemAliasRoutePrefix(settings.routePrefix) };
}

export function ecosystemAliasLookupPath(basePath: string, routePrefix: string): string {
  const prefix = normalizeEcosystemAliasRoutePrefix(routePrefix);
  const base = basePath.replace(/\/+$/, "");
  if (base.length > 0 && (!base.startsWith("/") || /[\s?#]/.test(base) || base.includes("//") || base.split("/").some((segment) => segment === "." || segment === ".."))) {
    throw new Error("Server base path must be a canonical slash-prefixed path.");
  }
  return `${base}${prefix}/overlay/lookup`;
}
