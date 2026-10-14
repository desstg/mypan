<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  deleteTmdbWallItem,
  previewTmdbWallDelete,
  type TmdbWallDeletePreview,
  type WallSourceRef,
} from "@/api/strmScrape";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import { toast } from "@/composables/useToast";
import "@/styles/jav.css";

/**
 * TMDB 影片墙「删除一部作品」的确认弹窗。
 *
 * 与番号墙那份（StrmJavWallDeleteModal）是同一套路：清单由服务端算、前端只负责
 * 摆出来 + 二次确认。**但语义差一条，文案必须跟着差**：
 *
 *	番号墙的「一部」= 一个 `.strm` 主干（多分片是**多部**，共用一个目录）
 *	TMDB 墙的「一部」= 一个**作品目录**（电影就是那一部；**剧集是整部**）
 *
 * 所以这里的危险动作是「整个作品目录端掉」，而剧集尤其要说清是**整部**，
 * 不是删一集 —— 用户点之前必须看见「共 N 个文件（含 Season 子目录）」。
 *
 * 本地删了不进任何回收站（真的 `os.RemoveAll`），网盘那份进回收站可恢复，
 * 所以文案的重心放本地那一半。
 */
const props = defineProps<{
  open: boolean;
  /** 数据源：任务 id 或本地目录源（见 api 的 WallSourceRef）。 */
  source: number | WallSourceRef;
  /** 要删的那一部（卡片的字段子集）。 */
  item: { id: string; title: string; media_type: string } | null;
}>();

const emit = defineEmits<{
  close: [];
  /** 删成功之后通知外面把这张卡从墙上拿掉。 */
  deleted: [];
}>();

const loading = ref(false);
const deleting = ref(false);
const preview = ref<TmdbWallDeletePreview | null>(null);
const errorText = ref("");

const title = computed(() => props.item?.title || "这一部");

/** 剧集 = 整部删，文案要单独说；电影就是这一部。 */
const isTV = computed(() => (preview.value?.media_type ?? props.item?.media_type) === "tv");

/** 「共 N 个文件」，被截断时说明一下。 */
const localCountText = computed(() => {
  const p = preview.value;
  if (!p) return "";
  if (p.local_file_count > p.local_files.length) {
    return `${p.local_file_count} 个文件（下面只列了前 ${p.local_files.length} 个）`;
  }
  return `${p.local_file_count} 个文件`;
});

/** 本地那一批的显示名：**相对作品目录**的路径。
 *
 * 不能只取文件名 —— 剧集那份清单是递归的，作品根与 `Season 01/` 下各有一个
 * `poster.jpg`，只显示文件名会出现两条一模一样的行，用户没法核对。 */
const localNames = computed(() => {
  const dir = (preview.value?.local_dir ?? "").replace(/\/+$/, "");
  const prefix = dir ? dir + "/" : "";
  return (preview.value?.local_files ?? []).map((f) =>
    prefix && f.startsWith(prefix) ? f.slice(prefix.length) : f,
  );
});

async function loadPreview() {
  if (!props.open) return;
  preview.value = null;
  errorText.value = "";
  const ref = props.source;
  const it = props.item;
  if (!it) {
    errorText.value = "没有选中要删除的作品";
    return;
  }
  loading.value = true;
  try {
    preview.value = await previewTmdbWallDelete(ref, it.id);
  } catch (error) {
    errorText.value = getApiErrorMessage(error, "读取「会删掉什么」失败");
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    void loadPreview();
  },
  { immediate: true },
);

/** 删除进行中不让关窗 —— 关掉之后请求还在飞，用户看不到结果。 */
function onClose() {
  if (deleting.value) return;
  emit("close");
}

async function confirmDelete() {
  if (deleting.value || !preview.value) return;
  const ref = props.source;
  const it = props.item;
  if (!it) return;
  deleting.value = true;
  try {
    const res = await deleteTmdbWallItem(ref, it.id);
    const parts = [`已删除 ${res.removed_files} 个本地文件`];
    if (res.removed_dir) parts.push("本地目录已删除");
    if (res.cloud_deleted) parts.push("网盘目录已删除");
    else if (res.cloud_skipped) parts.push("网盘未删除");
    toast.success(parts.join("，"));
    emit("deleted");
    emit("close");
  } catch (error) {
    // 失败时**不关窗**：报错摆在弹窗里，用户能看清是「网盘那一步失败了（本地没动）」，
    // 再决定重试还是取消。
    errorText.value = getApiErrorMessage(error, "删除失败");
  } finally {
    deleting.value = false;
  }
}
</script>

<template>
  <AppModal :open="open" bare nested @close="onClose">
    <div class="jav-del">
      <header class="jav-del__header">
        <div class="jav-del__titles">
          <h3>删除《{{ title }}》</h3>
          <p class="jav-del__sub">
            <template v-if="isTV">
              这一步会把整部剧（含各季目录下的所有集）从本地与网盘一起删掉。
              本地文件不可恢复，网盘那份进回收站。
            </template>
            <template v-else>
              这一步会删掉「本地文件」（不可恢复）与「网盘上的那一份」（进回收站）。请核对下面的清单。
            </template>
          </p>
        </div>
        <button type="button" aria-label="关闭" :disabled="deleting" @click="onClose">×</button>
      </header>

      <div v-if="loading" class="jav-del__state">正在核对会删掉什么…</div>
      <div v-else-if="errorText && !preview" class="jav-del__state jav-del__state--err">{{ errorText }}</div>

      <div v-else-if="preview" class="jav-del__body">
        <section class="jav-del__section">
          <div class="jav-del__section-title">
            <i class="fas fa-hard-drive" /> 本地文件（{{ localCountText }}）
          </div>
          <ul class="jav-del__list">
            <li v-for="name in localNames" :key="name">{{ name }}</li>
          </ul>
          <p class="jav-del__note">
            本地目录「{{ preview.local_dir || "任务根目录" }}」整个删除
          </p>
        </section>

        <section class="jav-del__section">
          <div class="jav-del__section-title"><i class="fas fa-cloud" /> 网盘</div>
          <template v-if="preview.cloud_skipped">
            <p class="jav-del__warn">网盘未删除：{{ preview.cloud_skipped }}</p>
          </template>
          <template v-else>
            <p class="jav-del__kv">
              删除整个目录：<code>{{ preview.cloud_path }}</code>
            </p>
            <p class="jav-del__note">目录里的视频、字幕、附件会一起进回收站。</p>
          </template>
        </section>

        <p v-if="isTV" class="jav-del__warn">
          这是一部剧集：删的是整部（所有季、所有集），不是单独一集。
        </p>

        <p v-if="errorText" class="jav-del__state--err">{{ errorText }}</p>
      </div>

      <footer class="jav-del__footer">
        <AppButton variant="secondary" :disabled="deleting" @click="onClose">取消</AppButton>
        <AppButton
          variant="danger"
          :disabled="deleting || loading || !preview"
          @click="confirmDelete"
        >
          {{ deleting ? "删除中…" : isTV ? "确认删除整部" : "确认删除" }}
        </AppButton>
      </footer>
    </div>
  </AppModal>
</template>

<!-- 样式在 `web/src/styles/jav.css` 的 `.jav-del*` 里（与番号墙那份**共用**）。 -->
