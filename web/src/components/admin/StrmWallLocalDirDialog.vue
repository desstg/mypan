<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  createWallLocalDir,
  deleteWallLocalDir,
  fetchWallLocalDirs,
  type WallLocalDir,
} from "@/api/strmScrape";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import LocalDirBrowserModal from "@/components/common/LocalDirBrowserModal.vue";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";

/**
 * 「海报墙 · 本地目录」弹窗：把一批**不在任何 STRM 任务之下**的媒体库目录挂到墙上。
 *
 * # 为什么要这个功能
 *
 * 海报墙的数据源一直是 STRM 任务（任务 → 输出目录 → 扫盘）。用户手上常有一批
 * 别的工具产出的目录（nfo + 图片齐全，作品载体可能是本应用那种 `.strm`，也可能
 * 就是 `.mp4` / `.mkv` 这些真实视频文件），想直接挂上来看、顺手补刮削 ——
 * 这份弹窗就是那个入口。**纯本地，不联动网盘。**
 *
 * # 路径是「容器内路径」
 *
 * 目录选择器列的是**跑 LitePan 那个进程能看到的路径**。部署在容器里时就是容器内
 * 路径（挂在容器里的那个），不是宿主机的 —— 弹窗里明写了这一句。
 *
 * # 删除只删注册项
 *
 * 那条 `confirm` 的文案必须把「不会删文件」说清楚：名字叫「移除」但用户很容易
 * 理解成「删掉这个目录」。
 */
const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ close: []; changed: [dirs: WallLocalDir[]] }>();

const dirs = ref<WallLocalDir[]>([]);
const loading = ref(false);
const saving = ref(false);
const errorText = ref("");

// 新建表单
const browseOpen = ref(false);
const formRoot = ref("");
const formName = ref("");
const formKind = ref<"tmdb" | "jav">("jav");

const KIND_OPTIONS = [
  { value: "jav", label: "番号影片" },
  { value: "tmdb", label: "TMDB 影片 / 剧集" },
];

/** 名字没填时用路径的 basename 做占位提示（与后端默认值同一条规则）。 */
const namePlaceholder = computed(() => {
  const p = formRoot.value.replace(/[/\\]+$/, "");
  return p.split(/[/\\]/).pop() || "目录名";
});

async function load() {
  loading.value = true;
  errorText.value = "";
  try {
    const res = await fetchWallLocalDirs();
    dirs.value = res.dirs ?? [];
  } catch (e) {
    errorText.value = getApiErrorMessage(e, "读取本地目录列表失败");
    dirs.value = [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    void load();
  },
  { immediate: true },
);

function onPicked(path: string) {
  browseOpen.value = false;
  formRoot.value = path;
  if (!formName.value.trim()) {
    const p = path.replace(/[/\\]+$/, "");
    formName.value = p.split(/[/\\]/).pop() || "";
  }
}

async function save() {
  if (saving.value) return;
  const root = formRoot.value.trim();
  if (!root) {
    toast.error("请先选择目录");
    return;
  }
  saving.value = true;
  try {
    const res = await createWallLocalDir({
      root,
      name: formName.value.trim() || undefined,
      media_kind: formKind.value,
    });
    dirs.value = res.dirs ?? [];
    emit("changed", dirs.value);
    // 表单清空，方便连续加几个（同一个人往往一次要挂好几个目录）。
    formRoot.value = "";
    formName.value = "";
    toast.success("已加入海报墙");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "添加失败"));
  } finally {
    saving.value = false;
  }
}

async function remove(dir: WallLocalDir) {
  try {
    await confirm({
      title: "从海报墙移除",
      message: `把「${dir.name}」从海报墙的下拉里去掉？目录 ${dir.root} 里的文件一个都不会动。`,
      confirmText: "移除",
      danger: true,
      icon: "trash",
    });
  } catch {
    return;
  }
  try {
    const res = await deleteWallLocalDir(dir.id);
    dirs.value = res.dirs ?? [];
    emit("changed", dirs.value);
    toast.success("已移除");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "移除失败"));
  }
}
</script>

<template>
  <AppModal :open="open" bare nested @close="emit('close')">
    <div class="wall-dir">
      <header class="wall-dir__header">
        <div class="wall-dir__titles">
          <h3>海报墙 · 本地目录</h3>
          <p class="wall-dir__sub">
            把「不在 STRM 任务下」的媒体库目录挂到海报墙上。挂上之后它出现在左上角
            那个下拉里，和任务一样能看海报、能刮削、能删除。
          </p>
        </div>
        <button type="button" aria-label="关闭" @click="emit('close')">×</button>
      </header>

      <div class="wall-dir__body">
        <!-- 新建 -->
        <section class="wall-dir__block">
          <div class="wall-dir__block-title">添加目录</div>
          <div class="wall-dir__row">
            <code class="wall-dir__path" :title="formRoot">{{ formRoot || "（还没选目录）" }}</code>
            <AppButton variant="secondary" @click="browseOpen = true">浏览…</AppButton>
          </div>
          <p class="wall-dir__hint">
            ⚠️ 这里列的是「容器内路径」—— 跑 MyPan 那个进程能看到的目录（挂在容器里的那个）。
          </p>
          <div class="wall-dir__row">
            <AppInput v-model="formName" :placeholder="namePlaceholder" />
            <AppSelect v-model="formKind" :options="KIND_OPTIONS" />
            <AppButton variant="primary" :disabled="saving || !formRoot.trim()" @click="save">
              {{ saving ? "添加中…" : "添加" }}
            </AppButton>
          </div>
        </section>

        <!-- 已挂的 -->
        <section class="wall-dir__block">
          <div class="wall-dir__block-title">已挂到墙上的目录（{{ dirs.length }}）</div>
          <div v-if="loading" class="wall-dir__state">加载中…</div>
          <div v-else-if="errorText" class="wall-dir__state wall-dir__state--err">{{ errorText }}</div>
          <div v-else-if="!dirs.length" class="wall-dir__state">还没有挂任何本地目录。</div>
          <ul v-else class="wall-dir__list">
            <li v-for="d in dirs" :key="d.id" class="wall-dir__item">
              <div class="wall-dir__item-main">
                <span class="wall-dir__item-name">{{ d.name }}</span>
                <span class="wall-dir__item-tag">{{ d.media_kind === "jav" ? "番号" : "tmdb" }}</span>
                <code class="wall-dir__item-path" :title="d.root">{{ d.root }}</code>
              </div>
              <AppButton variant="ghost" size="sm" @click="remove(d)">移除</AppButton>
            </li>
          </ul>
          <p class="wall-dir__hint">「移除」只是从下拉里去掉，磁盘上的文件一个都不会动。</p>
        </section>
      </div>
    </div>
  </AppModal>

  <!-- 目录选择器复用既有那条路（容器内浏览）—— 不另写一套面包屑/错误态。 -->
  <LocalDirBrowserModal
    :open="browseOpen"
    title="选择本地目录（容器内）"
    @close="browseOpen = false"
    @select="onPicked"
  />
</template>

<style scoped>
.wall-dir {
  width: min(92vw, 680px);
  max-height: min(88vh, 640px);
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.wall-dir__header {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  padding: 20px 24px 12px;
}
.wall-dir__titles { flex: 1; min-width: 0; }
.wall-dir__header h3 { margin: 0 0 6px; color: var(--text); font-size: 18px; }
.wall-dir__sub { margin: 0; color: var(--text-muted); font-size: 12px; line-height: 1.7; }
.wall-dir__header > button {
  border: 0; background: none; color: var(--text-muted); font-size: 22px; cursor: pointer;
  line-height: 1; padding: 0 4px;
}
.wall-dir__body {
  flex: 0 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: 0 24px 20px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.wall-dir__block-title { color: var(--text); font-size: 13px; font-weight: 600; margin-bottom: 8px; }
.wall-dir__row { display: flex; align-items: center; gap: 8px; }
.wall-dir__row + .wall-dir__row { margin-top: 8px; }
.wall-dir__path {
  flex: 1;
  min-width: 0;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  color: var(--text-regular);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.wall-dir__hint { margin: 8px 0 0; color: var(--text-muted); font-size: 12px; line-height: 1.7; }
.wall-dir__state { padding: 18px 0; color: var(--text-muted); font-size: 13px; }
.wall-dir__state--err { color: var(--danger); }
.wall-dir__list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.wall-dir__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
}
.wall-dir__item-main { display: flex; align-items: center; gap: 8px; min-width: 0; }
.wall-dir__item-name { color: var(--text); font-size: 13px; font-weight: 600; white-space: nowrap; }
.wall-dir__item-tag {
  flex: none;
  padding: 1px 7px;
  border-radius: var(--radius-pill);
  background: var(--surface);
  color: var(--text-muted);
  font-size: 11px;
}
.wall-dir__item-path {
  color: var(--text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 640px) {
  .wall-dir { width: 94vw; }
  .wall-dir__header, .wall-dir__body { padding-inline: 16px; }
  .wall-dir__row { flex-wrap: wrap; }
}
</style>
