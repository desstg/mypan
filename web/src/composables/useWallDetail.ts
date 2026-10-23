import { onScopeDispose, ref } from "vue";
import { toast } from "@/composables/useToast";
import {
  fetchStrmScrapeItemDetail,
  fetchStrmScrapeWallEpisodes,
  type PlayableFile,
  type StrmScrapeItem,
  type WallDetail,
  type WallSeasonProgress,
  type WallSourceRef,
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

  /**
   * 剧集的**逐集进度**（详情抽屉的彩色格子）。
   *
   * 初值来自详情接口（`detail.seasons`），之后由下面那个定时器刷新 —— 订阅刚推下来
   * 一集、或者用户手动删了一集，抽屉开着的时候格子要自己变色。
   */
  const seasons = ref<WallSeasonProgress[]>([]);
  /**
   * 轮询间隔。
   *
   * 15 秒：订阅那边推完一集，用户一般正开在这个抽屉上。再快没意义（服务端每次要读
   * 一遍目录），再慢就赶不上「刚推下来的那一集」。
   */
  const POLL_MS = 15_000;
  /** 轮询用的源 + 作品目录。详情接口一并给了（`rel_dir` 由服务端算，前端不拼）。 */
  let pollSource: number | WallSourceRef | null = null;
  let pollRelDir = "";
  let pollTimer: number | null = null;

  function stopEpisodePolling() {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer);
      pollTimer = null;
    }
    pollSource = null;
    pollRelDir = "";
  }

  /** 拉一次逐集进度。**失败静默** —— 格子停在旧状态即可，不必弹错。 */
  async function refreshEpisodes() {
    if (!pollSource || !pollRelDir) return;
    // 页面在后台时不拉：用户看不见，白扫一遍盘。
    if (typeof document !== "undefined" && document.hidden) return;
    try {
      seasons.value = await fetchStrmScrapeWallEpisodes(pollSource, pollRelDir);
    } catch {
      /* 保持旧状态 */
    }
  }

  /**
   * 起轮询。**判据只看详情里有没有 `seasons`** —— 服务端只在剧集上给这个字段
   * （电影连算都不算），所以这里不需要再判一次媒体类型。
   */
  function startEpisodePolling(source: number | WallSourceRef) {
    stopEpisodePolling();
    const got = detail.value;
    if (!got?.rel_dir || !got.seasons?.length) return;
    pollSource = source;
    pollRelDir = got.rel_dir;
    pollTimer = window.setInterval(refreshEpisodes, POLL_MS);
  }

  /**
   * 暂停 / 继续。
   *
   * 从抽屉里点播放时抽屉会被收起来（见 suspendedByPlayer），格子看不见了 ——
   * 但**不能整份清掉**：关掉播放窗抽屉会弹回来，那时要接着刷（源与目录都还在）。
   */
  function pauseEpisodePolling() {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function resumeEpisodePolling() {
    if (pollTimer !== null || !pollSource || !pollRelDir) return;
    pollTimer = window.setInterval(refreshEpisodes, POLL_MS);
  }

  // 组件卸载时把定时器停掉：`setInterval` 不随组件销毁自动清，留着就是每 15 秒
  // 对着一个已经不存在的抽屉发请求（切走页面再回来会攒出好几个）。
  onScopeDispose(stopEpisodePolling);

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
    seasons.value = [];
    stopEpisodePolling();
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
      // 抽屉弹回来了，格子重新开始刷（源与目录在暂停时保留着）。
      resumeEpisodePolling();
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

  /**
   * TMDB 影片墙。`source` 是任务 id 或本地目录源（与 openJav 同形）。
   *
   * 两种源的详情接口是同一个，`local_dir_id` 优先（见后端 TMDBWallDetail）；
   * 老调用点传裸 taskId，行为不变。
   */
  async function openTMDB(source: number | WallSourceRef, item: StrmScrapeItem) {
    if (typeof source === "number" ? !source : !source.local_dir_id && !source.strm_task_id) return;
    seasons.value = [];
    await present(item.title, () => fetchStrmScrapeItemDetail(source, item.id));
    seasons.value = detail.value?.seasons ?? [];
    startEpisodePolling(source);
  }

  /** 番号影片墙。`source` 是任务 id 或本地目录源（见 api 的 WallSourceRef）。 */
  async function openJav(
    source: number | WallSourceRef,
    item: { id: string; rel_dir: string; stem: string; number?: string; title?: string },
  ) {
    if (typeof source === "number" ? !source : !source.local_dir_id && !source.strm_task_id) return;
    await present(item.number || item.title || item.stem, () =>
      fetchJavWallItemDetail({
        ...(typeof source === "number" ? { strm_task_id: source } : source),
        rel_dir: item.rel_dir,
        stem: item.stem,
      }),
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
      // 抽屉收起来了，格子看不见 —— 停止轮询，别在用户看片时一直扫盘。
      pauseEpisodePolling();
    }
  }

  return {
    open,
    loading,
    detail,
    error,
    seasons,
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
