import { useEffect, useState } from "react";
import { PhoneCall } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Dialer } from "@/components/domain/call/Dialer";
import { CallCard } from "@/components/domain/call/CallCard";
import { OtherCallsList } from "@/components/domain/call/OtherCallsList";
import { HistoryDrawer } from "@/components/domain/history/HistoryDrawer";
import { EmptyState } from "@/components/shared/EmptyState";
import { isMine, useCalls } from "@/stores/calls";
import { apiGet } from "@/lib/api";

type SessionCallsInfo = {
    active: number;
    maxCallsPerSession: number;
};

export const CallsPage = ({ sid }: { sid: string }) => {
    const calls = useCalls((s) => s.calls);
    const [, force] = useState(0);

    useEffect(() => {
        const t = setInterval(() => force((n) => n + 1), 1000);
        return () => clearInterval(t);
    }, []);

    // Fonte de verdade do servidor para contagem real de calls ativas
    const { data: callsInfo } = useQuery<SessionCallsInfo>({
        queryKey: ["session-calls-info", sid],
        queryFn: () => apiGet<SessionCallsInfo>(`/api/sessions/${sid}/calls`),
        refetchInterval: 5_000,
        staleTime: 4_000,
    });

    const sessionCalls = calls.filter((c) => c.sessionId === sid && c.status !== "ended");
    const mine = sessionCalls.filter(isMine);
    const others = sessionCalls.filter((c) => !isMine(c));

    // Usa o valor do servidor quando disponível, fallback para contagem local
    const activeCount = callsInfo?.active ?? mine.length;

    return (
        <div className="mx-auto max-w-3xl space-y-6">
            <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                    <h2 className="text-sm font-medium text-muted-foreground">
                        {activeCount} active call{activeCount === 1 ? "" : "s"}
                    </h2>
                    <button
                        onClick={() => navigator.clipboard.writeText(sid)}
                        className="font-mono text-xs text-muted-foreground/60 hover:text-muted-foreground transition-colors"
                        title="Copy session ID"
                    >
                        {sid}
                    </button>
                </div>
                <HistoryDrawer sid={sid} />
            </div>
            <Dialer sid={sid} />
            {mine.length > 0 ? (
                <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
                    {mine.map((c) => (
                        <CallCard key={c.callId} call={c} />
                    ))}
                </div>
            ) : (
                <EmptyState
                    icon={<PhoneCall className="h-6 w-6" />}
                    title="No active calls"
                    description="Dial a number above to start a call."
                />
            )}
            <OtherCallsList calls={others} />
        </div>
    );
};