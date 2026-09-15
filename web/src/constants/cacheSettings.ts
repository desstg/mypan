export const WEBDAV_CACHE_SETTING_KEY = "webdav_cache_enabled";

export const CACHE_SETTING_KEYS = new Set([
  "cache_enabled",
  "cache_ttl",
  "cache_max_items",
  "cache_memory_limit_mb",
  "cache_persistence_enabled",
  "cache_persistence_interval_minutes",
  WEBDAV_CACHE_SETTING_KEY,
]);

export function isCacheSettingKey(key: string): boolean {
  return CACHE_SETTING_KEYS.has(key);
}
