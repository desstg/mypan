<script setup lang="ts">
import { computed } from "vue";
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
 * # 只播一集，选集在抽屉里
 *
 * 这个窗**只接一集**（`file`）。多集作品选哪一集是**详情抽屉**的事
 * （它先弹选集面板），所以这里不做选集、也不给 VideoPreview 传多条 episodes ——
 * 那样会出现两个选集入口，用户不知道该用哪个。
 *
 * 早先这里是 `items: PlayableFile[]` + 「多集默认起第一集」那一套，那是
 * 「卡片直接播」时代的写法；那个入口已经去掉，这段逻辑成了永不生效的死路径。
 */
const props = withDefaults(
  defineProps<{
    /** 这一集的标题，只用于窗头。 */
    title: string;
    /** 要播的那一集。选集由详情抽屉负责，这里只接一个。 */
    file: PlayableFile | null;
    /**
     * 字幕候选。详情抽屉会直接给（它已按选中那一集算好）；
     * 不给则回落到从 `file.subtitles` 推。
     */
    subtitles?: SubtitleCandidate[] | null;
  }>(),
  { subtitles: null },
);

const emit = defineEmits<{ close: [] }>();

/**
 * 字幕候选。
 *
 * 字幕是**本地磁盘上**的文件（`.strm` 同目录的 `.srt/.vtt/.sup`），后端已经连
 * 内容地址一起给过来了，所以这里不需要任何网盘请求。
 */
const subtitles = computed<SubtitleCandidate[]>(() =>
  props.subtitles ?? toPlayableCandidates(props.file?.subtitles ?? []),
);

/** 复制这一集的播放地址（外部播放器那条路的兜底，本版先落地复制这一半）。 */
async function copyLink() {
  const url = props.file?.path;
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

defineExpose({ subtitles });
</script>

<template>
  <VideoPreview
    v-if="file"
    :files="[]"
    :initial-file-id="file.name"
    :direct-u-r-l="file.path"
    :direct-file-id="file.name"
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
