import { apiGet, apiPost } from "@/lib/api";
import type { ProxyInfo, ProxyTestResult } from "@/types/proxy";

export type ProxySaveBody = {
  proxyEnabled: boolean;
  proxyType: string;
  proxyHost: string;
  proxyPort: number;
  proxyUsername: string;
  proxyPassword: string;
};

export const getProxy = (sid: string) =>
  apiGet<ProxyInfo>(`/api/sessions/${sid}/proxy`);

export const setProxy = (sid: string, cfg: ProxySaveBody) =>
  apiPost<{ ok: boolean }>(`/api/sessions/${sid}/proxy`, cfg);

export const testProxy = (sid: string, cfg: ProxySaveBody, timeoutMs?: number) =>
  apiPost<ProxyTestResult>(`/api/sessions/${sid}/proxy/test`, {
    ...cfg,
    timeoutMs: timeoutMs ?? 10000,
  });
