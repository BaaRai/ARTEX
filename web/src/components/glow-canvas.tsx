"use client";

// Drawing helpers and overlay pieces shared by the canvas views (Brain, Attack
// paths): theme colours read as RGB, cached glow sprites, and the small HUD
// controls that sit over the canvas.

import type * as React from "react";

import { Button } from "@/components/ui/button";
import { intlLocale } from "@/i18n";
import { cn } from "@/lib/utils";

export type RGB = [number, number, number];

export function clamp(v: number, lo: number, hi: number) {
  return v < lo ? lo : v > hi ? hi : v;
}

export function rgba([r, g, b]: RGB, a: number) {
  return `rgba(${r},${g},${b},${a})`;
}

export function rgbCss(c: RGB) {
  return `rgb(${c[0]},${c[1]},${c[2]})`;
}

/** Reads CSS colour variables off `probe` as RGB, keeping `fallback` for any it can't read. */
export function readCssColors<K extends string>(
  probe: HTMLElement,
  vars: Record<K, string>,
  fallback: Record<K, RGB>,
): Record<K, RGB> {
  const out = { ...fallback };
  const c = document.createElement("canvas");
  c.width = 1;
  c.height = 1;
  const ctx = c.getContext("2d", { willReadFrequently: true });
  if (!ctx) return out;
  const style = getComputedStyle(probe);
  for (const key of Object.keys(vars) as K[]) {
    const css = style.getPropertyValue(vars[key]).trim();
    if (!css) continue;
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = "#010203";
    ctx.fillStyle = css;
    if (ctx.fillStyle === "#010203") continue; // not a colour the canvas understands
    ctx.fillRect(0, 0, 1, 1);
    const d = ctx.getImageData(0, 0, 1, 1).data;
    out[key] = [d[0], d[1], d[2]];
  }
  return out;
}

/** A soft radial glow; `hot` gives the bright core drawn over it. */
export function glowSprite([r, g, b]: RGB, hot: boolean): HTMLCanvasElement {
  const size = 64;
  const c = document.createElement("canvas");
  c.width = size;
  c.height = size;
  const ctx = c.getContext("2d");
  if (!ctx) return c;
  const grad = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
  const mix = (v: number) => Math.round(v + (255 - v) * 0.7);
  if (hot) {
    grad.addColorStop(0, `rgba(${mix(r)},${mix(g)},${mix(b)},1)`);
    grad.addColorStop(0.35, `rgba(${r},${g},${b},0.9)`);
    grad.addColorStop(1, `rgba(${r},${g},${b},0)`);
  } else {
    grad.addColorStop(0, `rgba(${mix(r)},${mix(g)},${mix(b)},0.9)`);
    grad.addColorStop(0.1, `rgba(${r},${g},${b},0.75)`);
    grad.addColorStop(0.32, `rgba(${r},${g},${b},0.22)`);
    grad.addColorStop(0.6, `rgba(${r},${g},${b},0.06)`);
    grad.addColorStop(1, `rgba(${r},${g},${b},0)`);
  }
  ctx.fillStyle = grad;
  ctx.fillRect(0, 0, size, size);
  return c;
}

export function Stat({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <span>
      <span className={cn("font-medium text-white/90", tone)}>{value.toLocaleString(intlLocale)}</span> {label}
    </span>
  );
}

export function IconToggle({
  label,
  active,
  onClick,
  children,
}: {
  label: string;
  active?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Button
      type="button"
      size="icon"
      variant="ghost"
      title={label}
      aria-label={label}
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "size-8 rounded-md border border-white/10 bg-black/35 text-white/70 backdrop-blur hover:bg-white/10 hover:text-white [&_svg]:size-4",
        active && "border-white/25 bg-white/15 text-white",
      )}
    >
      {children}
    </Button>
  );
}

export function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-6 text-center text-sm text-white/60">
      {children}
    </div>
  );
}
