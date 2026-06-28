import { useState } from "react";
import { Loader2, KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { setAuth } from "@/lib/auth";

export const LoginScreen = ({ onSuccess }: { onSuccess: () => void }) => {
    const [url, setUrl] = useState(window.location.origin);
    const [key, setKey] = useState("");
    const [busy, setBusy] = useState(false);
    const [err, setErr] = useState("");

    const submit = async () => {
        setBusy(true);
        setErr("");
        const base = url.replace(/\/+$/, "");
        try {
            const r = await fetch(`${base}/api/config`, { headers: { "X-API-Key": key } });
            if (!r.ok) {
                setErr(r.status === 401 ? "API key inválida" : `Erro ${r.status}`);
                return;
            }
            setAuth(base, key);
            onSuccess();
        } catch {
            setErr("Não foi possível conectar à URL informada");
        } finally {
            setBusy(false);
        }
    };

    return (
        <div className="flex min-h-screen items-center justify-center bg-background px-4">
            <div className="w-full max-w-sm space-y-5 rounded-xl border bg-card p-6 shadow-sm">
                <div className="flex items-center gap-2">
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <KeyRound className="h-4 w-4" />
          </span>
                    <div>
                        <h1 className="text-lg font-semibold tracking-tight">WaCalls</h1>
                        <p className="text-xs text-muted-foreground">Acesse com a URL e a API key</p>
                    </div>
                </div>
                <div className="space-y-3">
                    <div className="space-y-1">
                        <Label>URL</Label>
                        <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://call.seudominio.com" />
                    </div>
                    <div className="space-y-1">
                        <Label>API key</Label>
                        <Input
                            type="password"
                            value={key}
                            onChange={(e) => setKey(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === "Enter") submit();
                            }}
                            placeholder="sua chave"
                        />
                    </div>
                    {err && <p className="text-sm text-destructive">{err}</p>}
                    <Button className="w-full" disabled={busy || !key.trim()} onClick={submit}>
                        {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
                        Entrar
                    </Button>
                </div>
            </div>
        </div>
    );
};