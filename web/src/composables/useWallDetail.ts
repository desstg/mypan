import { ref } from "vue";
import { toast } from "@/composables/useToast";
import {
  fetchStrmScrapeItemDetail,
  type PlayableFile,
  type StrmScrapeItem,
  type WallDetail,
} from "@/api/strmScrape";
import { fetchJavWallItemDetail } from "@/api/strmJavWall";
import { toPlayableCandidates } from "@/components/admin/wallPlayable";
import type { SubtitleCandidate } from "@/components/file/VideoPreview.vue";

/**
 * 「这一张卡点了」这件事的收口处：开详情抽屉 + 从抽屉里播放。
 *
 * # 为什么两面墙共用一个
 *
 * 卡片类型与入参完全不同（TMDB 给 `item_id`，番号给 `rel_dir + stem`），
 * 但差别只在**取详情那一行请求**上。拿到 `WallDetail` 之后：抽屉、选集、
 * 播放窗、字幕、报错 —— 全都一样。所以这里只做「按墙的类型取数 + 统一报错」。
 *
 * # 请求只发生在点击时
 *
 * 别把这段挪进卡片渲染 —— 一面墙几十上百张卡，每张都要读 nfo / 侧车 / 列
 * extrafanart，既拖慢列表也白读盘。
 */
export function useWallDetail() {
  const open = ref(false);
  const loading = ref(false);
  const detail = ref<WallDetail | null>(null);
  const error = ref("");

  // —— 播放窗（从抽屉里发起）——
  const playerOpen = ref(false);
  /** 正在播的那一集。**只接一个** —— 选集在抽屉里做完了，见 WallPlayer 的说明。 */
  const playerFile = ref<PlayableFile | null>(null);
  /**
   * 播放期间抽屉是不是被我们收起来了，关掉播放窗后要把它放回来。
   *
   * 为什么必须收起：播放窗 `.file-preview` 的 z-index 是 **10000**，而详情抽屉
   * 是 elevated 的 **10040** —— 抽屉压在上面，播放窗会被整个盖住（用户报的就是这个）。
   * **不抬播放窗的 z-index**：它是全局外壳（文件浏览器那边也在用），10000 是刻意的
   * （低于 --z-dropdown 10050，好让下拉菜单浮在预览之上），抬它会波及所有预览场景。
   */
  const suspendedByPlayer = ref(false);
  const playerTitle = ref("");
  const playerSubtitles = ref<SubtitleCandidate[]>([]);

  function close() {
    open.value = false;
    detail.value = null;
    error.value = "";
    // 用户主动关抽屉时清掉这个标记：否则下次关播放窗会凭空把抽屉弹回来。
    suspendedByPlayer.value = false;
  }

  function closePlayer() {
    playerOpen.value = false;
    playerFile.value = null;
    playerSubtitles.value = [];
    playerTitle.value = "";
    // 关掉播放窗就把详情抽屉放回来 —— 连续看几集时不用每次重新点卡片。
    if (suspendedByPlayer.value) {
      suspendedByPlayer.value = false;
      open.value = true;
    }
  }

  /** 取到详情就开抽屉；失败时说清楚原因，不要静默无反应。 */
  async function present(label: string, fetcher: () => Promise<WallDetail>) {
    open.value = true;
    loading.value = true;
    error.value = "";
    detail.value = null;
    try {
      const got = await fetcher();
      detail.value = got;
      // 抽屉标题用卡片的标题打底：详情里万一没有标题（nfo 缺失），
      // 也不至于显示成「详情」两个字。
      if (!got.title && !got.number) detail.value = { ...got, title: label };
    } catch (err) {
      error.value = errorMessageOf(err);
    } finally {
      loading.value = false;
    }
  }

  /** TMDB 影片墙。 */
  async function openTMDB(taskId: number, item: StrmScrapeItem) {
    if (!taskId) return;
    await present(item.title, () => fetchStrmScrapeItemDetail(taskId, item.id));
  }

  /** 番号影片墙。 */
  async function openJav(
    taskId: number,
    item: { id: string; rel_dir: string; stem: string; number?: string; title?: string },
  ) {
    if (!taskId) return;
    await present(item.number || item.title || item.stem, () =>
      fetchJavWallItemDetail({ strm_task_id: taskId, rel_dir: item.rel_dir, stem: item.stem }),
    );
  }

  /**
   * 从抽屉里播放某一集。
   *
   * 字幕跟着这一集走：后端在每条可播文件上都带了同目录的字幕清单
   * （字幕是本地文件，不需要网盘 file_id）。
   */
  function play(file: PlayableFile) {
    if (!file?.path) {
      toast.info("这一集没有可用的播放地址");
      return;
    }
    playerTitle.value = file.name;
    playerFile.value = file;
    playerSubtitles.value = toPlayableCandidates(file.subtitles);
    playerOpen.value = true;
    // 收起详情抽屉，否则它会盖住全屏播放窗（见 suspendedByPlayer 的说明）。
    if (open.value) {
      suspendedByPlayer.value = true;
      open.value = false;
    }
  }

  return {
    open,
    loading,
    detail,
    error,
    close,
    openTMDB,
    openJav,
    // 播放窗
    playerOpen,
    playerTitle,
    playerFile,
    playerSubtitles,
    closePlayer,
    play,
  };
}

/** 把接口报错落成一句人话。抽出来是为了两条路给同一种口径。 */
function errorMessageOf(error: unknown): string {
  const fallback = "读取详情失败";
  const message = error instanceof Error && error.message ? error.message : fallback;
  // 常见的一种：`.strm` 里的 host 是旧的（后台 base URL 改过而 STRM 没重生成）。
  // 那种必须说出来，否则用户只看到「打不开」，会以为片子坏了。
  return `${message}（若是刚改过访问地址，可重跑一次 STRM 任务刷新 .strm 正文）`;
}
