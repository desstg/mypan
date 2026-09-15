<script setup lang="ts">
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import "@/styles/settings-panel.css";

defineProps<{
  label: string;
  changed?: boolean;
  helpTitle?: string;
  helpText?: string;
}>();
</script>

<template>
  <div class="settings-row__label">
    <span>{{ label }}</span>
    <span v-if="changed" class="settings-row__badge">已修改</span>
    <SettingsHelpTooltip v-if="helpTitle && ($slots.help || helpText)" :title="helpTitle">
      <slot name="help">
        <p v-if="helpText">{{ helpText }}</p>
      </slot>
    </SettingsHelpTooltip>
    <slot v-else name="help" />
  </div>
</template>
