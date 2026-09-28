<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchJavWallHiddenDirs,
  saveJavWallHiddenDirs,
  type JavWallHiddenDirList,
  type JavWallHiddenDirOption,
} from "@/api/strmJavWall";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import { toast } from "@/composables/useToast";

// 番号墙「整档隐藏的目录」（页头那个「全部目录」按钮点开的那个弹窗）。
//
// 与隔壁「刮削范围」（StrmScrapeScopePicker）长得像，但**语义完全不同**，别混：
//   - 刮削范围是**排除哪些目录不刮**，按任务存，走 FolderSelector 那棵树；
//   - 这里是**哪些一级目录整档不出现在墙上**，**全局一份**，就是这个清单。
//
// **勾上 = 隐藏**（用户要求：默认不勾「未匹配」，也就是默认不显示它）。
// 存的是「隐藏名单」而不是「显示名单」：以后库里新冒出来的分类目录天然是显示的，
// 不会因为不在名单里就静默消失 —— 那是这个功能最容易踩的坑。

const props = defineProps<{
  open: boolean;
  /** 只用来取候选清单与部数（0 = 不扫盘，退回按分类规则的目标目录列）。 */
  taskId: number | null;
}>();

const emit = defineEmits<{
  close: [];
  /** 保存成功后通知外面重列墙（名单是全局的，别的墙也受影响）。 */
  saved: [];
}>();

const loading = ref(false);
const saving = ref(false);
const options = ref<JavWallHiddenDirOption[]>([]);
const fallbackName = ref("");
/** 勾上的名字集合（= 要被隐藏的）。用 Set 而不是数组：勾选是逐项改的。 */
const checked = ref<Set<string>>(new Set());
/** 出错时把原因显示在弹窗里 —— 空列表与「请求失败」在界面上长得一样，不区分就查不动。 */
const errorText = ref("");

const hiddenCount = computed(() => checked.value.size);

function applyList(data: JavWallHiddenDirList) {
  options.value = data.dirs ?? [];
  fallbackName.value = data.fallback_name ?? "";
  checked.value = new Set(data.hidden ?? []);
}

async function loadData() {
  loading.value = true;
  errorText.value = "";
  try {
    const taskId = props.taskId ?? 0;
    const data = await fetchJavWallHiddenDirs(taskId);
    applyList(data);
    // 按任务扫盘没扫出任何一级目录（输出目录还没生成 / 扫不到）时退到全局名单：
    // 只按分类规则的目标目录列。**有名字总比空白强** —— 空白会让用户以为功能坏了，
    // 而实际上他只是还没同步过。这条也兜住了「墙上能看到片、这里却是空的」那种不一致。
    if (!options.value.length && taskId > 0) {
      applyList(await fetchJavWallHiddenDirs(0));
      if (!options.value.length) {
        errorText.value = "分类规则里也还没有目标目录 —— 先去「目录整理 → 番号匹配规则设置」建一条。";
      }
    }
  } catch (error) {
    errorText.value = getApiErrorMessage(error, "读取失败");
    options.value = [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    void loadData();
  },
  { immediate: true },
);

function toggle(name: string, on: boolean) {
  const next = new Set(checked.value);
  if (on) next.add(name);
  else next.delete(name);
  checked.value = next;
}

/** 没有部数的行（兜底目录、已存名单里那些磁盘上还没有的）不显示「0 部」，显得像坏了。 */
function countLabel(opt: JavWallHiddenDirOption): string {
  return opt.count > 0 ? `${opt.count} 部` : "暂无";
}

async function save() {
  if (saving.value) return;
  saving.value = true;
  try {
    // 按候选清单的顺序回传，服务端还会再排序 + 去重。
    const dirs = options.value.filter((o) => checked.value.has(o.name)).map((o) => o.name);
    const data = await saveJavWallHiddenDirs(props.taskId ?? 0, dirs);
    applyList(data);
    toast.success(dirs.length ? `已隐藏 ${dirs.length} 个目录` : "已取消隐藏，全部目录都会显示");
    emit("saved");
    emit("close");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存失败"));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <AppModal :open="open" bare nested @close="emit('close')">
    <div class="jav-hidden">
      <header class="jav-hidden__header">
        <div class="jav-hidden__titles">
          <h3>番号墙隐藏的目录</h3>
          <p class="jav-hidden__sub">
            <b>勾上的目录整档不出现在海报墙上</b>（它的 tab 与卡片都会一起藏起来）。
            <template v-if="fallbackName">
              默认隐藏「{{ fallbackName }}」—— 那是分类规则里「没命中任何规则」的兜底桶，
              不代表真实分类，想让它显示就取消勾选。
            </template>
          </p>
        </div>
        <button type="button" aria-label="关闭" @click="emit('close')">×</button>
      </header>

      <div v-if="loading" class="jav-hidden__state">加载中…</div>
      <div v-else-if="!options.length" class="jav-hidden__state">
        <template v-if="errorText">{{ errorText }}</template>
        <template v-else>
          这个任务的输出目录里还没有一级目录。先在「STRM 任务」里同步一次，
          或确认任务的媒体类型与输出目录。
        </template>
      </div>
      <ul v-else class="jav-hidden__list">
        <li v-for="opt in options" :key="opt.name" class="jav-hidden__row">
          <label class="jav-hidden__label">
            <input
              type="checkbox"
              :checked="checked.has(opt.name)"
              @change="toggle(opt.name, ($event.target as HTMLInputElement).checked)"
            />
            <span class="jav-hidden__name">
              {{ opt.name }}
              <span v-if="opt.name === fallbackName" class="jav-hidden__badge">兜底目录</span>
            </span>
          </label>
          <span class="jav-hidden__count">{{ countLabel(opt) }}</span>
        </li>
      </ul>

      <footer class="jav-hidden__footer">
        <span>
          {{ hiddenCount ? `已勾选 ${hiddenCount} 个目录（这些都会被藏起来）` : "没有勾选任何目录，全部都会显示" }}
        </span>
        <div class="jav-hidden__actions">
          <AppButton variant="secondary" :disabled="saving" @click="emit('close')">取消</AppButton>
          <AppButton variant="primary" :disabled="saving || loading" @click="save">
            {{ saving ? "保存中…" : "保存" }}
          </AppButton>
        </div>
      </footer>
    </div>
  </AppModal>
</template>

<style scoped>
.jav-hidden {
  width: min(90vw, 560px);
  /* 弹窗高度**由内容决定**（列表通常就几行），超过视口才滚 —— 所以列表那条是
     `flex: 0 1 auto` 而不是 `flex: 1 1 0`：后者在 max-height 不生效时会把列表
     压成 0 高（`.modal--bare` 的 `overflow: visible` 让里面那个 86vh 上限失效），
     症状就是「弹窗打开了、标题和按钮都在，中间一片空白，一个目录都看不见」。 */
  max-height: min(86vh, 620px);
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
.jav-hidden__header {
  display: flex;
  align-items: flex-start;
  padding: 20px 24px 14px;
  gap: 16px;
}
.jav-hidden__titles { flex: 1; min-width: 0; }
.jav-hidden__header h3 { margin: 0 0 6px; color: var(--text); font-size: 18px; }
.jav-hidden__sub { margin: 0; color: var(--text-muted); font-size: 12px; line-height: 1.7; }
.jav-hidden__header > button {
  border: 0; background: none; color: var(--text-muted); font-size: 22px; cursor: pointer;
  line-height: 1; padding: 0 4px;
}
.jav-hidden__state { padding: 32px 24px; text-align: center; color: var(--text-muted); font-size: 13px; }
.jav-hidden__list {
  /* 内容驱动高度：几行就几行高，长了（有上限）才出现内部滚动条。
     写成 `flex: 1 1 0` 会在「上限没生效」的情形下被压成 0 高 —— 见上面那段注释。 */
  flex: 0 1 auto;
  min-height: 0;
  overflow-y: auto;
  margin: 0;
  padding: 0 24px 8px;
  list-style: none;
}
.jav-hidden__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 9px 10px;
  border-radius: var(--radius-sm);
}
.jav-hidden__row:hover { background: var(--surface-sunken); }
.jav-hidden__label {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
  cursor: pointer;
  color: var(--text);
  font-size: 13px;
}
.jav-hidden__label input { width: 15px; height: 15px; accent-color: var(--brand); cursor: pointer; }
.jav-hidden__name { display: flex; align-items: center; gap: 8px; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jav-hidden__badge {
  padding: 1px 6px;
  border-radius: var(--radius-pill);
  background: var(--surface-sunken);
  color: var(--text-muted);
  font-size: 11px;
  white-space: nowrap;
}
.jav-hidden__count { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
.jav-hidden__footer {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  border-top: 1px solid var(--border);
  padding: 14px 24px;
  color: var(--text-muted);
  font-size: 12px;
}
.jav-hidden__actions { display: flex; gap: 8px; }
@media (max-width: 640px) {
  .jav-hidden { width: 94vw; }
  .jav-hidden__header, .jav-hidden__footer, .jav-hidden__list { padding-inline: 18px; }
  .jav-hidden__footer { flex-direction: column; align-items: stretch; }
}
</style>
