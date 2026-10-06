<script setup lang="ts">
import { ref } from "vue";
import VideoPreview from "@/components/file/VideoPreview.vue";
import type { SubtitleCandidate } from "@/components/file/VideoPreview.vue";
import { toPlayableCandidates } from "@/components/admin/wallPlayable";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import type { PlayableFile } from "@/api/strmScrape";

/**
 * 海报墙的播放窗：把「这一张卡能播什么」接进通用的 VideoPreview。
 *
 * # 为什么不直接把文件塞进 VideoPreview
 *
 * 墙上没有网盘 file 列表 —— 只有 `.strm` 正文里那一行播放地址。VideoPreview 的
 * 两条腿（`accountId + file_id` 和同目录字幕）在这儿都不成立，所以给它开了
 * `directURL` / `directSubtitles` 两个口子，由这一层做换算。
 *
 * # 多集
 *
 * 一次把该作品的**全部** `.strm` 都拿回来交给 VideoPreview 当 episodes，
 * 它自带的选集面板就会出现在左侧（条件是 `episodes.length > 1`），
 * 与在文件浏览器里播多集视频完全一样 —— 单集则没有面板，直接播。
 */
const props = defineProps<{
  /** 这一张卡的标题，只用于窗头。 */
  title: string;
  /** 后端给的可播文件清单（含每一条的同目录字幕）。 */
  items: PlayableFile[];
}>();

const emit = defineEmits<{ close: [] }>();

/**
 * 起播的那一条。
 *
 * 多集时默认起第一集，用户再在选集面板里切 —— 与文件浏览器打开一个多集目录的
 * 行为一致（那边也是从列表头开始）。
 */
const current = ref<PlayableFile | null>(props.items[0] ?? null);

/**
 * 字幕候选。
 *
 * 字幕是**本地磁盘上**的文件（`.strm` 同目录的 `.srt/.vtt/.sup`），后端已经连
 * 内容地址一起给过来了，所以这里不需要任何网盘请求。切集时不重新取：
 * 同一个作品目录下各集共用同一批字幕，后端也是按「目录」列的。
 */
const subtitles = ref<SubtitleCandidate[]>(toPlayableCandidates(props.items[0]?.subtitles ?? []));

/** 复制这一集的播放地址（外部播放器那条路的兜底，本版先落地复制这一半）。 */
async function copyLink() {
  const url = current.value?.path;
  if (!url) return;
  await copyTextToClipboard(url, {
    successMessage: "播放地址已复制",
    errorMessage: "复制失败，请手动复制",
  });
}

/** 外部播放器调起：本版占位，先把入口摆出来。 */
function openExternal() {
  toast.info("「用本机播放器打开」还在开发中，可以先用「复制链接」粘到播放器里");
}

defineExpose({ current, subtitles });
</script>

<template>
  <VideoPreview
    v-if="current"
    :files="[]"
    :initial-file-id="current.name"
    :direct-u-r-l="current.path"
    :direct-file-id="current.name"
    :direct-subtitles="subtitles"
    @close="emit('close')"
  >
    <template #actions>
      <button type="button" class="wall-player__act" title="复制播放地址" @click="copyLink">复制链接</button>
      <button type="button" class="wall-player__act" title="用本机播放器打开" @click="openExternal">
        用播放器打开
      </button>
    </template>
  </VideoPreview>
</template>

<style scoped>
.wall-player__act {
  padding: 5px 10px;
  border: 1px solid rgba(255, 255, 255, 0.28);
  border-radius: var(--radius-pill);
  background: transparent;
  color: inherit;
  font-size: 12.5px;
  cursor: pointer;
  transition: background 0.15s ease, border-color 0.15s ease;
}

.wall-player__act:hover {
  background: rgba(255, 255, 255, 0.12);
  border-color: rgba(255, 255, 255, 0.45);
}
</style>
