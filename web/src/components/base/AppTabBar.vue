<script setup lang="ts">
interface Tab {
  key: string;
  label: string;
  /** 该 Tab 内存在未保存改动时标一个小圆点。 */
  changed?: boolean;
}

defineProps<{ tabs: Tab[]; modelValue: string }>();
const emit = defineEmits<{ "update:modelValue": [string] }>();
</script>

<template>
  <div class="tabbar">
    <div class="tabbar__tabs">
      <button
        v-for="t in tabs"
        :key="t.key"
        type="button"
        class="tabbar__tab"
        :class="{ 'tabbar__tab--active': t.key === modelValue }"
        @click="emit('update:modelValue', t.key)"
      >
        {{ t.label }}
        <span v-if="t.changed" class="tabbar__tab-dot" aria-hidden="true" />
        <span v-if="t.changed" class="tabbar__sr-only">（有未保存改动）</span>
      </button>
    </div>
    <div class="tabbar__actions">
      <slot name="actions" />
    </div>
  </div>
</template>

<style scoped>
.tabbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  background: var(--surface);
  border-radius: var(--radius-md);
  padding: 8px 10px;
  box-shadow: var(--shadow-card);
  margin-bottom: 18px;
}
.tabbar__tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}
.tabbar__tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: none;
  background: transparent;
  color: var(--text-muted);
  font-size: 14px;
  font-weight: 600;
  padding: 8px 14px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: var(--transition);
}
.tabbar__tab-dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--warning);
}
.tabbar__sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  border: 0;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.tabbar__tab:hover {
  color: var(--text);
  background: var(--border-soft);
}
.tabbar__tab--active,
.tabbar__tab--active:hover {
  background: var(--brand);
  color: var(--text-on-brand);
}
.tabbar__actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
</style>
