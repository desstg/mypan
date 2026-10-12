<script setup lang="ts">
/**
 * 「搜索转存」页的外壳：搜索 / 转存 / 转存记录 三个 tab。
 *
 * 账号列表在这一层拉一次，几个 tab 共用 —— 都要选「保存到哪个网盘」，
 * 各拉一次是几次重复请求，还会让两边的选择状态不一致。
 */
import { computed, defineAsyncComponent, onMounted, ref, watch } from "vue";
import { storeToRefs } from "pinia";
import AppButton from "@/components/base/AppButton.vue";
import SectionTabBar from "@/components/admin/SectionTabBar.vue";
import SearchTransferRecords from "@/components/admin/SearchTransferRecords.vue";
import { useSectionTabRoute } from "@/composables/useSectionTabRoute";
import { useAccountsStore } from "@/stores/accounts";
import "@/styles/search-transfer.css";

const SearchTransferSearch = defineAsyncComponent(
  () => import("@/components/admin/SearchTransferSearch.vue"),
);
const SearchTransferManual = defineAsyncComponent(
  () => import("@/components/admin/SearchTransferManual.vue"),
);

// 记录有新增时置脏：只用来在 tab 上点一个小圆点提示「有新记录」。
const recordsDirty = ref(false);
const recordsRef = ref<{ reload: () => void } | null>(null);

const SEARCH_TAB = "search";
const TRANSFER_TAB = "transfer";
const RECORDS_TAB = "records";

// ⚠️「转存记录」**不进 tabs** —— 它的入口只有右上角那颗按钮（与「开始刮削」
// 同样式的那个）。两处都放会在同一排上出现两个同名控件。
// 但 RECORDS_TAB 仍要留在 validTabs 里：激活态走的是 route.query.tab，
// 去掉它会让点进记录页之后被 normalize 回落到「搜索」。
const tabs = computed(() => [
  { key: SEARCH_TAB, label: "搜索" },
  { key: TRANSFER_TAB, label: "转存" },
]);

const { activeTab, setActiveTab } = useSectionTabRoute(SEARCH_TAB, [
  SEARCH_TAB,
  TRANSFER_TAB,
  RECORDS_TAB,
]);

const accountsStore = useAccountsStore();
const { accounts, loading: accountsLoading } = storeToRefs(accountsStore);

/** 右上角那颗按钮：与页头其它主操作同样式（同海报墙的「开始刮削」），
 *  点它切到「转存记录」这一档。 */
function openRecords() {
  // 已经在这一档上时 setActiveTab 会提前返回、下面的 watch 也不会触发，
  // 所以这种情况要自己重拉一次（用户点它就是想看最新的）。
  if (activeTab.value === RECORDS_TAB) {
    recordsDirty.value = false;
    recordsRef.value?.reload();
    return;
  }
  void setActiveTab(RECORDS_TAB);
}

// 切到「转存记录」这一档就重拉一次。
//
// ⚠️ 必须带 immediate，而且不能只靠组件里的 onMounted：面板是 v-show 保活，
// 首次挂载之后**再也不会**重新挂载，光靠挂载那一拍会一直停在首次的结果 ——
// 「处理中」的记录就再也不会收敛成真实状态了。
watch(
  activeTab,
  (tab) => {
    if (tab !== RECORDS_TAB) return;
    recordsDirty.value = false;
    recordsRef.value?.reload();
  },
  { immediate: true },
);

onMounted(() => {
  void accountsStore.loadAccounts();
});
</script>

<template>
  <div class="search-transfer-page">
    <SectionTabBar :tabs="tabs" :model-value="activeTab" @update:model-value="setActiveTab">
      <template #actions>
        <AppButton type="button" variant="primary" @click="openRecords">
          转存记录<span v-if="recordsDirty" class="st-records-dot" aria-hidden="true" />
        </AppButton>
      </template>
    </SectionTabBar>

    <div v-show="activeTab === SEARCH_TAB" class="stp-pane">
      <SearchTransferSearch
        :accounts="accounts"
        :accounts-loading="accountsLoading"
        @saved="recordsDirty = true"
      />
    </div>
    <div v-show="activeTab === TRANSFER_TAB" class="stp-pane">
      <SearchTransferManual :accounts="accounts" :accounts-loading="accountsLoading" />
    </div>
    <div v-show="activeTab === RECORDS_TAB" class="stp-pane">
      <SearchTransferRecords ref="recordsRef" />
    </div>
  </div>
</template>

<style scoped>
.search-transfer-page {
  display: flex;
  flex-direction: column;
}

/* 「有新记录」的小圆点：跟 Tab 上那个「未保存改动」的点同款语义。
   按钮是 primary（实心品牌色底），所以点用**反色**才看得见 —— 用 --brand
   会和按钮底色糊在一起，等于没有提示。 */
.st-records-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  margin-left: 5px;
  border-radius: 50%;
  background: var(--text-on-brand, #fff);
  vertical-align: middle;
}
</style>
