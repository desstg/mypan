import type { MediaOrganizeTmdbSearchHit } from "@/api/mediaOrganize";

export function hitTitle(hit: MediaOrganizeTmdbSearchHit) {
  return hit.title || hit.name || hit.original_title || hit.original_name || "未命名";
}

export function hitYear(hit: MediaOrganizeTmdbSearchHit): number | undefined {
  const raw = hit.release_date || hit.first_air_date || "";
  const y = Number(String(raw).slice(0, 4));
  return y > 1900 ? y : undefined;
}

export function hitId(hit: MediaOrganizeTmdbSearchHit) {
  return String(hit.id ?? "");
}

export function hitMediaType(hit: MediaOrganizeTmdbSearchHit, fallback = "movie") {
  const t = (hit.media_type || "").toLowerCase();
  if (t === "tv" || t === "movie") return t;
  if (hit.name || hit.first_air_date) return "tv";
  if (hit.title || hit.release_date) return "movie";
  return fallback;
}

export function hitKey(hit: MediaOrganizeTmdbSearchHit) {
  return `${hitMediaType(hit)}:${hitId(hit)}`;
}

export function hitTypeLabel(hit: MediaOrganizeTmdbSearchHit) {
  return hitMediaType(hit) === "tv" ? "剧集" : "电影";
}

export function hitPosterURL(hit: MediaOrganizeTmdbSearchHit, size: "w154" | "w500" = "w154") {
  const path = (hit.poster_path || "").trim();
  if (!path) return "";
  if (path.startsWith("http://") || path.startsWith("https://")) return path;
  return `https://image.tmdb.org/t/p/${size}${path.startsWith("/") ? path : `/${path}`}`;
}
