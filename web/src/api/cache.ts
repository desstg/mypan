import { http } from "./client";

export interface CacheStats {
  total_keys: number;
  total_size_bytes: number;
  hit_rate: number;
  hits?: number;
  misses?: number;
  evictions?: number;
  expirations?: number;
}

export function fetchCacheStats() {
  return http.get<CacheStats>("/admin/cache/stats");
}

export function clearCache() {
  return http.post<{ cleared_count: number }>("/admin/clear-cache", {});
}
