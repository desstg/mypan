<script setup lang="ts">
import { defineAsyncComponent, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";

// 普传视图（默认）：先下载后上传的整目录搬家。
const CrossDrivePlainTransfer = defineAsyncComponent(
  () => import("@/components/admin/CrossDrivePlainTransfer.vue"),
);
// 秒传视图：沿用原「跨盘秒传」完整交互（scan/probe/execute + 兜底 relay），
// 懒加载避免普传首屏拉取大组件；v-show 保活两边的状态。
const CrossDriveTransfer = defineAsyncComponent(
  () => import("@/components/admin/CrossDriveTransfer.vue"),
);

const PLAIN_TAB = "plain";
const RAPID_TAB = "rapid";

const tabs = [
  { key: PLAIN_TAB, label: "跨盘普传" },
  { key: RAPID_TAB, label: "跨盘秒传" },
];

const { activeTab, setActiveTab } = useSectionTabRoute(PLAIN_TAB, [PLAIN_TAB, RAPID_TAB]);

// 秒传子组件实例：用于调用其矩阵弹层
const rapidViewRef = ref<{ openMatrix?: () => void } | null>(null);
function openRapidMatrix() {
  rapidViewRef.value?.openMatrix?.();
}
</script>

<template>
  <div class="cross-transfer-page">
    <SectionTabBar :tabs="tabs" :model-value="activeTab" @update:model-value="setActiveTab">
      <template #actions>
        <AppButton
          v-if="activeTab === RAPID_TAB"
          type="button"
          variant="primary"
          @click="openRapidMatrix"
        >
          线路选择
        </AppButton>
      </template>
    </SectionTabBar>
    <div v-show="activeTab === PLAIN_TAB" class="ctp-pane">
      <CrossDrivePlainTransfer />
    </div>
    <div v-show="activeTab === RAPID_TAB" class="ctp-pane">
      <CrossDriveTransfer ref="rapidViewRef" />
    </div>
  </div>
</template>

<style scoped>
/* TabBar 自带 margin-bottom:18px（AppTabBar），容器不再叠加间距，与任务管理等页面一致。 */
.cross-transfer-page {
  display: flex;
  flex-direction: column;
}

.ctp-pane {
  min-height: 0;
}
</style>
