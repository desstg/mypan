<script setup lang="ts">
import AppButton from "@/components/base/AppButton.vue";

withDefaults(
  defineProps<{
    display?: string;
    title?: string;
    placeholder?: string;
    browseLabel?: string;
    wrapperClass?: string;
  }>(),
  {
    display: "",
    placeholder: "点击浏览选择账号及目录",
    browseLabel: "浏览",
  },
);

const emit = defineEmits<{ browse: [] }>();

function openBrowse() {
  emit("browse");
}
</script>

<template>
  <div class="account-folder-field" :class="wrapperClass">
    <button
      type="button"
      class="account-folder-field__main"
      :title="title || placeholder"
      @click="openBrowse"
    >
      <span
        class="account-folder-field__text"
        :class="{ 'account-folder-field__text--placeholder': !display }"
      >
        {{ display || placeholder }}
      </span>
    </button>
    <AppButton type="button" variant="primary" class="account-folder-field__browse" @click="openBrowse">
      {{ browseLabel }}
    </AppButton>
  </div>
</template>

<style scoped>
.account-folder-field {
  display: flex;
  align-items: stretch;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: var(--surface);
  transition: border-color 0.15s;
}

.account-folder-field:focus-within {
  border-color: var(--brand);
}

.account-folder-field__main {
  flex: 1;
  min-width: 0;
  margin: 0;
  padding: 9px 12px; /* 与 AppInput 盒模型一致，并排时高度相同 */
  border: none;
  background: transparent;
  text-align: left;
  cursor: pointer;
  color: var(--text);
  font-size: 13px;
  line-height: normal;
}

.account-folder-field__main:hover {
  background: color-mix(in srgb, var(--surface-sunken) 60%, transparent);
}

.account-folder-field__text {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account-folder-field__text--placeholder {
  color: var(--text-muted);
}

.account-folder-field__browse {
  flex-shrink: 0;
  min-width: 72px;
  height: auto;
  border-radius: 0 !important;
  border: none !important;
  border-left: 1px solid var(--border) !important;
  box-shadow: none !important;
}
</style>
