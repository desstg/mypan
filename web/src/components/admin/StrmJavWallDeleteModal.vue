<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { getApiErrorMessage } from "@/api/client";
import {
  deleteJavWallItem,
  previewJavWallDelete,
  type JavWallDeletePreview,
} from "@/api/strmJavWall";
import type { WallSourceRef } from "@/api/strmScrape";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import { toast } from "@/composables/useToast";
import "@/styles/jav.css";

/**
 * 番号墙「删除一部」的确认弹窗。
 *
 * # 为什么不是普通 confirm
 *
 * 这是整个项目里唯一一个**会删用户东西**的动作，而且删两处（本地 + 网盘）、
 * 有三档语义（整个目录 / 只这一部的文件 / 网盘那份没删成）。一句「确定删除吗？」
 * 说不清会删掉什么 —— 用户点确认之前必须看见**具体清单**。
 *
 * 清单由服务端算（`delete-preview`），前端只负责摆出来 + 二次确认：
 * 前端自己拼的话，两边的判据迟早会漂，而那一漂就是事故。
 *
 * # 文案里必须说清的两件事
 *
 *   - **本地删了不可恢复**（它是真的 `os.Remove`，不进任何回收站）；
 *   - **平铺布局只删这一部**（同目录还有别的作品载体，目录与那几张共用图都留着）。
 *
 * 网盘那份进回收站是可以恢复的，所以危险的是本地那一半 —— 文案的重心放这边。
 */
const props = defineProps<{
  open: boolean;
  /** 数据源：任务 id 或本地目录源（见 api 的 WallSourceRef）。 */
  source: number | WallSourceRef;
  /** 要删的那一部（卡片的字段子集）。 */
  item: { number: string; title: string; rel_dir: string; stem: string } | null;
}>();

const emit = defineEmits<{
  close: [];
  /** 删成功之后通知外面把这张卡从墙上拿掉。 */
  deleted: [];
}>();

const loading = ref(false);
const deleting = ref(false);
const preview = ref<JavWallDeletePreview | null>(null);
const errorText = ref("");

const title = computed(() => props.item?.number || props.item?.title || props.item?.stem || "这一部");

/** 本地那批文件的显示名（只取文件名，路径那段是目录，没必要占地方）。 */
const localNames = computed(() =>
  (preview.value?.local_files ?? []).map((f) => f.split("/").pop() ?? f),
);

async function loadPreview() {
  if (!props.open) return;
  preview.value = null;
  errorText.value = "";
  const id = props.source ?? 0;
  const it = props.item;
  if (!id || !it) {
    errorText.value = "没有选中要删除的影片";
    return;
  }
  loading.value = true;
  try {
    preview.value = await previewJavWallDelete(id, it.rel_dir, it.stem);
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

/** 本地目录那一句（**只在非平铺时用**，见模板里那条 `v-if="!preview.flat"`）。 */
const localDirText = computed(() => {
  const p = preview.value;
  if (!p) return "";
  const dir = p.local_dir || "任务根目录";
  if (p.remove_local_dir) return `本地目录「${dir}」删空后一并删除`;
  return `本地目录「${dir}」保留（目录里还有本程序不认识的其它文件）`;
});

/** 删除进行中不让关窗 —— 关掉之后请求还在飞，用户看不到结果。 */
function onClose() {
  if (deleting.value) return;
  emit("close");
}

async function confirmDelete() {
  if (deleting.value || !preview.value) return;
  const id = props.source ?? 0;
  const it = props.item;
  if (!id || !it) return;
  deleting.value = true;
  try {
    const res = await deleteJavWallItem(id, it.rel_dir, it.stem);
    const parts = [`已删除 ${res.removed_files} 个本地文件`];
    if (res.removed_dir) parts.push("本地目录已删除");
    if (res.cloud_deleted) parts.push(res.cloud_remove_dir ? "网盘目录已删除" : "网盘文件已删除");
    else if (res.cloud_skipped) parts.push("网盘未删除");
    toast.success(parts.join("，"));
    emit("deleted");
    emit("close");
  } catch (error) {
    // 失败时**不关窗**，也**不刷新墙** —— 报错信息摆在弹窗里，用户能看清是
    // 「网盘那一步失败了（本地没动）」，再决定重试还是取消。
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
            这一步会删掉「本地文件」（不可恢复）与「网盘上的那一份」（进回收站）。请核对下面的清单。
          </p>
        </div>
        <button type="button" aria-label="关闭" :disabled="deleting" @click="onClose">×</button>
      </header>

      <div v-if="loading" class="jav-del__state">正在核对会删掉什么…</div>
      <div v-else-if="errorText && !preview" class="jav-del__state jav-del__state--err">{{ errorText }}</div>

      <div v-else-if="preview" class="jav-del__body">
        <!-- 本地 -->
        <section class="jav-del__section">
          <div class="jav-del__section-title">
            <i class="fas fa-hard-drive" /> 本地文件（{{ preview.local_files.length }}）
          </div>
          <ul class="jav-del__list">
            <li v-for="name in localNames" :key="name">{{ name }}</li>
          </ul>
          <!-- 本地目录这一句**只在这个目录属于一部时**有意义。平铺目录（多分片）里
               它属于好几部，说「目录删空后一并删除」会让人以为整个目录要没了 ——
               那种情形由下面那条 warn 来说。 -->
          <p v-if="!preview.flat" class="jav-del__note">{{ localDirText }}</p>
        </section>

        <!-- 网盘 -->
        <section class="jav-del__section">
          <div class="jav-del__section-title"><i class="fas fa-cloud" /> 网盘</div>
          <template v-if="preview.cloud_skipped">
            <p class="jav-del__warn">网盘未删除：{{ preview.cloud_skipped }}</p>
          </template>
          <template v-else-if="preview.cloud_remove_dir">
            <p class="jav-del__kv">
              删除整个目录：<code>{{ preview.cloud_path }}</code>
            </p>
            <p class="jav-del__note">目录里的视频、字幕、附件会一起进回收站。</p>
          </template>
          <template v-else>
            <p class="jav-del__kv">只删除这一部的文件（目录保留）：</p>
            <ul class="jav-del__list">
              <li v-for="name in preview.cloud_files ?? []" :key="name">{{ name }}</li>
            </ul>
          </template>
        </section>

        <p v-if="preview.flat" class="jav-del__warn">
          这个目录里有多个影片文件：只删这一部，同目录其它影片不受影响。
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
          {{ deleting ? "删除中…" : "确认删除" }}
        </AppButton>
      </footer>
    </div>
  </AppModal>
</template>

<!-- 样式在 `web/src/styles/jav.css` 的 `.jav-del*` 里（与 TMDB 墙那份**共用**）：
     scoped 样式不跨组件，两份逐字复制迟早会漂，而这两个弹窗必须长得一样。 -->
