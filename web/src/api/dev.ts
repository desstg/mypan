import { http } from "./client";

export interface DevState {
  unlocked: boolean;
}

export const devApi = {
  state: () => http.get<DevState>("/admin/dev/state"),
  unlock: (code: string) => http.post<DevState>("/admin/dev/unlock", { code }),
};
