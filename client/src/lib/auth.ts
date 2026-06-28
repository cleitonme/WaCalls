const URL_KEY = "wacalls.apiUrl";
const KEY_KEY = "wacalls.apiKey";

export const getApiBase = (): string => (localStorage.getItem(URL_KEY) || "").replace(/\/+$/, "");
export const getApiKey = (): string => localStorage.getItem(KEY_KEY) || "";
export const isAuthed = (): boolean => !!getApiKey();

export const setAuth = (url: string, key: string): void => {
  localStorage.setItem(URL_KEY, url.replace(/\/+$/, ""));
  localStorage.setItem(KEY_KEY, key);
};

export const clearAuth = (): void => {
  localStorage.removeItem(KEY_KEY);
};

export const apiUrl = (path: string): string => getApiBase() + path;

// checkAuth verifica se o backend aceita (servidor aberto -> 200 mesmo sem key).
export const checkAuth = async (): Promise<boolean> => {
  try {
    const r = await fetch(apiUrl("/api/config"), { headers: { "X-API-Key": getApiKey() } });
    return r.ok;
  } catch {
    return false;
  }
};