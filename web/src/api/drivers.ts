import { http } from "./client";
import type { DriverInfo } from "./types";

export const driversApi = {
  list: () => http.get<DriverInfo[]>("/admin/drivers"),
};
