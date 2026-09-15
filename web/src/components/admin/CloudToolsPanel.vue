<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  cloudToolsApi,
  type CloudTool115Status,
} from "@/api/cloudTools";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import AppButton from "@/components/base/AppButton.vue";
import AIToolCard from "@/components/admin/AIToolCard.vue";
import ClassificationToolCard from "@/components/admin/ClassificationToolCard.vue";
import CleanupToolCard from "@/components/admin/CleanupToolCard.vue";
import CoverExtractToolCard from "@/components/admin/CoverExtractToolCard.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";
import LocalUploadToolCard from "@/components/admin/LocalUploadToolCard.vue";
import ProxyToolsPanel from "@/components/admin/ProxyToolsPanel.vue";
import QuarkTVToolCard from "@/components/admin/QuarkTVToolCard.vue";

const props = withDefaults(defineProps<{ searchOpen?: boolean }>(), { searchOpen: false });
const emit = defineEmits<{ "update:searchOpen": [boolean] }>();

const { runLoad } = useSettingsLoad();

const searchQuery = ref("");
const searchInputRef = ref<HTMLInputElement | null>(null);
const cardTitles = ["Emby 反代", "飞牛影视反代", "115 STRM 增强", "夸克 STRM 接管", "AI 辅助识别", "目录整理分类", "从服务器上传", "垃圾清理工具", "视频海报生成"];

function matches(title: string) {
  const q = searchQuery.value.trim().toLowerCase();
  return !q || title.toLowerCase().includes(q);
}

const hasMatch = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  return !q || cardTitles.some((t) => t.toLowerCase().includes(q));
});

function closeSearch() {
  searchQuery.value = "";
  emit("update:searchOpen", false);
}

watch(
  () => props.searchOpen,
  async (open) => {
    if (open) {
      await nextTick();
      searchInputRef.value?.focus();
    } else {
      searchQuery.value = "";
    }
  },
);

const status = ref<CloudTool115Status>({ enabled: false, cache_count: 0, available: false });
const saving = ref(false);
const clearing = ref(false);

async function load() {
  await runLoad(async () => {
    const st = await cloudToolsApi.status115();
    status.value = st;
  }, "加载网盘工具状态失败");
}

onMounted(load);

async function toggleEnabled() {
  saving.value = true;
  const next = !status.value.enabled;
  try {
    const res = await cloudToolsApi.set115Enabled(next);
    status.value.enabled = res.enabled;
    toast.success(
      res.enabled
        ? "已启用：115Open 账号的 STRM 任务将改用全量清单模式执行"
        : "已停用：115Open 账号的 STRM 任务恢复逐目录递归",
    );
  } catch (e) {
    toast.error(getApiErrorMessage(e, "修改开关失败"));
  } finally {
    saving.value = false;
  }
}

async function clearCache() {
  const ok = await confirm({
    title: "清空映射数据？",
    message:
      `将删除 ${status.value.cache_count.toLocaleString("zh-CN")} 条目录路径映射记录，` +
      "下次该账号执行 STRM 任务时会重新解析目录路径，用于纠正目录被移动 / 重命名后的路径漂移。此操作不影响网盘文件与已生成的 STRM 文件。",
    confirmText: "确认清空",
    cancelText: "取消",
    danger: true,
  }).catch(() => false);
  if (!ok) return;

  clearing.value = true;
  try {
    const res = await cloudToolsApi.clear115Cache(0);
    toast.success(`已清空 ${res.removed.toLocaleString("zh-CN")} 条路径映射记录`);
    await load();
  } catch (e) {
    toast.error(getApiErrorMessage(e, "清空映射数据失败"));
  } finally {
    clearing.value = false;
  }
}

</script>

<template>
  <div class="cloud-tools">
    <div v-if="searchOpen" class="tool-search">
      <div class="tool-search__mask" @click="closeSearch" />
      <div class="tool-search__box">
        <input ref="searchInputRef" v-model="searchQuery" placeholder="搜索工具，如：飞牛、Emby、反代" @keydown.esc="closeSearch" />
        <button type="button" @click="closeSearch">×</button>
      </div>
    </div>
    <div class="cloud-tools__grid">
      <ProxyToolsPanel :search-query="searchQuery" />
      <CloudToolCard
        v-show="matches('115 STRM 增强')"
        :enabled="status.enabled"
        name="115 STRM 增强"
        driver="115Open · STRM 全量清单扫描"
        logo-src="/logos/115.png"
        logo-alt="115"
        :tags="[{ label: '实验性', variant: 'warn' }]"
        :stat-value="status.cache_count.toLocaleString('zh-CN')"
        stat-label="条路径映射关系"
      >
        <template #toggle>
          <button
            class="check-toggle"
            type="button"
            :class="{ on: status.enabled }"
            :aria-label="status.enabled ? '停用 115 STRM 增强' : '启用 115 STRM 增强'"
            :disabled="saving || !status.available"
            title="启用 / 停用"
            @click="toggleEnabled"
          >
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path
                d="M3.5 8.5 6.5 11.5 12.5 4.5"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </button>
        </template>
        使用全量清单扫描方式，减少逐层目录请求，减少网盘风控几率，还可加速 STRM 生成。
        <template #actions>
          <AppButton size="sm" variant="danger" :disabled="clearing" @click="clearCache">
            {{ clearing ? "清空中…" : "清空映射" }}
          </AppButton>
        </template>
      </CloudToolCard>

      <QuarkTVToolCard :search-query="searchQuery" />

      <AIToolCard :search-query="searchQuery" />

      <ClassificationToolCard :search-query="searchQuery" />

      <LocalUploadToolCard :search-query="searchQuery" />

      <CleanupToolCard :search-query="searchQuery" />

      <CoverExtractToolCard :search-query="searchQuery" />
    </div>
    <div v-if="searchOpen && !hasMatch" class="tool-search__empty">没有找到相关工具</div>
  </div>
</template>

<style scoped>
.tool-search__mask {
  position: fixed;
  inset: 0;
  z-index: var(--z-modal);
  background: rgba(15, 23, 42, 0.35);
}
.tool-search__box {
  position: fixed;
  top: 140px;
  left: 50%;
  transform: translateX(-50%);
  z-index: calc(var(--z-modal) + 1);
  display: flex;
  align-items: center;
  gap: 8px;
  width: min(520px, calc(100vw - 40px));
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-pop);
  padding: 12px 16px;
}
.tool-search__box input {
  flex: 1;
  border: none;
  outline: none;
  background: transparent;
  font-size: 15px;
  color: var(--text);
}
.tool-search__box button {
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 16px;
  padding: 2px 6px;
  border-radius: var(--radius-sm);
}
.tool-search__box button:hover {
  background: var(--border-soft);
  color: var(--text);
}
.tool-search__empty {
  margin-top: 16px;
  text-align: center;
  color: var(--text-muted);
  font-size: 14px;
  padding: 40px 0;
}

.cloud-tools__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
  align-items: start;
  gap: 16px;
}

.check-toggle {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  border: 0;
  padding: 0;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  background: var(--border);
  color: var(--text-muted);
  transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease;
}
.check-toggle svg {
  width: 14px;
  height: 14px;
}
.check-toggle:hover {
  background: var(--surface-hover);
}
.check-toggle.on {
  background: var(--success);
  color: #fff;
  box-shadow: 0 0 0 4px rgba(16, 185, 129, 0.16);
}
.check-toggle.on:hover {
  background: color-mix(in srgb, var(--success) 88%, #000);
}
.check-toggle:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
