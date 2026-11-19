"use client";

import * as React from "react";

import { CheckIcon, CopyIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { cn, copyText } from "@/lib/utils";

type CopyButtonProps = {
  // text to copy; empty disables the button.
  text: string | null | undefined;
  // toast message shown after a successful copy, defaults to "Copied".
  successMessage?: string;
  label?: React.ReactNode;
  size?: React.ComponentProps<typeof Button>["size"];
  variant?: React.ComponentProps<typeof Button>["variant"];
  className?: string;
};

// CopyButton is the shared "copy to clipboard" button: built-in success/failure feedback, and in non-secure HTTP
// contexts it degrades automatically (see copyText).
export function CopyButton({
  text,
  successMessage,
  label,
  size = "sm",
  variant = "outline",
  className,
}: CopyButtonProps) {
  const t = useTranslations("copyButton");
  const [copied, setCopied] = React.useState(false);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  React.useEffect(() => {
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);

  async function handleCopy() {
    if (!text) return;
    const ok = await copyText(text);
    if (ok) {
      setCopied(true);
      toast.success(successMessage ?? t("toasts.copied"));
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error(t("toasts.copyFailed"));
    }
  }

  return (
    <Button type="button" size={size} variant={variant} className={cn(className)} disabled={!text} onClick={handleCopy}>
      {copied ? <CheckIcon /> : <CopyIcon />}
      {label ?? t("copy")}
    </Button>
  );
}
