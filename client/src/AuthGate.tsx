import { useEffect, useState } from "react";
import { App } from "./App";
import { LoginScreen } from "./components/auth/LoginScreen";
import { checkAuth } from "@/lib/auth";

export const AuthGate = () => {
    const [state, setState] = useState<"checking" | "login" | "ok">("checking");

    useEffect(() => {
        checkAuth().then((ok) => setState(ok ? "ok" : "login"));
    }, []);

    if (state === "checking") return null;
    if (state === "login") return <LoginScreen onSuccess={() => setState("ok")} />;
    return <App />;
};