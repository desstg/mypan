<script setup lang="ts">
import { computed } from "vue";
import { getSvg } from "./svgRegistry";
import { getIconfontSymbolId } from "./iconfontSymbolMap";

const props = withDefaults(
  defineProps<{ name: string; size?: number | string; className?: string }>(),
  { size: 18, className: "" },
);

const dim = computed(() => {
  const n = Number(props.size);
  return Number.isFinite(n) && n > 0 ? n : 18;
});

const rootStyle = computed(() => ({
  width: `${dim.value}px`,
  height: `${dim.value}px`,
}));

const symbolId = computed(() => getIconfontSymbolId(props.name));
const symbolHash = computed(() => (symbolId.value ? `#${symbolId.value}` : ""));
const fallbackMarkup = computed(() => (symbolId.value ? "" : getSvg(props.name)));
</script>

<template>
  <span class="lp-svg-icon" :class="className" :style="rootStyle" aria-hidden="true">
    <svg
      v-if="symbolId"
      class="lp-iconfont-use"
      xmlns="http://www.w3.org/2000/svg"
      xmlns:xlink="http://www.w3.org/1999/xlink"
      width="100%"
      height="100%"
      focusable="false"
      preserveAspectRatio="xMidYMid meet"
    >
      <use :href="symbolHash" :xlink:href="symbolHash" />
    </svg>
    <span v-else class="lp-svg-fallback" v-html="fallbackMarkup" />
  </span>
</template>

<style scoped>
.lp-svg-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  vertical-align: middle;
  line-height: 0;
}
.lp-iconfont-use {
  display: block;
  overflow: visible;
}
.lp-svg-fallback :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
}
</style>
