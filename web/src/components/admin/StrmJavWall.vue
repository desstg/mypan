<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchJavWallItems,
  rebuildJavWallItem,
  refreshJavWall,
  type JavWallItem,
  type JavWallListResult,
} from "@/api/strmJavWall";
import AppButton from "@/components/base/AppButton.vue";
import StrmJavMetaDrawer from "@/components/admin/StrmJavMetaDrawer.vue";
import { toast } from "@/composables/useToast";
import "@/styles/jav.css";

// 番号影片的海报墙（辅助工具 → 海报墙，任务媒体类型 = 番号影片时由 StrmScrapePanel 挂上来）。
//
// 与 TMDB 那面墙的区别都来自用户的要求：
//   * 「类型」不是电影/剧集，而是**磁盘上实际存在的一级目录**（有码/无码/欧美/国产…）；
//     哪些目录整档不显示由用户自己勾（页头「全部目录」按钮 / STRM 设置里那一行），
//     「未匹配」默认已勾 —— 它取的是分类规则里那条兜底规则的目标目录，改名会自动跟上。
//   * 多分片目录（`91CM-109-cd1/cd2/cd3`）**每个分片一张卡**，三张都显示 cd1 的图。
//   * 缩略图 / 海报图 两种视图切换（纯前端，两个 URL 都在数据里）。
//   * 鼠标指过去出「编辑」「重刮」。

// 搜索与排序由**页头**统一控制（用户要求：番号墙自己的那两个框不要了）。
// 子组件只按这三个 prop 请求 —— 页头那套按钮对两种任务长得一模一样，功能各是各的。
const props = defineProps<{
  taskId: number | null;
  keyword?: string;
  sort?: string;
}>();

/** 隐藏名单变了（或拿到新的一份）时把名单抛给外面：页头那颗按钮要显示「隐藏 N 个目录」。 */
const emit = defineEmits<{
  "hidden-dirs": [string[]];
  /** 墙上那句「xx 已隐藏」旁边的「调整」：请父组件打开勾选弹窗。 */
  "open-hidden-dirs": [];
}>();

const loading = ref(false);
const refreshing = ref(false);
const result = ref<JavWallListResult | null>(null);
const category = ref<string>("");
const view = ref<"thumb" | "poster">("poster");
const busyStem = ref<string>("");

const drawerOpen = ref(false);
const editing = ref<JavWallItem | null>(null);

const items = computed(() => result.value?.items ?? []);
const categories = computed(() => result.value?.categories ?? []);

/** 把隐藏名单抛给外面（页头那颗「全部目录」按钮要显示「隐藏 N 个目录」）。 */
function publishHiddenDirs() {
  emit("hidden-dirs", result.value?.hidden_dirs ?? []);
}

async function load(options: { silent?: boolean } = {}) {
  if (!props.taskId) {
    result.value = null;
    publishHiddenDirs();
    return;
  }
  if (!options.silent) loading.value = true;
  try {
    result.value = await fetchJavWallItems(props.taskId, {
      category: category.value,
      keyword: props.keyword ?? "",
      sort: props.sort ?? "number_asc",
      limit: 200,
    });
    publishHiddenDirs();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "读取番号海报墙失败"));
    result.value = null;
    publishHiddenDirs();
  } finally {
    loading.value = false;
  }
}

/** 「刷新元数据」= 重读本地磁盘并重列（不是重新生成；重新生成是卡片上的「重刮」）。 */
async function refreshMeta() {
  if (!props.taskId) return;
  refreshing.value = true;
  try {
    result.value = await refreshJavWall(props.taskId, {
      category: category.value,
      keyword: props.keyword ?? "",
      sort: props.sort ?? "number_asc",
      limit: 200,
    });
    publishHiddenDirs();
    toast.success("已重新读取本地文件");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "刷新失败"));
  } finally {
    refreshing.value = false;
  }
}

function openEditor(item: JavWallItem) {
  editing.value = item;
  drawerOpen.value = true;
}

/** 保存之后只换这一张卡（不整表重列，免得滚动位置乱跳）。 */
function replaceItem(updated: JavWallItem) {
  if (!result.value) return;
  const idx = result.value.items.findIndex((it) => it.id === updated.id);
  if (idx >= 0) result.value.items[idx] = updated;
}

async function rebuild(item: JavWallItem) {
  if (!props.taskId) return;
  busyStem.value = item.stem;
  try {
    const detail = await rebuildJavWallItem(props.taskId, item.rel_dir, item.stem);
    replaceItem(detail.item);
    toast.success("已重刮：" + item.number);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "重刮失败"));
  } finally {
    busyStem.value = "";
  }
}

function subtitle(item: JavWallItem): string {
  const bits: string[] = [item.category || "根目录"];
  if (!item.has_nfo) bits.push("缺 nfo");
  if (!item.has_sidecar) bits.push("无侧车");
  return bits.join(" · ");
}

watch(() => props.taskId, () => {
  category.value = "";
  void load();
});
watch([category, () => props.sort], () => void load());
// 关键词来自页头那个输入框：那边是 v-model 直连，这里跟着 prop 变就重列（防抖）。
let keywordTimer: number | undefined;
watch(() => props.keyword, () => {
  window.clearTimeout(keywordTimer);
  keywordTimer = window.setTimeout(() => void load(), 300);
});

onMounted(() => void load());
onUnmounted(() => window.clearTimeout(keywordTimer));

defineExpose({ refreshMeta, load });
</script>

<template>
  <div class="jav-wall">
    <!-- 全量扫描的常驻警告：那种模式每轮都会用侧车重建 nfo/海报，手改的会被覆盖 -->
    <p v-if="result?.full_sync_wipe" class="jav-wall__banner">
      ⚠ 本任务是「全量」扫描模式：每次扫描都会用侧车 JSON 重建 nfo 与海报，手工编辑会被覆盖。
      想保留编辑请把扫描方式改成「更新」或「补缺」；想还原自动生成的版本，就跑一次全量扫描。
    </p>

    <div class="jav-wall__bar">
      <div class="jav-wall__tabs">
        <button
          type="button"
          class="jav-tab"
          :class="{ 'jav-tab--active': category === '' }"
          @click="category = ''"
        >
          全部
        </button>
        <button
          v-for="cat in categories"
          :key="cat.name"
          type="button"
          class="jav-tab"
          :class="{ 'jav-tab--active': category === cat.name }"
          @click="category = cat.name"
        >
          {{ cat.name || "根目录" }}<span class="jav-tab__count">{{ cat.count }}</span>
        </button>
        <span v-if="result?.hidden_dirs?.length" class="jav-wall__hidden">
          {{ result.hidden_dirs.join("/") }} 已隐藏
          <button type="button" class="jav-wall__hidden-link" @click="emit('open-hidden-dirs')">
            调整
          </button>
        </span>
      </div>

      <!-- 搜索与排序都挪到页头那两个按钮上了（与 tmdb 那套外观一致、功能各是各的），
           这里只留视图切换。 -->
      <div class="jav-wall__tools">
        <div class="jav-wall__view" role="group" aria-label="视图切换">
          <button
            type="button"
            class="jav-wall__view-btn"
            :class="{ 'jav-wall__view-btn--active': view === 'thumb' }"
            title="缩略图（封面原图，横版）"
            @click="view = 'thumb'"
          >
            缩略图
          </button>
          <button
            type="button"
            class="jav-wall__view-btn"
            :class="{ 'jav-wall__view-btn--active': view === 'poster' }"
            title="海报图（2:3 竖版）"
            @click="view = 'poster'"
          >
            海报图
          </button>
        </div>
      </div>
    </div>

    <div v-if="loading" class="jav-wall__state">加载中…</div>
    <div v-else-if="!items.length" class="jav-wall__state">
      这个任务的输出目录里还没有番号影片。先在「STRM 任务」里同步一次，或确认任务的媒体类型与输出目录。
    </div>

    <div v-else class="jav-wall__grid" :class="`jav-wall__grid--${view}`">
      <article v-for="item in items" :key="item.id" class="jav-card jav-card--wall">
        <div class="jav-card__cover" :class="{ 'jav-card__cover--poster': view === 'poster' }">
          <img v-if="view === 'poster' && item.poster_url" :src="item.poster_url" :alt="item.number" loading="lazy" />
          <img v-else-if="view === 'thumb' && item.thumb_url" :src="item.thumb_url" :alt="item.number" loading="lazy" />
          <div v-else class="jav-card__placeholder">无图</div>

          <div class="jav-card__hover">
            <AppButton type="button" size="sm" variant="secondary" @click="openEditor(item)">编辑</AppButton>
            <AppButton
              type="button"
              size="sm"
              variant="primary"
              :disabled="busyStem === item.stem"
              title="用本地 json 重建这一部的 nfo，并重新下载封面与海报（剧照只补缺的）"
              @click="rebuild(item)"
            >
              {{ busyStem === item.stem ? "重刮中…" : "重刮" }}
            </AppButton>
          </div>
          <div v-if="busyStem === item.stem" class="jav-wall__busy" />
        </div>
        <div class="jav-wall__meta">
          <div class="jav-wall__number" :title="item.number">{{ item.number }}</div>
          <div class="jav-wall__title" :title="item.title">{{ item.title }}</div>
          <div class="jav-wall__sub">{{ subtitle(item) }}</div>
        </div>
      </article>
    </div>

    <StrmJavMetaDrawer
      :open="drawerOpen"
      :task-id="props.taskId"
      :item="editing"
      :full-sync-wipe="result?.full_sync_wipe ?? false"
      @close="drawerOpen = false"
      @changed="replaceItem"
    />
  </div>
</template>

<style scoped>
.jav-wall { display: flex; flex-direction: column; gap: 12px; }
.jav-wall__banner { margin: 0; padding: 10px 12px; border-radius: var(--radius-sm); background: rgba(180, 83, 9, 0.08); color: #b45309; font-size: 12px; line-height: 1.6; }
.jav-wall__bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.jav-wall__tabs { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.jav-tab__count { margin-left: 6px; font-size: 11px; opacity: 0.7; }
.jav-wall__hidden { font-size: 12px; color: var(--text-muted); }
.jav-wall__hidden-link {
  margin-left: 6px; padding: 0; border: 0; background: none;
  color: var(--brand); font-size: 12px; cursor: pointer; text-decoration: underline;
}
.jav-wall__tools { display: flex; align-items: center; gap: 8px; }
.jav-wall__view { display: inline-flex; border: 1px solid var(--border); border-radius: var(--radius-pill); overflow: hidden; }
.jav-wall__view-btn { padding: 5px 12px; font-size: 12px; background: transparent; border: 0; color: var(--text-muted); cursor: pointer; }
.jav-wall__view-btn--active { background: var(--brand); color: #fff; }
.jav-wall__state { padding: 40px; text-align: center; color: var(--text-muted); }
/* 两套网格，按视图切：
   - 缩略图（横版）：5 列固定，保持原来的大小 —— 横图铺满宽度后本来就矮，再缩小就看不清了；
   - 海报图（竖版）：**与「STRM 刮削」那面墙同尺寸**（auto-fill 140px + 14px 间距）。
     两边都是 2:3 的海报，尺寸一致才像同一套界面；原来固定 5 列在宽屏上一张有 260px 宽，
     比刮削墙大了一大截。 */
.jav-wall__grid--thumb { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 16px; }
@media (max-width: 1280px) { .jav-wall__grid--thumb { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
@media (max-width: 900px) { .jav-wall__grid--thumb { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 640px) { .jav-wall__grid--thumb { grid-template-columns: repeat(2, minmax(0, 1fr)); } }

.jav-wall__grid--poster { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 14px; }

/* 海报图视图用 2:3，缩略图视图保持 .jav-card__cover 原本的 3:2 */
.jav-card__cover--poster { aspect-ratio: 2 / 3; }
.jav-wall__meta { padding: 8px 2px 0; display: flex; flex-direction: column; gap: 2px; }
.jav-wall__number { font-weight: 600; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jav-wall__title { font-size: 12px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jav-wall__sub { font-size: 11px; color: var(--text-muted); }

/* 悬停显形（照 CoverExtractToolCard 的 .cand-rm 那一套）：触屏上常显，否则永远点不到 */
.jav-card__hover { position: absolute; inset: auto 6px 6px 6px; display: flex; gap: 6px; justify-content: center; opacity: 0; transition: opacity 0.12s; z-index: 2; }
.jav-card:hover .jav-card__hover,
.jav-card:focus-within .jav-card__hover { opacity: 1; }
@media (hover: none) { .jav-card__hover { opacity: 1; } }
.jav-wall__busy { position: absolute; inset: 0; background: rgba(15, 23, 42, 0.25); }
</style>
