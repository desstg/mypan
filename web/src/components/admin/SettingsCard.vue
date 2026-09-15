<script setup lang="ts">
import "@/styles/settings-panel.css";

withDefaults(
  defineProps<{
    /** 省略标题时不渲染表头，交由外层（如 Tab 栏）标注分组。 */
    title?: string;
    accent?: string;
  }>(),
  { accent: "var(--brand)" },
);
</script>

<template>
  <section class="settings-card" :class="{ 'settings-card--titleless': !title }" :style="{ '--accent': accent }">
    <div v-if="title || $slots['head-aside'] || $slots['head-actions']" class="settings-card__head">
      <div v-if="title || $slots['head-aside']" class="settings-card__head-main">
        <h3 v-if="title" class="settings-card__title">{{ title }}</h3>
        <div v-if="$slots['head-aside']" class="settings-card__head-aside">
          <slot name="head-aside" />
        </div>
      </div>
      <div v-if="$slots['head-actions']" class="settings-card__head-actions">
        <slot name="head-actions" />
      </div>
    </div>
    <div class="settings-card__body">
      <slot />
    </div>
  </section>
</template>
