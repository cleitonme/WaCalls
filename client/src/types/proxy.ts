export type ProxyType = "HTTP" | "HTTPS" | "SOCKS5";

// Configuração enviada ao salvar/testar. A senha fica em branco ao editar quando
// o operador não a altera (o backend preserva a atual nesse caso).
export type ProxyConfig = {
  proxyEnabled: boolean;
  proxyType: ProxyType;
  proxyHost: string;
  proxyPort: number;
  proxyUsername: string;
  proxyPassword: string;
};

// Forma mascarada retornada pelo backend (senha sempre "").
export type ProxyInfo = {
  proxyEnabled: boolean;
  proxyType: ProxyType;
  proxyHost: string;
  proxyPort: number;
  proxyUsername: string;
  proxyPassword: string;
  proxySet: boolean;
};

export type ProxyTestResult = {
  success: boolean;
  latencyMs: number;
  exitIP: string;
  errorCategory: string;
  message?: string;
};

const PROXY_ERROR_MESSAGES: Record<string, string> = {
  timeout: "Timeout — o proxy não respondeu no prazo.",
  auth: "Falha na autenticação — usuário ou senha incorretos.",
  host_invalid: "Host inválido — não foi possível resolver o endereço.",
  scheme_invalid: "Tipo de proxy inválido.",
  connection_failed: "Falha de conexão — proxy recusou ou sem rota.",
  http_error: "O proxy respondeu, mas com erro HTTP.",
  invalid: "Configuração de proxy inválida.",
  unknown: "Erro desconhecido ao testar o proxy.",
};

export const proxyErrorLabel = (category: string): string =>
  PROXY_ERROR_MESSAGES[category] ?? category ?? "Erro.";
