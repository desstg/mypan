<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchJavWallItem,
  rebuildJavWallItem,
  saveJavWallMeta,
  saveJavWallPoster,
  type JavCropRect,
  type JavMovieMeta,
  type JavWallItem,
  type JavWallItemDetail,
} from "@/api/strmJavWall";
import AdminSettingsDrawer from "@/components/admin/AdminSettingsDrawer.vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import JavStringListEditor from "@/components/admin/JavStringListEditor.vue";
import "@/styles/jav-editor.css";
import { nativeToDisplay } from "@/utils/posterCrop";
import { toast } from "@/composables/useToast";

// 番号影片的元数据编辑器：上半截裁海报、下半截改字段。
//
// 两条硬规则（都是用户定的）：
//   ① **只写本地文件**：保存元数据 = 重写 `<主干>.nfo`；保存海报 = 覆盖 `poster.jpg`。
//      **json 一律不写**（本地与网盘都不动）。
//   ② **想恢复自动生成的那一版，就跑一次「全量」扫描** —— 所以这里不做"撤销"。

const props = defineProps<{
  open: boolean;
  taskId: number | null;
  item: JavWallItem | null;
  /** 任务扫描方式是「全量」时提示一句：下次扫描会覆盖手改的内容。 */
  fullSyncWipe?: boolean;
}>();

const emit = defineEmits<{ close: []; changed: [item: JavWallItem] }>();

// 状态机：**不能再出现"空白页"** —— 之前加载失败/提前返回时内容区什么都不渲染，
// 用户只看到底下两个按钮，完全不知道发生了什么。
const state = ref<"idle" | "loading" | "ready" | "error">("idle");
const loadError = ref("");
const loading = computed(() => state.value === "loading");
const savingMeta = ref(false);
const savingPoster = ref(false);
const rebuilding = ref(false);
const detail = ref<JavWallItemDetail | null>(null);
const form = reactive<JavMovieMeta>(emptyMeta());

// 裁剪区：屏幕上的框（像素，相对显示宽度）与原始像素双份，换算见 posterCrop.ts
const stageEl = ref<HTMLElement | null>(null);
const displayWidth = ref(0);
const rect = ref<JavCropRect>({ x: 0, y: 0, w: 0, h: 0 });

// 「添加水印」的勾选：一个复选框（字幕）+ 三组单选（每组「无」= 不贴）。
//
// 数据结构刻意与后端那个 preset 数组一致（键就是图标名），这样"自动预置"就是
// 直接赋值、不用翻译一层。8K 与「流出」**推不出来**（见后端 watermark.go 的说明），
// 所以它们永远只能手选、不会被预置。
const wmSub = ref(false);
const wmCensored = ref<"censored" | "uncensored" | "leak" | "">("");
const wmUncensor = ref<"umr" | "">("");
const wmRes = ref<"4k" | "8k" | "">("");

/**
 * 勾选结果 → 要贴的水印 id 列表。
 *
 * **「无码」与「流出」都贴 leak.png**（用户定的：只有那一张图，它写的就是"无码流出"）。
 * 两个选项分开只是让用户能标出"这是流出片"这个事实，贴出来是一样的。
 */
function watermarkIDs(): string[] {
  const ids: string[] = [];
  if (wmSub.value) ids.push("sub");
  if (wmCensored.value === "uncensored" || wmCensored.value === "leak") ids.push("leak");
  if (wmUncensor.value === "umr") ids.push("umr");
  if (wmRes.value) ids.push(wmRes.value);
  return ids;
}

/** 后端给的预置 → 三个控件。 */
function applyWatermarkPreset(preset: string[] | undefined) {
  const list = preset ?? [];
  wmSub.value = list.includes("sub");
  wmUncensor.value = list.includes("umr") ? "umr" : "";
  wmRes.value = list.includes("8k") ? "8k" : list.includes("4k") ? "4k" : "";
  // 预置落到「无码」而不是「流出」：模型里分不出"流出"（那与破解是同一个标志位），
  // 把一个普通无码片标成"流出"是编造事实。两个选项贴出来的图是一样的。
  wmCensored.value = list.includes("leak") ? "uncensored" : "censored";
}
const dragState = ref<{ startX: number; startY: number; start: JavCropRect } | null>(null);

function emptyMeta(): JavMovieMeta {
  return {
    number: "", number_letter: "", title: "", origin_title: "", summary: "",
    actors: [], director: "", tags: [], series: "", maker: "", publisher: "", label: "",
    release_date: "", duration: 0, score: 0, score_max: 5, votes: 0, rating_name: "javdb",
    javdb_url: "", cover_url: "", trailer_url: "", added_at: "",
    four_k: false, uncensored: false, has_subtitle: false,
    lock_data: false, custom_rating: "JP-18+", mpaa: "JP-18+", country_code: "JP",
  };
}

/**
 * 编辑器里那张缩略图。
 *
 * **把 URL 上的 `w` 换成 0**：服务端那个参数是「按卡片宽度缩放」用的，而这里要的是
 * **原始像素** —— 裁剪框是按 thumb_width/thumb_height 算的，喂一张缩过的图会让
 * 框的比例与实际存出来的结果对不上（用户挪了半天，存出来是另一个位置）。
 *
 * 换 w 而不是让后端别加：URL 是后端拼的（`javWallImageURL`），两处各拼一份迟早走岔；
 * 这里只是把那个参数改成「不缩放」，语义仍然由后端定义。
 */
const thumbSrc = computed(() => {
  const url = props.item?.thumb_url ?? "";
  return url.replace(/([?&])w=\d+/, "$10");
});
const canSavePoster = computed(() => (detail.value?.poster.has_thumb ?? false));
// 屏幕上的框 = 原始像素 × 显示缩放
const displayRect = computed(() => {
  if (!detail.value) return { x: 0, y: 0, w: 0, h: 0 };
  return nativeToDisplay(rect.value, displayWidth.value, detail.value.poster.thumb_width);
});

async function load() {
  if (!props.open) {
    state.value = "idle";
    return;
  }
  if (!props.taskId || !props.item) {
    // 抽屉开了但没有指向某一部（正常流程不会发生，但别让界面空着）
    console.error("[jav-wall] 打开编辑器时缺少参数", { taskId: props.taskId, item: props.item });
    state.value = "error";
    loadError.value = "没有选中影片（刷新一下列表再点「编辑」）";
    return;
  }
  state.value = "loading";
  loadError.value = "";
  detail.value = null;
  try {
    const data = await fetchJavWallItem(props.taskId, props.item.rel_dir, props.item.stem);
    detail.value = data;
    // `?? []` 不是多余的：服务端（或更早的版本）可能在 actors/tags 上给 null，
    // 而 JavStringListEditor 读 `modelValue.length` —— null 会让整个抽屉渲染失败。
    const meta = data.meta ?? ({} as JavMovieMeta);
    Object.assign(form, emptyMeta(), meta, {
      actors: meta.actors ?? [],
      tags: meta.tags ?? [],
    });
    applyWatermarkPreset(data.watermark?.preset);
    rect.value = data.poster?.rect ?? { x: 0, y: 0, w: 0, h: 0 };
    state.value = "ready";
    // 等图片挂上去再量宽度（量不到就靠图片自身的 load 事件再量一次）
    requestAnimationFrame(measure);
  } catch (error) {
    // **把原因显示在抽屉里**（也打一份到控制台）：只弹个 toast 的话，
    // 用户看到的就是一个空白页 + 一枚一闪而过的提示，没法反馈。
    loadError.value = getApiErrorMessage(error, "读取这一部的元数据失败");
    console.error("[jav-wall] 读取单品失败", error);
    state.value = "error";
  }
}

function measure() {
  if (!stageEl.value) return;
  displayWidth.value = stageEl.value.clientWidth;
}

watch(() => [props.open, props.item?.stem, props.taskId], load, { immediate: true });

// —— 裁剪交互：指针捕获拖拽（照 CoverExtractToolCard 的姿势）——
//
// **只左右拖动，不改框的大小**：框的尺寸就是当前 poster 的尺寸（生成器裁的是 0.70
// 的宽高比，不是 2:3）。允许缩放的话，用户随手一拖就会把海报比例悄悄改掉 ——
// 而那张图在 Emby 那边的外框比例是固定的，改了只会多出留白。
function startDrag(event: PointerEvent) {
  if (!canSavePoster.value) return;
  const el = event.currentTarget as HTMLElement;
  dragState.value = { startX: event.clientX, startY: event.clientY, start: { ...rect.value } };
  el.setPointerCapture(event.pointerId);
  event.preventDefault();
}

function moveDrag(event: PointerEvent) {
  const st = dragState.value;
  if (!st || !detail.value) return;
  const scale = displayWidth.value > 0 ? detail.value.poster.thumb_width / displayWidth.value : 1;
  const dx = (event.clientX - st.startX) * scale;
  const dy = (event.clientY - st.startY) * scale;
  const W = detail.value.poster.thumb_width;
  const H = detail.value.poster.thumb_height;
  // 横图（比 2:3 宽）高度吃满、只横向滑；竖图宽度吃满、只纵向滑 ——
  // 与生成器 PosterCropWindow 的两种取窗方式同形。
  const horizontal = W / H > 2 / 3;
  const x = horizontal ? Math.max(0, Math.min(st.start.x + dx, W - st.start.w)) : st.start.x;
  const y = horizontal ? st.start.y : Math.max(0, Math.min(st.start.y + dy, H - st.start.h));
  rect.value = { ...st.start, x: Math.round(x), y: Math.round(y) };
  event.preventDefault();
}

function finishDrag(event: PointerEvent) {
  const el = event.currentTarget as HTMLElement;
  if (el.hasPointerCapture(event.pointerId)) el.releasePointerCapture(event.pointerId);
  dragState.value = null;
}

function resetRect() {
  if (!detail.value) return;
  rect.value = { ...detail.value.poster.rect };
}

async function saveMeta() {
  if (!props.taskId || !props.item) return;
  savingMeta.value = true;
  try {
    const data = await saveJavWallMeta(props.taskId, props.item.rel_dir, props.item.stem, { ...form });
    detail.value = data;
    Object.assign(form, emptyMeta(), data.meta);
    toast.success("元数据已保存（已重写 " + data.names.nfo + "）");
    emit("changed", data.item);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存失败"));
  } finally {
    savingMeta.value = false;
  }
}

async function savePoster() {
  if (!props.taskId || !props.item || !detail.value) return;
  savingPoster.value = true;
  try {
    // ⚠️ **不要再换算一次**：`rect.value` 在拖动时就已经是原生像素了
    // （moveDrag 里把鼠标位移按 native/display 换算过了）。这里再调一次
    // displayToNative 会把窗口放大（800/700 这种比例），顶到边界后被夹成"整张图" ——
    // 存出来的 poster 与 thumb 一模一样，用户实测撞见过。
    const native = { ...rect.value };
    const data = await saveJavWallPoster(
      props.taskId,
      props.item.rel_dir,
      props.item.stem,
      native,
      watermarkIDs(),
    );
    detail.value = data;
    rect.value = data.poster.rect;
    toast.success("海报已保存（已覆盖 " + data.names.poster + "）");
    emit("changed", data.item);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存海报失败"));
  } finally {
    savingPoster.value = false;
  }
}

async function rebuild() {
  if (!props.taskId || !props.item) return;
  rebuilding.value = true;
  try {
    const data = await rebuildJavWallItem(props.taskId, props.item.rel_dir, props.item.stem);
    detail.value = data;
    Object.assign(form, emptyMeta(), data.meta);
    rect.value = data.poster.rect;
    toast.success("已重刮：nfo 重写、封面与海报重新下载（剧照只补缺）");
    emit("changed", data.item);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "重刮失败"));
  } finally {
    rebuilding.value = false;
  }
}
</script>

<template>
  <AdminSettingsDrawer :open="open" title="编辑元数据" @close="emit('close')">
    <div v-if="state === 'idle' || loading" class="jav-edit__loading">加载中…</div>
    <div v-else-if="state === 'error'" class="jav-edit__error">
      <p>没能读到这一部的数据。</p>
      <p class="jav-edit__error-detail">{{ loadError }}</p>
      <AppButton type="button" variant="secondary" size="sm" @click="load">重试</AppButton>
    </div>
    <div v-else-if="detail" class="jav-edit">
      <p v-if="detail.notice" class="jav-edit__notice">{{ detail.notice }}</p>
      <p v-if="fullSyncWipe" class="jav-edit__notice jav-edit__notice--warn">
        本任务为「全量」扫描模式：下次扫描会按侧车 JSON 重建 nfo 与海报、并重新下载封面，这次编辑会被覆盖。
      </p>

      <!-- 左栏裁海报、右栏表单 —— 排版照 XL_center 的 nfo 编辑器那套（两栏 + 分组标题）。 -->
      <div class="jav-edit__body">
        <!-- ① 海报：thumb + 选框，框外压暗模糊 -->
        <section class="jav-edit__left">
          <div class="jav-edit__label">海报预览</div>
          <div v-if="!detail.poster.has_thumb" class="jav-edit__empty">这一部没有 thumb 图，无法裁剪（先「重刮」一次）</div>
          <div v-else ref="stageEl" class="jav-crop" @pointerdown.self="startDrag($event)">
            <img :src="thumbSrc" alt="缩略图" draggable="false" @load="measure" />
            <!-- 框外四块：压暗 + 模糊（不用 clip-path，兼容性更稳） -->
            <div class="jav-crop__dim" :style="{ left: 0, top: 0, width: '100%', height: displayRect.y + 'px' }" />
            <div class="jav-crop__dim" :style="{ left: 0, top: displayRect.y + 'px', width: displayRect.x + 'px', height: displayRect.h + 'px' }" />
            <div
              class="jav-crop__dim"
              :style="{ left: (displayRect.x + displayRect.w) + 'px', top: displayRect.y + 'px', right: 0, height: displayRect.h + 'px' }"
            />
            <div
              class="jav-crop__dim"
              :style="{ left: 0, top: (displayRect.y + displayRect.h) + 'px', right: 0, bottom: 0 }"
            />
            <div
              class="jav-crop__box"
              :style="{ left: displayRect.x + 'px', top: displayRect.y + 'px', width: displayRect.w + 'px', height: displayRect.h + 'px' }"
              @pointerdown.stop="startDrag($event)"
              @pointermove="moveDrag"
              @pointerup="finishDrag"
              @pointercancel="finishDrag"
            >
              <span class="jav-crop__handle">⇔</span>
            </div>
          </div>
          <div class="jav-edit__hint">
            框 {{ rect.w }}×{{ rect.h }}
            <template v-if="detail.poster.rect_source === 'default'">
              （这张海报是很早以前裁的、与现在的封面尺寸对不上，认不出原来框在哪；
              已经先放在默认位置，拖到合适的地方点「裁剪」即可 —— 裁过一次之后就对得上了）
            </template>
          </div>
          <div class="jav-edit__actions">
            <AppButton type="button" variant="secondary" size="sm" :disabled="!canSavePoster" @click="resetRect">
              还原当前位置
            </AppButton>
            <AppButton
              type="button"
              variant="primary"
              size="sm"
              title="把框内的部分存成 poster.jpg，覆盖现在这张"
              :disabled="savingPoster || !canSavePoster"
              @click="savePoster"
            >
              {{ savingPoster ? "裁剪中…" : "裁剪" }}
            </AppButton>
          </div>
          <div class="jav-edit__hint">在框上按住鼠标左右拖动，选好位置后点「裁剪」</div>

          <!-- 添加水印：勾上的项会贴到**截出来的**那张 poster 上（位置固定，见后端
               watermark.go）。默认按影片属性预置，用户可以改。 -->
          <div class="jav-wm">
            <div class="jav-wm__title">添加水印</div>
            <label class="jav-wm__check">
              <input v-model="wmSub" type="checkbox" />
              字幕
            </label>
            <div class="jav-wm__row">
              <label><input v-model="wmCensored" type="radio" value="censored" /> 有码</label>
              <label><input v-model="wmCensored" type="radio" value="uncensored" /> 无码</label>
              <label><input v-model="wmCensored" type="radio" value="leak" /> 流出</label>
            </div>
            <div class="jav-wm__row">
              <label><input v-model="wmUncensor" type="radio" value="umr" /> 破解</label>
              <label><input v-model="wmUncensor" type="radio" value="" /> 无</label>
            </div>
            <div class="jav-wm__row">
              <label><input v-model="wmRes" type="radio" value="4k" /> 4K</label>
              <label><input v-model="wmRes" type="radio" value="8k" /> 8K</label>
              <label><input v-model="wmRes" type="radio" value="" /> 无</label>
            </div>
            <div class="jav-edit__hint">
              8K 与「流出」推不出来（上游把 8k 归进 4K 那一档，流出与破解是同一个标记），
              这两个只能手选。
            </div>
          </div>
        </section>

        <!-- ② 右栏：字段 -->
        <section class="jav-edit__right">
          <div class="jav-edit__grid">
            <div class="jav-edit__field jav-edit__field--full">
              <label class="jav-edit__label">标题</label>
              <AppInput v-model="form.title" placeholder="影片标题" />
              <p v-if="form.origin_title" class="jav-edit__hint">
                这里就是 nfo 的 <code>&lt;title&gt;</code>；原名在下面那栏，两栏各有各的。
              </p>
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">番号</label>
              <AppInput :model-value="detail.meta.number" disabled placeholder="如 ABC-123" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">原名</label>
              <AppInput v-model="form.origin_title" placeholder="Original Title" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">发行日期</label>
              <AppInput v-model="form.release_date" type="date" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">时长（分钟）</label>
              <AppInput :model-value="String(form.duration)" type="number" @update:model-value="form.duration = Number($event) || 0" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">评分（5 分制）</label>
              <AppInput :model-value="String(form.score)" type="number" @update:model-value="form.score = Number($event) || 0" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">导演</label>
              <AppInput v-model="form.director" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">片商</label>
              <AppInput v-model="form.maker" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">发行</label>
              <AppInput v-model="form.publisher" />
            </div>
            <div class="jav-edit__field">
              <label class="jav-edit__label">系列</label>
              <AppInput v-model="form.series" />
            </div>
          </div>

          <div class="jav-edit__section-title">演员</div>
          <JavStringListEditor v-model="form.actors" placeholder="输入演员名后回车" empty-hint="无" />

          <div class="jav-edit__section-title">标签</div>
          <JavStringListEditor v-model="form.tags" placeholder="输入标签后回车" empty-hint="无" />

          <div class="jav-edit__section-title">剧情简介</div>
          <textarea v-model="form.summary" class="jav-edit__textarea" rows="5" placeholder="影片剧情简介…" />
        </section>
      </div>
    </div>

    <template #foot>
      <AppButton type="button" variant="ghost" :disabled="rebuilding" @click="rebuild">
        {{ rebuilding ? "重刮中…" : "重刮（用本地 json 重建）" }}
      </AppButton>
      <span class="jav-edit__spacer" />
      <AppButton type="button" variant="secondary" @click="emit('close')">关闭</AppButton>
      <AppButton type="button" variant="primary" :disabled="savingMeta" @click="saveMeta">
        {{ savingMeta ? "保存中…" : "保存元数据" }}
      </AppButton>
    </template>
  </AdminSettingsDrawer>
</template>

<style scoped>
/* 排版照 E:\claude code\XL_center 的 nfo 编辑器：**左栏裁图、右栏表单**，
   右栏里用「分类信息」那种小标题把字段分组。功能与样式仍是我们自己的
   （AppInput / AppButton / JavStringListEditor 那套），只复刻外观与布局。 */
.jav-edit { display: flex; flex-direction: column; gap: 14px; }

.jav-edit__body {
  display: grid;
  /* 左右**各一半**（用户要求）：截图区与元数据同宽，视觉上是一张表的两栏。 */
  grid-template-columns: 1fr 1fr;
  gap: 20px;
  align-items: start;
}
/* 窄屏堆成一栏（与源码的 768px 断点一致） */
@media (max-width: 900px) {
  .jav-edit__body { grid-template-columns: 1fr; }
}
.jav-edit__left { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.jav-edit__right { display: flex; flex-direction: column; gap: 10px; min-width: 0; }

.jav-edit__loading { padding: 24px; color: var(--text-muted); }
.jav-edit__error { display: flex; flex-direction: column; gap: 10px; align-items: flex-start; padding: 24px 4px; color: var(--text); }
.jav-edit__error-detail { margin: 0; font-size: 12px; color: #b91c1c; word-break: break-all; }
.jav-edit__notice { margin: 0; padding: 8px 10px; border-radius: var(--radius-sm); background: var(--surface-sunken); font-size: 12px; color: var(--text-muted); }
.jav-edit__notice--warn { color: #b45309; background: rgba(180, 83, 9, 0.08); }

.jav-edit__label { font-size: 12px; color: var(--text-muted); margin-bottom: 4px; }
/* 字段底下的一句说明（比 label 更轻，不抢眼）。 */
.jav-edit__hint { margin: 6px 0 0; font-size: 11.5px; color: var(--text-muted); line-height: 1.6; }
.jav-edit__hint code { font-size: 11px; }
.jav-edit__field { display: flex; flex-direction: column; min-width: 0; }
.jav-edit__field--full { grid-column: 1 / -1; }
.jav-edit__grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px 14px; }
/* 分组小标题：上边一条分隔线，与源码的 .form-section-title 同形 */
.jav-edit__section-title {
  margin-top: 6px;
  padding-top: 10px;
  border-top: 1px solid var(--border);
  font-size: 13px;
  font-weight: 600;
}

.jav-edit__hint { font-size: 12px; font-weight: 400; color: var(--text-muted); line-height: 1.6; }
.jav-edit__marks { display: flex; gap: 16px; font-size: 13px; }
.jav-edit__marks label { display: inline-flex; gap: 6px; align-items: center; }
.jav-edit__textarea {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  line-height: 1.6;
  resize: vertical;
}
.jav-edit__actions { display: flex; gap: 8px; justify-content: flex-end; }
.jav-edit__empty { padding: 12px; color: var(--text-muted); font-size: 13px; background: var(--surface-sunken); border-radius: var(--radius-sm); }
.jav-edit__spacer { flex: 1; }

/* 添加水印（照 34.png）：标题 + 一个复选框 + 三组单选，组间一条细横线 */
.jav-wm {
  margin-top: 4px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.jav-wm__title { font-size: 13px; font-weight: 600; }
.jav-wm__check,
.jav-wm__row label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  cursor: pointer;
}
.jav-wm__check input,
.jav-wm__row input { width: 15px; height: 15px; accent-color: var(--brand); cursor: pointer; }
/* .jav-wm__check 单独一行；三组单选各自一行、等距铺开 */
.jav-wm__check { padding: 6px 0; border-bottom: 1px solid var(--border); }
.jav-wm__row {
  display: flex;
  gap: 18px;
  padding: 6px 0;
  border-bottom: 1px solid var(--border);
}
.jav-wm__row:last-of-type { border-bottom: 0; }

/* 裁剪区：thumb 铺满，框外压暗模糊，框中间一个拖动提示 */
.jav-crop { position: relative; user-select: none; touch-action: none; border-radius: var(--radius-sm); overflow: hidden; background: #000; line-height: 0; }
.jav-crop img { display: block; width: 100%; height: auto; }
.jav-crop__dim { position: absolute; background: rgba(15, 23, 42, 0.35); backdrop-filter: blur(10px); -webkit-backdrop-filter: blur(10px); pointer-events: none; }
.jav-crop__box { position: absolute; border: 2px solid var(--brand); box-sizing: border-box; cursor: grab; }
.jav-crop__box:active { cursor: grabbing; }
/* 框中央那个圆钮：纯视觉的「可以拖」提示（照源码的 .crop-handle） */
.jav-crop__handle {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--brand);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  line-height: 1;
  pointer-events: none;
}
</style>
