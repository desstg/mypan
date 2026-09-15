<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import type { Crumb } from "@/stores/browser";

// compact：窄对话框专用，深度>3 时收缩为根+…+末两级。
// maxVisible：可选，覆盖折叠阈值（简约首页导航用，空间被账号下拉占用时单独限定）。
const props = defineProps<{ items: Crumb[]; compact?: boolean; maxVisible?: number }>();
const emit = defineEmits<{ navigate: [index: number] }>();

function collapseThreshold(width: number) {
  if (width >= 1400) return 8;
  if (width >= 1200) return 8;
  if (width >= 1000) return 7;
  if (width >= 800) return 5;
  return 4;
}

const screenThreshold = ref(collapseThreshold(window.innerWidth));

const maxItems = computed(() => {
  const count = props.items.length;
  const threshold = screenThreshold.value;
  return count <= threshold ? count : threshold + 1;
});

const collapsed = computed(() => {
  if (props.compact) return props.items.length > 3;
  const threshold = props.maxVisible ?? maxItems.value;
  return props.items.length > threshold;
});

const tailCount = computed(() =>
  props.compact ? 2 : (props.maxVisible ?? maxItems.value) - 2,
);

const hiddenItems = computed(() => {
  if (!collapsed.value) return [] as { crumb: Crumb; index: number }[];
  const lastCount = tailCount.value;
  return props.items.slice(1, props.items.length - lastCount).map((crumb, i) => ({
    crumb,
    index: i + 1,
  }));
});

const visibleTail = computed(() => {
  if (!collapsed.value) return [] as { crumb: Crumb; index: number }[];
  const lastCount = tailCount.value;
  return props.items.slice(-lastCount).map((crumb, i) => ({
    crumb,
    index: props.items.length - lastCount + i,
  }));
});

function go(index: number) {
  if (index < 0 || index >= props.items.length - 1) return;
  emit("navigate", index);
}

let resizeTimer: ReturnType<typeof setTimeout> | undefined;

function onResize() {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    screenThreshold.value = collapseThreshold(window.innerWidth);
  }, 100);
}

onMounted(() => window.addEventListener("resize", onResize));
onUnmounted(() => {
  window.removeEventListener("resize", onResize);
  clearTimeout(resizeTimer);
});
</script>

<template>
  <nav class="breadcrumb">
    <template v-if="!collapsed">
      <span
        v-for="(item, index) in items"
        :key="item.id"
        class="breadcrumb-item"
        :class="{ active: index === items.length - 1 }"
        :title="item.name"
        @click="go(index)"
      >
        <span class="breadcrumb-item-label">{{ item.name }}</span>
      </span>
    </template>

    <template v-else>
      <span class="breadcrumb-item" :title="items[0].name" @click="go(0)">
        <span class="breadcrumb-item-label">{{ items[0].name }}</span>
      </span>

      <span class="breadcrumb-ellipsis-dropdown">
        <span class="breadcrumb-ellipsis">...</span>
        <div class="breadcrumb-dropdown">
          <div
            v-for="node in hiddenItems"
            :key="node.crumb.id"
            class="breadcrumb-dropdown-item"
            :title="node.crumb.name"
            @click="go(node.index)"
          >
            <span class="breadcrumb-dropdown-item-label">{{ node.crumb.name }}</span>
          </div>
        </div>
      </span>

      <span
        v-for="node in visibleTail"
        :key="node.crumb.id"
        class="breadcrumb-item"
        :class="{ active: node.index === items.length - 1 }"
        :title="node.crumb.name"
        @click="go(node.index)"
      >
        <span class="breadcrumb-item-label">{{ node.crumb.name }}</span>
      </span>
    </template>
  </nav>
</template>

<style scoped>
.breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-muted);
  font-size: 14px;
  flex: 1;
  min-width: 0;
  flex-wrap: nowrap;
  white-space: nowrap;
}

.breadcrumb-item {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  max-width: min(180px, 16vw);
  flex-shrink: 1;
  cursor: pointer;
  transition: color 0.2s;
  font-weight: 400;
}

.breadcrumb-item.active {
  max-width: min(320px, 28vw);
  color: var(--text);
  cursor: default;
}

.breadcrumb-item-label {
  display: inline-block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.breadcrumb-item:hover:not(.active) {
  color: var(--brand);
}

.breadcrumb-item:not(:last-child)::after {
  content: "";
  display: inline-block;
  width: 8px;
  height: 8px;
  background-color: var(--text-muted);
  -webkit-mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 1024 1024'%3E%3Cpath d='M766.976 586.24L335.317333 945.749333a61.013333 61.013333 0 0 1-65.365333 8.277334 62.250667 62.250667 0 0 1-35.285333-56.32V168.96c0-24.234667 13.866667-46.250667 35.626666-56.490667a61.013333 61.013333 0 0 1 65.621334 8.96l431.658666 369.536a62.464 62.464 0 0 1-0.597333 95.402667v-0.128z'/%3E%3C/svg%3E") center / contain no-repeat;
  mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 1024 1024'%3E%3Cpath d='M766.976 586.24L335.317333 945.749333a61.013333 61.013333 0 0 1-65.365333 8.277334 62.250667 62.250667 0 0 1-35.285333-56.32V168.96c0-24.234667 13.866667-46.250667 35.626666-56.490667a61.013333 61.013333 0 0 1 65.621334 8.96l431.658666 369.536a62.464 62.464 0 0 1-0.597333 95.402667v-0.128z'/%3E%3C/svg%3E") center / contain no-repeat;
  margin-left: 8px;
  vertical-align: middle;
  flex-shrink: 0;
}

.breadcrumb-ellipsis-dropdown {
  flex-shrink: 0;
  position: relative;
  z-index: 6;
}

.breadcrumb-ellipsis {
  color: var(--text-muted);
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  transition: color 0.2s, background-color 0.2s;
  user-select: none;
}

.breadcrumb-ellipsis:hover {
  color: var(--brand);
  background-color: var(--surface-hover);
}

.breadcrumb-dropdown {
  position: absolute;
  top: 100%;
  left: 50%;
  transform: translateX(-50%) translateY(-5px);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05);
  z-index: 10020;
  min-width: 120px;
  max-width: min(280px, 40vw);
  opacity: 0;
  visibility: hidden;
  transition: opacity 0.2s ease, transform 0.2s ease, visibility 0.2s;
  padding: 4px 0;
}

.breadcrumb-ellipsis-dropdown:hover .breadcrumb-dropdown {
  opacity: 1;
  visibility: visible;
  transform: translateX(-50%) translateY(0);
}

.breadcrumb-dropdown-item {
  display: block;
  max-width: min(240px, 28vw);
  padding: 8px 16px;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
  font-size: 14px;
  font-weight: 400;
  color: var(--text-regular);
  margin: 2px 4px;
  border-radius: 6px;
  overflow: hidden;
}

.breadcrumb-dropdown-item:hover {
  background-color: var(--surface-hover);
  color: var(--brand);
}

.breadcrumb-dropdown-item-label {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 768px) {
  .breadcrumb {
    font-size: 13px;
  }
  .breadcrumb-item {
    max-width: min(160px, 38vw);
  }
  .breadcrumb-item.active {
    max-width: min(220px, 52vw);
  }
}
</style>
