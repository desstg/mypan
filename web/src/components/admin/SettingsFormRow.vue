<script setup lang="ts">
import type { SettingItem } from "@/api/settings";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import SettingsRowLabel from "@/components/admin/SettingsRowLabel.vue";
import "@/styles/settings-panel.css";

const props = defineProps<{
  item: SettingItem;
  modelValue: string;
  changed?: boolean;
  label?: string;
  helpTitle?: string;
  helpText?: string;
}>();

const emit = defineEmits<{ "update:modelValue": [string] }>();

function displayLabel(): string {
  const base = props.label ?? props.item.label;
  if (props.item.type === "int" && props.item.unit) return `${base}（${props.item.unit}）`;
  return base;
}
</script>

<template>
  <SettingsRow :changed="changed" :show-changed-badge="false">
    <template #info>
      <SettingsRowLabel
        :label="displayLabel()"
        :changed="changed"
        :help-title="helpTitle ?? (item.description ? `${displayLabel()}说明` : undefined)"
        :help-text="helpText ?? item.description"
      >
        <template v-if="$slots.help" #help>
          <slot name="help" />
        </template>
      </SettingsRowLabel>
    </template>
    <template #control>
      <div class="settings-row__field">
        <SettingsBoolSegment
          v-if="item.type === 'bool'"
          :model-value="modelValue === 'true'"
          :label="displayLabel()"
          @update:model-value="emit('update:modelValue', $event ? 'true' : 'false')"
        />
        <div v-else-if="item.type === 'select'" class="field-select">
          <AppSelect
            :model-value="modelValue"
            :options="item.options || []"
            @update:model-value="emit('update:modelValue', String($event))"
          />
        </div>
        <div v-else-if="item.type === 'int'" class="field-num">
          <AppInput
            :model-value="modelValue"
            type="number"
            :placeholder="item.default"
            @update:model-value="emit('update:modelValue', String($event))"
          />
        </div>
        <div v-else class="field-text">
          <AppInput
            :model-value="modelValue"
            :placeholder="item.default"
            autocomplete="off"
            @update:model-value="emit('update:modelValue', String($event))"
          />
        </div>
      </div>
    </template>
  </SettingsRow>
</template>
