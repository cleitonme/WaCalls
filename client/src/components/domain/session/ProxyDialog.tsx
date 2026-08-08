import { useEffect, useState } from "react";
import { Loader2, Network, Plug, ShieldCheck } from "lucide-react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { getProxy, setProxy, testProxy, type ProxySaveBody } from "@/services/proxy";
import { proxyErrorLabel } from "@/types/proxy";
import type { ProxyInfo, ProxyType } from "@/types/proxy";

type Props = {
  sid: string;
  proxy?: ProxyInfo;
};

const TYPES: ProxyType[] = ["HTTP", "HTTPS", "SOCKS5"];

export const ProxyDialog = ({ sid, proxy }: Props) => {
  const [open, setOpen] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [type, setType] = useState<ProxyType>("HTTP");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [passwordSet, setPasswordSet] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  // Ao abrir, carrega a config atual (mascarada) para popular o form. A senha
  // nunca vem do backend; mostramos apenas se há uma guardada (passwordSet).
  useEffect(() => {
    if (!open) return;
    const base: Partial<ProxyInfo> = proxy ?? {};
    setEnabled(!!base.proxyEnabled);
    setType((base.proxyType as ProxyType) || "HTTP");
    setHost(base.proxyHost ?? "");
    setPort(base.proxyPort ? String(base.proxyPort) : "");
    setUsername(base.proxyUsername ?? "");
    setPassword("");
    setPasswordSet(!!base.proxySet);

    // Reforça buscando do endpoint (mais autoritativo que o snapshot da sessão).
    getProxy(sid)
      .then((info) => {
        setEnabled(!!info.proxyEnabled);
        if (info.proxyType) setType(info.proxyType as ProxyType);
        setHost(info.proxyHost ?? "");
        setPort(info.proxyPort ? String(info.proxyPort) : "");
        setUsername(info.proxyUsername ?? "");
        setPassword(info.proxyPassword ?? "");
        setPasswordSet(!!info.proxySet);
      })
      .catch(() => {});
  }, [open, sid, proxy]);

  const buildBody = (): ProxySaveBody => ({
    proxyEnabled: enabled,
    proxyType: type,
    proxyHost: host.trim(),
    proxyPort: Number(port) || 0,
    proxyUsername: username.trim(),
    proxyPassword: password, // vazio => backend preserva a atual
  });

  const onTest = async () => {
    if (enabled && !host.trim()) {
      toast.error("Informe o host do proxy.");
      return;
    }
    setTesting(true);
    try {
      const res = await testProxy(sid, buildBody(), 10000);
      if (res.success) {
        toast.success(`Conectado com sucesso. Latência ${res.latencyMs} ms${res.exitIP ? ` · IP ${res.exitIP}` : ""}.`);
      } else {
        toast.error(proxyErrorLabel(res.errorCategory));
      }
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setTesting(false);
    }
  };

  const onSave = async () => {
    setSaving(true);
    try {
      await setProxy(sid, buildBody());
      toast.success("Configuração de proxy salva. A sessão será reconectada.");
      setOpen(false);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Network className="h-4 w-4" />
          Proxy
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldCheck className="h-5 w-5" /> Proxy do canal
          </DialogTitle>
          <DialogDescription>
            Roteia as conexões do WhatsApp deste canal por um proxy HTTP, HTTPS ou SOCKS5.
            Salvar reconecta a sessão — encerre chamadas ativas antes.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <label className="flex cursor-pointer items-center gap-2.5 rounded-lg border p-3 text-sm font-medium transition-colors hover:bg-accent">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4 rounded border-input accent-primary"
            />
            Ativar Proxy
            {proxy?.proxyEnabled && (
              <Badge variant="success" className="ml-auto">ativo</Badge>
            )}
          </label>

          <fieldset disabled={!enabled} className={cn("space-y-4 transition-opacity", !enabled && "pointer-events-none opacity-50")}>
            <div className="space-y-2">
              <Label>Tipo</Label>
              <div className="grid grid-cols-3 gap-2">
                {TYPES.map((t) => (
                  <button
                    key={t}
                    type="button"
                    onClick={() => setType(t)}
                    className={cn(
                      buttonVariants({ variant: type === t ? "default" : "outline", size: "sm" }),
                      "w-full",
                    )}
                  >
                    {t}
                  </button>
                ))}
              </div>
            </div>

            <div className="grid grid-cols-3 gap-3">
              <div className="col-span-2 space-y-2">
                <Label htmlFor="proxy-host">Host</Label>
                <Input
                  id="proxy-host"
                  value={host}
                  onChange={(e) => setHost(e.target.value)}
                  placeholder="proxy.meudominio.com"
                  autoComplete="off"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="proxy-port">Porta</Label>
                <Input
                  id="proxy-port"
                  type="number"
                  value={port}
                  onChange={(e) => setPort(e.target.value)}
                  placeholder="1080"
                  autoComplete="off"
                />
              </div>
            </div>

            <div className="space-y-2">
              <Label htmlFor="proxy-user">Usuário (opcional)</Label>
              <Input
                id="proxy-user"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="usuario"
                autoComplete="off"
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="proxy-pass">Senha (opcional)</Label>
              <Input
                id="proxy-pass"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={passwordSet ? "•••• (deixe em branco para manter)" : "senha"}
                autoComplete="new-password"
              />
              {passwordSet && password === "" && (
                <p className="text-xs text-muted-foreground">
                  Senha guardada. Deixe em branco para preservá-la.
                </p>
              )}
            </div>
          </fieldset>
        </div>

        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="outline" onClick={onTest} disabled={testing || saving}>
            {testing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plug className="h-4 w-4" />}
            Testar Proxy
          </Button>
          <Button onClick={onSave} disabled={saving || testing}>
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
            Salvar
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};
