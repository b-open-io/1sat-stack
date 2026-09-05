import { describe, expect, test } from "bun:test";
import {
  DEFAULT_ECOSYSTEM_ALIAS_SETTINGS,
  ECOSYSTEM_ALIAS_KEYS,
  readEcosystemAliasSettings,
  writeEcosystemAliasSettings,
  validateEcosystemAliasSettings,
  normalizeEcosystemAliasSettings,
  ecosystemAliasLookupPath,
} from "../src/lib/ecosystem-alias-settings";

describe("ecosystem alias settings", () => {
  test("uses safe disabled defaults while leaving standard routes enabled", () => {
    expect(readEcosystemAliasSettings({})).toEqual(DEFAULT_ECOSYSTEM_ALIAS_SETTINGS);
  });

  test("preserves explicit false values and operator overrides", () => {
    expect(readEcosystemAliasSettings({
      [ECOSYSTEM_ALIAS_KEYS.enabled]: "false",
      [ECOSYSTEM_ALIAS_KEYS.syncEnabled]: "true",
      [ECOSYSTEM_ALIAS_KEYS.routesEnabled]: "false",
      [ECOSYSTEM_ALIAS_KEYS.routePrefix]: "/identity",
      [ECOSYSTEM_ALIAS_KEYS.logLevel]: "debug",
    })).toMatchObject({
      enabled: false,
      syncEnabled: true,
      routesEnabled: false,
      routePrefix: "/identity",
      logLevel: "debug",
    });
  });

  test("writes every restart-bound setting, including false toggles", () => {
    const values = writeEcosystemAliasSettings({
      ...DEFAULT_ECOSYSTEM_ALIAS_SETTINGS,
      enabled: true,
      routesEnabled: false,
      subscriptionId: "sub_123",
    });

    expect(values).toEqual({
      [ECOSYSTEM_ALIAS_KEYS.enabled]: "true",
      [ECOSYSTEM_ALIAS_KEYS.syncEnabled]: "false",
      [ECOSYSTEM_ALIAS_KEYS.subscriptionId]: "sub_123",
      [ECOSYSTEM_ALIAS_KEYS.concurrency]: "8",
      [ECOSYSTEM_ALIAS_KEYS.batchSize]: "1000",
      [ECOSYSTEM_ALIAS_KEYS.routesEnabled]: "false",
      [ECOSYSTEM_ALIAS_KEYS.routePrefix]: "/ecosystemalias",
      [ECOSYSTEM_ALIAS_KEYS.logLevel]: "info",
    });
  });
});

 test("rejects invalid worker values and nonliteral routes before saving", () => {
  for (const concurrency of ["0", "65", "1.5", " 8", "NaN"]) {
   expect(validateEcosystemAliasSettings({...DEFAULT_ECOSYSTEM_ALIAS_SETTINGS, concurrency}).concurrency).toBeDefined();
  }
  for (const routePrefix of ["", "/", "identity", "/:alias", "/*", "/%2f", "/a/../b", "/a//b"]) {
   expect(validateEcosystemAliasSettings({...DEFAULT_ECOSYSTEM_ALIAS_SETTINGS, routePrefix}).routePrefix).toBeDefined();
  }
  expect(validateEcosystemAliasSettings({...DEFAULT_ECOSYSTEM_ALIAS_SETTINGS, batchSize: "10001"}).batchSize).toBeDefined();
  expect(validateEcosystemAliasSettings(DEFAULT_ECOSYSTEM_ALIAS_SETTINGS)).toEqual({});
 });
 test("saved prefix and lookup preview use the same normalization", () => {
  const settings = normalizeEcosystemAliasSettings({...DEFAULT_ECOSYSTEM_ALIAS_SETTINGS, routePrefix: "/identity/"});
  expect(settings.routePrefix).toBe("/identity");
  expect(ecosystemAliasLookupPath("/1sat/", settings.routePrefix)).toBe("/1sat/identity/overlay/lookup");
  expect(() => ecosystemAliasLookupPath("/a/../b", settings.routePrefix)).toThrow();
 });
