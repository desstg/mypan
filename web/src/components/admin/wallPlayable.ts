import type { SubtitleCandidate } from "@/components/file/VideoPreview.vue";
import type { PlayableSubtitle } from "@/api/strmScrape";

/**
 * 后端给的字幕清单 → VideoPreview 认的候选形状。
 *
 * 单独放一个模块是因为**两面墙都要用**（TMDB 墙与番号墙各有一个播放窗），
 * 而两边的后端返回值是同一个类型。抄两份的话，将来字幕字段一变就会有一边忘了跟。
 *
 * `id` 用 URL：VideoPreview 拿它当选中项的键与 `v-for` 的 key。它本来期望的是
 * 网盘 file_id，而墙上这条路的字幕没有 file_id —— URL 在同一目录内唯一，
 * 当键完全够用，也顺便让 `is-selected` 的比较不需要再挂一层。
 */
export function toPlayableCandidates(subtitles: PlayableSubtitle[] | undefined): SubtitleCandidate[] {
  return (subtitles ?? []).map((sub) => ({
    id: sub.url || sub.name,
    label: sub.label || sub.name,
    format: sub.format,
    url: sub.url,
  }));
}
