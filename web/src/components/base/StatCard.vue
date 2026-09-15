<script setup lang="ts">
import { computed } from "vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";

const props = withDefaults(
  defineProps<{
    value: string | number;
    label: string;
    icon: string;
    tone?: "blue" | "red" | "purple" | "amber";
    sideActions?: boolean;
  }>(),
  { tone: "blue", sideActions: false },
);

const isSvgIcon = computed(() => /^[a-z0-9-]+$/i.test(props.icon));
</script>

<template>
  <div
    class="stat-card"
    :class="{
      'stat-card--with-actions': Boolean($slots.actions),
      'stat-card--side-actions': sideActions && Boolean($slots.actions),
    }"
  >
    <div class="stat-card__icon" :class="`stat-card__icon--${tone}`">
      <SvgIcon v-if="isSvgIcon" :name="icon" :size="20" />
      <span v-else>{{ icon }}</span>
    </div>
    <div class="stat-card__main">
      <div class="stat-card__value">{{ value }}</div>
      <div class="stat-card__footer">
        <div class="stat-card__label">{{ label }}</div>
        <div v-if="$slots.actions" class="stat-card__actions">
          <slot name="actions" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stat-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 18px 20px;
  border-radius: var(--radius-xl);
  background: var(--surface);
  box-shadow: var(--shadow-card);
}
.stat-card__icon {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 14px;
  color: #fff;
  font-size: 18px;
  flex-shrink: 0;
  overflow: hidden;
}
.stat-card__icon--blue {
  background: linear-gradient(135deg, var(--brand-start), var(--brand-end));
}
.stat-card__icon--red {
  background: linear-gradient(135deg, #ef4444, #f97316);
}
.stat-card__icon--purple {
  background: linear-gradient(135deg, #7c3aed, #4f46e5);
}
.stat-card__icon--amber {
  background: linear-gradient(135deg, #f59e0b, #f97316);
}
.stat-card__main {
  min-width: 0;
  flex: 1;
}
.stat-card__value {
  font-size: 24px;
  font-weight: 700;
  line-height: 1.1;
  color: var(--text);
  white-space: nowrap;
}
.stat-card--with-actions .stat-card__value {
  font-size: 20px;
}
.stat-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 6px;
}
.stat-card__label {
  min-width: 0;
  font-size: 13px;
  color: var(--text-muted);
}
.stat-card__actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.stat-card--side-actions .stat-card__main {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  grid-template-rows: auto auto;
  column-gap: 8px;
  align-items: center;
}

.stat-card--side-actions .stat-card__value {
  grid-column: 1;
  grid-row: 1;
}

.stat-card--side-actions .stat-card__footer {
  display: contents;
}

.stat-card--side-actions .stat-card__label {
  grid-column: 1;
  grid-row: 2;
  margin-top: 6px;
}

.stat-card--side-actions .stat-card__actions {
  grid-column: 2;
  grid-row: 1 / span 2;
  align-self: center;
}
</style>
