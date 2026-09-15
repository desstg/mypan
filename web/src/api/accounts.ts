import { http } from "./client";
import type { Account, AccountPayload } from "./types";

export const accountsApi = {
  list: () => http.get<Account[]>("/admin/accounts"),
  get: (id: number) => http.get<Account>(`/admin/accounts/${id}`),
  create: (payload: AccountPayload) => http.post<Account>("/admin/accounts", payload),
  update: (id: number, payload: AccountPayload) => http.put<Account>(`/admin/accounts/${id}`, payload),
  remove: (id: number) => http.del<{ id: number }>(`/admin/accounts/${id}`),
  toggle: (id: number) => http.post<Account>(`/admin/accounts/${id}/toggle`),
  setDefault: (id: number) => http.post<Account>(`/admin/accounts/${id}/set-default`),
  refreshProfile: (id: number) => http.post(`/admin/accounts/${id}/refresh-profile`),
};
