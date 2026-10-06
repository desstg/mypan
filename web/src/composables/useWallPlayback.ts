import { ref } from "vue";
import { toast } from "@/composables/useToast";
import { fetchStrmScrapeItemPlayable, type PlayableFile, type StrmScrapeItem } from "@/api/strmScrape";
import { fetchJavWallItemPlayable } from "@/api/strmJavWall";

/**
 * 「这一张卡点了播放」这件事的收口处。
 *
 * 两面墙（TMDB 墙、番号墙）都要接播放，而它们的卡片类型与入参完全不同
 * （一个给 `rel_dir + strm_name`，另一个给 `rel_dir + stem`）。差别只在**那一行
 * 请求**上，拿到 `PlayableFile[]` 之后：拼播放窗、起播、字幕、报错 —— 全都一样。
 *
 * 所以这里只做两件事：按墙的类型取数据 + 统一报错。窗体交给 WallPlayer。
 *
 * **请求只发生在点击时**。别把这段挪进卡片渲染 —— 一面墙几十上百张卡，
 * 每张读一次 `.strm`（番号那套还会列一次目录），既拖慢列表也白读盘。
 */
export function useWallPlayback() {
  const open = ref(false);
  const title = ref("");
  const items = ref<PlayableFile[]>([]);
  /** 正在取地址的卡片 id，用来在按钮上转圈。 */
  const loadingId = ref<string | null>(null);

  function close() {
    open.value = false;
    items.value = [];
    title.value = "";
  }

  /** 取到可播文件就开窗；一个都没有时**说清楚原因**，不要静默无反应。 */
  function present(label: string, list: PlayableFile[]) {
    if (!list.length) {
      // 两种可能：目录里压根没有 `.strm`，或者有但正文不是本应用的播放地址
      // （被手改过、指向别的播放器）。它们对用户的处置是一样的，都如实说。
      toast.info(`《${label}》这个目录下没有找到可播放的 .strm 文件`);
      return;
    }
    title.value = label;
    items.value = list;
    open.value = true;
  }

  /** TMDB 影片墙：`strm_name` 为空表示多集作品，后端会列整个目录。 */
  async function playTMDB(taskId: number, item: StrmScrapeItem) {
    if (!taskId) return;
    loadingId.value = item.id;
    try {
      const res = await fetchStrmScrapeItemPlayable({
        strm_task_id: taskId,
        rel_dir: item.rel_dir,
        strm_name: item.strm_name,
      });
      present(item.title, res.items ?? []);
    } catch (error) {
      toast.error(errorMessageOf(error));
    } finally {
      loadingId.value = null;
    }
  }

  /** 番号影片墙：stem 走后端那套三层闸门。 */
  async function playJav(
    taskId: number,
    item: { id: string; rel_dir: string; stem: string; number?: string; title?: string },
  ) {
    if (!taskId) return;
    loadingId.value = item.id;
    try {
      const res = await fetchJavWallItemPlayable({
        strm_task_id: taskId,
        rel_dir: item.rel_dir,
        stem: item.stem,
      });
      present(item.number || item.title || item.stem, res.items ?? []);
    } catch (error) {
      toast.error(errorMessageOf(error));
    } finally {
      loadingId.value = null;
    }
  }

  return { open, title, items, loadingId, close, playTMDB, playJav };
}

/** 把接口报错落成一句人话。抽出来是为了两条路给同一种口径。 */
function errorMessageOf(error: unknown): string {
  const fallback = "读取播放地址失败";
  const message = error instanceof Error && error.message ? error.message : fallback;
  // 常见的一种：`.strm` 里的 host 是旧的（后台 base URL 改过而 STRM 没重生成）。
  // 那种必须说出来，否则用户只看到「播放失败」，会以为是片子坏了。
  return `${message}（若是刚改过访问地址，可重跑一次 STRM 任务刷新 .strm 正文）`;
}

