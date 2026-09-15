import { ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";

type RunLoadOptions = {
  silent?: boolean;
};

export function useSettingsLoad(initialLoading = true) {
  const loading = ref(initialLoading);
  const loaded = ref(false);

  async function runLoad<T>(
    fn: () => Promise<T>,
    errorMessage = "加载设置失败",
    options?: RunLoadOptions,
  ): Promise<T | undefined> {
    const silent = options?.silent === true;
    if (!silent) loading.value = true;
    try {
      const result = await fn();
      loaded.value = true;
      return result;
    } catch (e) {
      toast.error(getApiErrorMessage(e, errorMessage));
      return undefined;
    } finally {
      if (!silent) loading.value = false;
    }
  }

  return { loading, loaded, runLoad };
}
