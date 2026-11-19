"use client";

import { useEffect, useState } from "react";

import { useRouter } from "next/navigation";

import { useTranslations } from "next-intl";

import { LanguageToggle } from "@/components/language-toggle";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function SetupPage() {
  const t = useTranslations("auth");
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  // when the initialization state cannot be fetched, do not default to "not initialized" — that would show the setup form to an
  // instance that already has a password, and following it would overwrite the original. Close the entrance instead.
  const [unavailable, setUnavailable] = useState(false);

  useEffect(() => {
    api
      .authStatus()
      .then(({ initialized }) => {
        if (initialized) router.replace("/login");
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : t("backendUnavailable"));
        setUnavailable(true);
      })
      .finally(() => setChecking(false));
  }, [router, t]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setError(t("setup.mismatch"));
      return;
    }
    if (password.length < 12) {
      setError(t("setup.tooShort"));
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.initPassword(password, setupToken.trim());
      auth.setToken(token);
      router.replace("/function/tasks");
    } catch (err) {
      setError(err instanceof Error ? err.message : t("setup.initFailed"));
    } finally {
      setLoading(false);
    }
  }

  if (checking) return null;

  return (
    <div className="relative flex h-dvh">
      <LanguageToggle className="absolute right-4 top-4 z-20 text-muted-foreground" />
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">{unavailable ? t("setup.cannotConfirm") : t("setup.title")}</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">
              {unavailable ? t("setup.unavailableDesc") : t("setup.desc")}
            </p>
          </div>
          {unavailable ? (
            <div className="flex flex-col gap-4">
              {error && <p className="text-center text-sm text-destructive">{error}</p>}
              <Button type="button" className="w-full" onClick={() => window.location.reload()}>
                {t("setup.retry")}
              </Button>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="flex flex-col gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="setup-token">{t("setup.token")}</Label>
                <Input
                  id="setup-token"
                  value={setupToken}
                  onChange={(e) => setSetupToken(e.target.value)}
                  placeholder={t("setup.tokenPlaceholder")}
                  autoFocus
                  autoComplete="off"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="password">{t("setup.newPassword")}</Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={t("setup.newPasswordPlaceholder")}
                  autoComplete="new-password"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="confirm">{t("setup.confirmPassword")}</Label>
                <Input
                  id="confirm"
                  type="password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  placeholder={t("setup.confirmPasswordPlaceholder")}
                  autoComplete="new-password"
                />
              </div>
              {error && <p className="text-sm text-destructive">{error}</p>}
              <Button
                type="submit"
                className="w-full"
                disabled={loading || !setupToken.trim() || !password || !confirm}
              >
                {loading ? t("setup.saving") : t("setup.submit")}
              </Button>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
