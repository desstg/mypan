<script setup lang="ts">
import type { FileItem } from "@/api/types";
import { fileKind } from "@/utils/fileIcon";
import SvgIcon from "@/components/icons/SvgIcon.vue";

const props = withDefaults(defineProps<{ file: FileItem; size?: number }>(), { size: 18 });
</script>

<template>
  <SvgIcon :name="fileKind(props.file)" :size="size" class="file-icon" />
</template>
