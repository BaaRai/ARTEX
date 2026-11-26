"use client";

// Attack paths of one task, drawn as a glowing 3D network. Everything comes
// from data the task already recorded: the asset coverage graph, the task's
// findings and the assets they were anchored to, and the exploration graph
// that shows which finding led the agent to the next. The entry point sits at
// the left, each layer of assets fans out as a ring along the path axis, and
// the riskiest chain is lit with traffic flowing along it. The model lives in
// lib/attack-path.ts; this file draws it and handles the camera and overlay.

import * as React from "react";

import { Crosshair, Eye, EyeOff, Maximize2, Minimize2, Pause, Play, RotateCw } from "lucide-react";

import {
  Centered,
  clamp,
  glowSprite,
  IconToggle,
  type RGB,
  readCssColors,
  rgba,
  rgbCss,
  Stat,
} from "@/components/glow-canvas";
import { intlLocale, t } from "@/i18n";
import { api } from "@/lib/api";
import {
  type AttackChain,
  type AttackGraph,
  buildAttackGraph,
  chainTo,
  type FindingIn,
  layoutAttackGraph,
  type PathKind,
  type PathNode,
  type Severity,
} from "@/lib/attack-path";
import { type Camera, type Projected, project } from "@/lib/brain-sim";
import { setVisibleInterval } from "@/lib/polling";
import { statusMeta } from "@/lib/status";
import type { CoverageGraphData, Edge, Finding, TaskNode } from "@/lib/types";
import { cn } from "@/lib/utils";

type ColorKey = PathKind | Severity | "closed" | "bg";

const KIND_VAR: Partial<Record<ColorKey, string>> = {
  entry: "--primary",
  company: "--chart-1",
  root_domain: "--chart-1",
  subdomain: "--chart-2",
  ip: "--chart-2",
  app: "--chart-2",
  service: "--chart-5",
  endpoint: "--chart-3",
  bg: "--background",
};

// Severity hues match the app's severity badges (rose, orange, amber, slate).
const FALLBACK: Record<ColorKey, RGB> = {
  entry: [176, 140, 255],
  company: [176, 140, 255],
  root_domain: [176, 140, 255],
  subdomain: [96, 205, 230],
  ip: [96, 205, 230],
  app: [96, 205, 230],
  service: [110, 220, 160],
  endpoint: [250, 190, 90],
  asset: [160, 160, 190],
  finding: [244, 63, 94],
  critical: [244, 63, 94],
  high: [249, 115, 22],
  medium: [245, 158, 11],
  low: [148, 163, 184],
  closed: [100, 116, 139],
  bg: [16, 13, 28],
};

const BASE_SIZE: Record<PathKind, number> = {
  entry: 20,
  company: 13,
  root_domain: 12,
  subdomain: 9,
  ip: 9,
  app: 9,
  service: 7.5,
  endpoint: 6,
  asset: 7,
  finding: 9,
};

const KIND_LABEL: Record<PathKind, string> = {
  entry: t("Entry point"),
  company: t("Company"),
  root_domain: t("Root domain"),
  subdomain: t("Subdomain"),
  ip: t("IP"),
  app: t("App"),
  service: t("Service"),
  endpoint: t("Endpoint"),
  asset: t("Asset"),
  finding: t("Finding"),
};

const SEVERITY_LABEL: Record<Severity, string> = {
  critical: t("Critical"),
  high: t("High"),
  medium: t("Medium"),
  low: t("Low"),
};

const SEVERITIES: Severity[] = ["critical", "high", "medium", "low"];
const ASSET_LEGEND: PathKind[] = [
  "entry",
  "company",
  "root_domain",
  "subdomain",
  "ip",
  "app",
  "service",
  "endpoint",
  "asset",
];
const REFRESH_MS = 15000;

interface Packet {
  hops: PathNode[];
  /** Distance travelled along the hops. */
  at: number;
  speed: number;
  tone: ColorKey;
  size: number;
  loop: boolean;
}

interface Shock {
  n: PathNode;
  age: number;
  tone: ColorKey;
}

interface HoverInfo {
  x: number;
  y: number;
  kind: PathKind;
  label: string;
  severity: Severity | "";
}

interface NodeInfo {
  id: string;
  kind: PathKind;
  label: string;
  inScope: boolean;
  tested: boolean;
  severity: Severity | "";
  status: string;
  verification: string;
  summary: string;
  risk: number;
  findingCount: number;
  worst: Severity | "";
  subtreeRisk: number;
  top: Array<{ id: string; label: string; severity: Severity | "" }>;
}

interface Stats {
  assets: number;
  findings: number;
  closed: number;
  chains: number;
  critical: number;
  high: number;
  dropped: number;
  /** Asset kinds on screen, so the legend names only what is drawn. */
  kinds: PathKind[];
}

function readPalette(probe: HTMLElement): Record<ColorKey, RGB> {
  const vars = {} as Record<ColorKey, string>;
  for (const [k, v] of Object.entries(KIND_VAR)) vars[k as ColorKey] = v as string;
  const out = readCssColors(probe, vars, FALLBACK);
  const [r, g, b] = out.bg;
  out.bg = [Math.round(r * 0.55), Math.round(g * 0.55), Math.round(b * 0.62)];
  return out;
}

function findingIn(f: Finding): FindingIn {
  return {
    id: f.id,
    name: f.name,
    vulnclass: f.vulnclass,
    severity: f.severity,
    status: f.status,
    verification_status: f.verification_status,
    summary: f.summary,
    assets: f.assets,
    inherited: f.inherited,
  };
}

function shortLabel(n: PathNode): string {
  if (n.kind === "entry") return KIND_LABEL.entry;
  const s = n.label.replace(/\s+/g, " ").trim();
  return s.length > 46 ? `${s.slice(0, 45)}…` : s;
}

function infoFor(n: PathNode): NodeInfo {
  const top = n.kind === "finding" ? [] : collectFindings(n).slice(0, 5);
  return {
    id: n.id,
    kind: n.kind,
    label: n.label,
    inScope: n.inScope,
    tested: n.tested,
    severity: n.severity,
    status: n.status,
    verification: n.verification,
    summary: n.summary,
    risk: n.risk,
    findingCount: n.findingCount,
    worst: n.worst,
    subtreeRisk: Math.round(n.subtreeRisk * 10) / 10,
    top: top.map((f) => ({ id: f.id, label: shortLabel(f), severity: f.severity })),
  };
}

/** Live findings in an asset's subtree, riskiest first. */
function collectFindings(n: PathNode): PathNode[] {
  const out: PathNode[] = [];
  const walk = (a: PathNode, guard: number) => {
    for (const f of a.findings) if (f.risk > 0) out.push(f);
    if (guard < 64) for (const c of a.children) walk(c, guard + 1);
  };
  walk(n, 0);
  return out.sort((a, b) => b.risk - a.risk || (a.id < b.id ? -1 : 1));
}

function chainLength(hops: PathNode[]): number[] {
  const acc = [0];
  for (let i = 1; i < hops.length; i++) {
    const a = hops[i - 1];
    const b = hops[i];
    acc.push(acc[i - 1] + Math.max(1, Math.hypot(b.x - a.x, b.y - a.y, b.z - a.z)));
  }
  return acc;
}

function pathToFinding(g: AttackGraph, f: PathNode): PathNode[] {
  const lead: PathNode[] = [];
  for (let a: PathNode | null = f.asset; a && a !== g.entry; a = a.parent) lead.unshift(a);
  return [g.entry, ...lead, f];
}

class AttackRenderer {
  graph: AttackGraph | null = null;
  readonly cam: Camera = { yaw: 0.42, pitch: -0.24, dist: 1100, tx: 0, ty: 0, tz: 0 };
  goal = { tx: 0, ty: 0, tz: 0, dist: 1100 };
  sway = true;
  paused = false;
  showClosed = true;
  chain: AttackChain | null = null;
  selected: PathNode | null = null;
  hovered: PathNode | null = null;
  onSelect: (n: PathNode | null) => void = () => undefined;
  onHover: (h: HoverInfo | null) => void = () => undefined;

  private ctx: CanvasRenderingContext2D;
  private w = 1;
  private h = 1;
  private inset = 0;
  private dpr = 1;
  private time = 0;
  private palette: Record<ColorKey, RGB> = FALLBACK;
  private sprites = new Map<string, HTMLCanvasElement>();
  /** Drawn position of each node, eased toward its layout position. */
  private pos = new Map<string, [number, number, number]>();
  private proj = new Map<PathNode, Projected>();
  private packets: Packet[] = [];
  private shocks: Shock[] = [];
  private emitAt = new Map<PathNode, number>();
  private chainHops: PathNode[] = [];
  private chainSet = new Set<PathNode>();
  private chainEdges = new Set<string>();
  private raf = 0;
  private last = 0;
  private lastInteract = -1e9;
  private swayBase = 0.42;
  private font = "ui-sans-serif, system-ui, sans-serif";
  private drag: { x: number; y: number; px: number; py: number; pan: boolean; moved: boolean } | null = null;
  private readonly cleanups: Array<() => void> = [];

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly reducedMotion: boolean,
  ) {
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("2D canvas unavailable");
    this.ctx = ctx;
    this.sway = !reducedMotion;
    this.font = getComputedStyle(document.body).fontFamily || this.font;
    this.bindInput();
  }

  setPalette(p: Record<ColorKey, RGB>) {
    this.palette = p;
    this.sprites.clear();
  }

  resize(w: number, h: number) {
    this.dpr = Math.min(2, window.devicePixelRatio || 1);
    this.w = Math.max(1, w);
    this.h = Math.max(1, h);
    this.inset = this.w >= 768 ? 300 : 0;
    this.canvas.width = Math.round(this.w * this.dpr);
    this.canvas.height = Math.round(this.h * this.dpr);
    this.canvas.style.width = `${this.w}px`;
    this.canvas.style.height = `${this.h}px`;
  }

  start() {
    const loop = (now: number) => {
      const dt = this.last ? (now - this.last) / 1000 : 0.016;
      this.last = now;
      this.frame(Math.min(dt, 0.05));
      this.raf = requestAnimationFrame(loop);
    };
    this.raf = requestAnimationFrame(loop);
  }

  destroy() {
    cancelAnimationFrame(this.raf);
    for (const c of this.cleanups) c();
  }

  /** Swap in a freshly built graph, easing nodes from where they were drawn. */
  setGraph(g: AttackGraph, fresh: Set<string>) {
    const first = !this.graph;
    this.graph = g;
    const nextPos = new Map<string, [number, number, number]>();
    for (const n of g.nodes.values()) {
      const old = this.pos.get(n.id);
      if (old) nextPos.set(n.id, old);
      else {
        // New nodes grow out of their parent or asset.
        const from = (n.kind === "finding" ? n.asset : n.parent) ?? g.entry;
        const fp = this.pos.get(from.id);
        nextPos.set(n.id, first ? [n.x, n.y, n.z] : fp ? [...fp] : [from.x, from.y, from.z]);
      }
    }
    this.pos = nextPos;
    this.proj.clear();
    this.packets = this.packets.filter((p) => p.hops.every((h) => g.nodes.get(h.id) === h));
    this.shocks = [];
    for (const id of fresh) {
      const n = g.nodes.get(id);
      if (!n || n.kind !== "finding") continue;
      this.shocks.push({ n, age: 0, tone: n.risk > 0 ? (n.severity as Severity) : "closed" });
      this.packets.push({
        hops: pathToFinding(g, n),
        at: 0,
        speed: 320,
        tone: n.risk > 0 ? (n.severity as Severity) : "closed",
        size: 1.4,
        loop: false,
      });
    }
    if (this.selected) this.selected = g.nodes.get(this.selected.id) ?? null;
  }

  setChain(c: AttackChain | null) {
    this.chain = c;
    this.chainHops = c?.hops ?? [];
    this.chainSet = new Set(this.chainHops);
    this.chainEdges = new Set();
    for (let i = 1; i < this.chainHops.length; i++)
      this.chainEdges.add(`${this.chainHops[i - 1].id}>${this.chainHops[i].id}`);
    this.packets = this.packets.filter((p) => !p.loop);
    if (!c) return;
    const lengths = chainLength(c.hops);
    const total = lengths[lengths.length - 1];
    const count = this.reducedMotion ? 2 : clamp(Math.round(total / 120), 4, 14);
    for (let i = 0; i < count; i++) {
      this.packets.push({
        hops: c.hops,
        at: (total * i) / count,
        speed: 190,
        tone: c.worst,
        size: 1.15,
        loop: true,
      });
    }
  }

  fit(animate = true) {
    if (!this.graph) return;
    this.fitNodes([...this.graph.nodes.values()], animate, 0.92);
  }

  fitChain(c: AttackChain) {
    this.fitNodes(c.hops, true, 1);
  }

  private fitNodes(list: PathNode[], animate: boolean, pct: number) {
    if (!list.length) return;
    let cx = 0;
    let cy = 0;
    let cz = 0;
    for (const n of list) {
      cx += n.x;
      cy += n.y;
      cz += n.z;
    }
    cx /= list.length;
    cy /= list.length;
    cz /= list.length;
    const xs = list.map((n) => Math.abs(n.x - cx)).sort((a, b) => a - b);
    const rs = list.map((n) => Math.hypot(n.y - cy, n.z - cz)).sort((a, b) => a - b);
    // The whole length of the path axis, so chained findings at the far end stay in view.
    const hx = Math.max(80, xs[xs.length - 1] ?? 0) + 40;
    const hr = Math.max(60, rs[Math.min(rs.length - 1, Math.floor(rs.length * pct))] ?? 0) + 30;
    const w = this.w - this.inset;
    const focal = Math.min(w, this.h) * 0.9;
    const dist = Math.max((hx * focal) / (w * 0.46), (hr * focal) / (this.h * 0.44)) + hr * 0.3;
    this.goal = { tx: cx, ty: cy, tz: cz, dist: clamp(dist, 140, 9000) };
    if (!animate) Object.assign(this.cam, this.goal);
  }

  select(n: PathNode | null) {
    this.selected = n;
    if (n && this.graph) {
      this.goal.tx = n.x;
      this.goal.ty = n.y;
      this.goal.tz = n.z;
      this.goal.dist = Math.min(this.cam.dist, n.kind === "entry" ? 520 : 300);
    }
    this.onSelect(n);
  }

  private color(key: ColorKey): RGB {
    return this.palette[key] ?? FALLBACK[key];
  }

  private sprite(key: ColorKey, hot = false): HTMLCanvasElement {
    const id = hot ? `${key}:hot` : key;
    let s = this.sprites.get(id);
    if (!s) {
      s = glowSprite(this.color(key), hot);
      this.sprites.set(id, s);
    }
    return s;
  }

  private nodeColor(n: PathNode): ColorKey {
    if (n.kind === "finding") return n.risk > 0 ? (n.severity as Severity) || "low" : "closed";
    return n.kind;
  }

  private visible(n: PathNode): boolean {
    return this.showClosed || n.kind !== "finding" || n.risk > 0;
  }

  private at(n: PathNode): [number, number, number] {
    return this.pos.get(n.id) ?? [n.x, n.y, n.z];
  }

  private frame(dt: number) {
    const g = this.graph;
    const { cam, goal } = this;
    if (!this.paused) {
      this.time += dt;
      this.step(dt);
    }
    if (this.sway && !this.drag && this.time - this.lastInteract > 4) {
      cam.yaw += (this.swayBase + Math.sin(this.time * 0.18) * 0.38 - cam.yaw) * (1 - Math.exp(-dt * 0.6));
    }
    const k = 1 - Math.exp(-dt * 3.2);
    cam.tx += (goal.tx - cam.tx) * k;
    cam.ty += (goal.ty - cam.ty) * k;
    cam.tz += (goal.tz - cam.tz) * k;
    cam.dist += (goal.dist - cam.dist) * k;
    if (g) {
      const e = 1 - Math.exp(-dt * 4);
      for (const n of g.nodes.values()) {
        const p = this.pos.get(n.id);
        if (!p) continue;
        p[0] += (n.x - p[0]) * e;
        p[1] += (n.y - p[1]) * e;
        p[2] += (n.z - p[2]) * e;
      }
    }
    this.draw();
  }

  private step(dt: number) {
    const g = this.graph;
    if (!g) return;
    // Background traffic: every open finding sends packets down its own path,
    // more often the riskier it is.
    if (!this.reducedMotion && this.packets.length < 320) {
      for (const f of g.findings) {
        if (f.risk <= 0) continue;
        const next = this.emitAt.get(f) ?? this.time + ((Number.parseInt(f.findingId, 10) || 0) % 7) * 0.4;
        if (this.time >= next) {
          this.packets.push({
            hops: pathToFinding(g, f),
            at: 0,
            speed: 150 + f.risk * 8,
            tone: f.severity as Severity,
            size: 0.7,
            loop: false,
          });
          this.emitAt.set(f, this.time + clamp(9 / Math.max(0.5, f.risk), 0.9, 12));
        } else if (!this.emitAt.has(f)) this.emitAt.set(f, next);
      }
    }
    const keep: Packet[] = [];
    for (const p of this.packets) {
      p.at += p.speed * dt;
      const total = chainLength(p.hops).at(-1) ?? 0;
      if (p.at >= total) {
        if (p.loop) p.at -= total;
        else {
          const end = p.hops[p.hops.length - 1];
          if (end.kind === "finding" && p.size >= 1) this.shocks.push({ n: end, age: 0, tone: p.tone });
          continue;
        }
      }
      keep.push(p);
    }
    this.packets = keep;
    for (const s of this.shocks) s.age += dt;
    this.shocks = this.shocks.filter((s) => s.age < 1.6);
  }

  private fog(p: Projected): number {
    return clamp(1.3 - (p.depth / Math.max(1, this.cam.dist)) * 0.6, 0.12, 1);
  }

  private point(hops: PathNode[], at: number, out: Projected, w: number): boolean {
    const lengths = chainLength(hops);
    let i = 1;
    while (i < lengths.length - 1 && lengths[i] < at) i++;
    const a = this.at(hops[i - 1]);
    const b = this.at(hops[i]);
    const seg = lengths[i] - lengths[i - 1] || 1;
    const u = clamp((at - lengths[i - 1]) / seg, 0, 1);
    project(this.cam, a[0] + (b[0] - a[0]) * u, a[1] + (b[1] - a[1]) * u, a[2] + (b[2] - a[2]) * u, w, this.h, out);
    return out.scale > 0;
  }

  private curve(a: Projected, b: Projected, bend: number) {
    const mx = (a.sx + b.sx) / 2 + (b.sy - a.sy) * bend;
    const my = (a.sy + b.sy) / 2 - (b.sx - a.sx) * bend;
    this.ctx.moveTo(a.sx, a.sy);
    this.ctx.quadraticCurveTo(mx, my, b.sx, b.sy);
  }

  private draw() {
    const { ctx, cam, h } = this;
    const g = this.graph;
    const w = this.w - this.inset;
    const fullW = this.w;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 1;
    const bg = this.color("bg");
    const grad = ctx.createRadialGradient(w * 0.45, h / 2, 0, w / 2, h / 2, Math.max(fullW, h) * 0.8);
    grad.addColorStop(0, rgba([bg[0] + 16, bg[1] + 10, bg[2] + 24].map((v) => Math.min(255, v)) as RGB, 1));
    grad.addColorStop(1, rgba([bg[0] * 0.55, bg[1] * 0.55, bg[2] * 0.66].map(Math.round) as RGB, 1));
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, fullW, h);
    if (!g) return;

    for (const n of g.nodes.values()) {
      let p = this.proj.get(n);
      if (!p) {
        p = { sx: 0, sy: 0, scale: 0, depth: 0 };
        this.proj.set(n, p);
      }
      const [x, y, z] = this.at(n);
      project(cam, x, y, z, w, h, p);
    }
    const base = (Math.min(w, h) * 0.9) / cam.dist;
    const t = this.time;
    ctx.globalCompositeOperation = "lighter";

    // A faint haze along the path axis gives the network a body.
    {
      const e = this.proj.get(g.entry);
      if (e && e.scale > 0) {
        const r = Math.min(900, 260 * e.scale * 2);
        ctx.globalAlpha = 0.18;
        ctx.drawImage(this.sprite("entry"), e.sx - r, e.sy - r, r * 2, r * 2);
      }
    }

    // Containment links, bucketed by depth fog; links that lead to live findings glow warmer.
    const buckets: Array<Array<[Projected, Projected]>> = [[], [], []];
    const warm: Array<[Projected, Projected, number, ColorKey]> = [];
    const affects: Array<[Projected, Projected, ColorKey, number]> = [];
    const enables: Array<[Projected, Projected, ColorKey, boolean]> = [];
    for (const e of g.edges) {
      if (!this.visible(e.b)) continue;
      const a = this.proj.get(e.a);
      const b = this.proj.get(e.b);
      if (!a || !b || a.scale === 0 || b.scale === 0) continue;
      if (this.chainEdges.has(`${e.a.id}>${e.b.id}`)) continue; // drawn with the chain
      if (e.kind === "contains") {
        if (e.b.subtreeRisk > 0) warm.push([a, b, e.b.subtreeRisk, e.b.worst || "low"]);
        const f = (this.fog(a) + this.fog(b)) / 2;
        buckets[f > 0.75 ? 2 : f > 0.42 ? 1 : 0].push([a, b]);
      } else if (e.kind === "affects") {
        affects.push([a, b, this.nodeColor(e.b), e.b.risk > 0 ? 1 : 0.4]);
      } else enables.push([a, b, this.nodeColor(e.b), e.b.risk > 0 && e.a.risk > 0]);
    }
    ctx.lineCap = "round";
    const link = this.color("entry");
    const alphas = [0.08, 0.17, 0.32];
    for (let i = 0; i < 3; i++) {
      if (!buckets[i].length) continue;
      ctx.strokeStyle = rgba(link, alphas[i]);
      ctx.lineWidth = clamp(base * 1.1, 0.6, 2);
      ctx.beginPath();
      for (const [a, b] of buckets[i]) this.curve(a, b, 0.1);
      ctx.stroke();
    }
    for (const [a, b, risk, key] of warm) {
      const f = (this.fog(a) + this.fog(b)) / 2;
      ctx.strokeStyle = rgba(this.color(key), clamp(0.08 + Math.sqrt(risk) * 0.05, 0.08, 0.36) * f);
      ctx.lineWidth = clamp(base * (1.2 + Math.sqrt(risk) * 0.35), 0.8, 4);
      ctx.beginPath();
      this.curve(a, b, 0.1);
      ctx.stroke();
    }
    ctx.setLineDash([2, 4]);
    for (const [a, b, key, s] of affects) {
      ctx.strokeStyle = rgba(this.color(key), 0.35 * s * this.fog(b));
      ctx.lineWidth = clamp(base * 0.9, 0.6, 1.6);
      ctx.beginPath();
      ctx.moveTo(a.sx, a.sy);
      ctx.lineTo(b.sx, b.sy);
      ctx.stroke();
    }
    ctx.setLineDash([6, 6]);
    ctx.lineDashOffset = -t * 18;
    for (const [a, b, key, live] of enables) {
      ctx.strokeStyle = rgba(this.color(key), live ? 0.5 : 0.18);
      ctx.lineWidth = clamp(base * 1.6, 1, 2.6);
      ctx.beginPath();
      this.curve(a, b, -0.22);
      ctx.stroke();
    }
    ctx.setLineDash([]);
    ctx.lineDashOffset = 0;

    // The selected chain: a wide halo and a bright core along every hop.
    if (this.chainHops.length > 1) {
      const col = this.color(this.chain?.worst ?? "critical");
      const pts = this.chainHops.map((n) => this.proj.get(n));
      for (const [width, alpha] of [
        [10, 0.08],
        [5, 0.18],
        [1.8, 0.85],
      ] as const) {
        ctx.strokeStyle = rgba(col, alpha);
        ctx.lineWidth = clamp(base * width, width * 0.5, width * 1.6);
        ctx.beginPath();
        for (let i = 1; i < pts.length; i++) {
          const a = pts[i - 1];
          const b = pts[i];
          if (!a || !b || a.scale === 0 || b.scale === 0) continue;
          const enable = this.chainHops[i - 1].kind === "finding" && this.chainHops[i].kind === "finding";
          if (enable) this.curve(a, b, -0.22);
          else this.curve(a, b, 0.1);
        }
        ctx.stroke();
      }
    }

    // Shock rings where traffic lands on a finding, and on new findings.
    for (const s of this.shocks) {
      const p = this.proj.get(s.n);
      if (!p || p.scale === 0) continue;
      const u = s.age / 1.6;
      const r = u ** 0.55 * 40 * p.scale + 3;
      ctx.strokeStyle = rgba(this.color(s.tone), (1 - u) * 0.6);
      ctx.lineWidth = 1.2 + (1 - u) * 2.4;
      ctx.beginPath();
      ctx.arc(p.sx, p.sy, r, 0, Math.PI * 2);
      ctx.stroke();
    }

    // Nodes.
    for (const n of g.nodes.values()) {
      if (!this.visible(n)) continue;
      const p = this.proj.get(n);
      if (!p || p.scale === 0) continue;
      if (p.sx < -80 || p.sy < -80 || p.sx > fullW + 80 || p.sy > h + 80) continue;
      const f = this.fog(p);
      const key = this.nodeColor(n);
      const onChain = this.chainSet.has(n);
      let energy = 0.25;
      let size = BASE_SIZE[n.kind];
      let dim = 1;
      if (n.kind === "entry") {
        energy = 0.55 + 0.2 * Math.sin(t * 1.6);
      } else if (n.kind === "finding") {
        size *= 0.8 + Math.sqrt(Math.max(0.3, n.risk)) * 0.28;
        if (n.risk > 0) {
          const rate = n.severity === "critical" ? 3.2 : n.severity === "high" ? 2.2 : 1.4;
          energy = 0.35 + 0.3 * (0.5 + 0.5 * Math.sin(t * rate + n.depth));
        } else dim = 0.45;
      } else {
        size *= 1 + Math.min(1.2, Math.sqrt(n.subtreeRisk) * 0.12);
        if (!n.inScope) dim = 0.5;
        else if (!n.tested && n.findingCount === 0) dim = 0.72;
      }
      if (onChain) energy = Math.max(energy, 0.7);
      if (n === this.hovered || n === this.selected) energy = Math.max(energy, 0.9);
      const r = size * p.scale * (1 + energy * 0.35);
      const R = Math.min(170, r * (4 + energy * 1.6));
      if (R < 1.3) {
        ctx.globalAlpha = f * dim * 0.8;
        ctx.fillStyle = rgba(this.color(key), 1);
        ctx.fillRect(p.sx - 0.6, p.sy - 0.6, 1.2, 1.2);
        continue;
      }
      // Assets that hold live findings carry a faint halo in their worst severity.
      if (n.kind !== "finding" && n.kind !== "entry" && n.worst) {
        const hr = Math.min(190, R * 1.5);
        ctx.globalAlpha = clamp(f * 0.35 * Math.min(1, 0.3 + Math.sqrt(n.subtreeRisk) * 0.15), 0, 0.5);
        ctx.drawImage(this.sprite(n.worst), p.sx - hr, p.sy - hr, hr * 2, hr * 2);
      }
      ctx.globalAlpha = clamp(f * dim * (0.45 + 0.55 * Math.min(1, 0.35 + energy)), 0, 1);
      ctx.drawImage(this.sprite(key), p.sx - R, p.sy - R, R * 2, R * 2);
      if (r > 1.1) {
        const c = Math.min(64, r * (0.9 + energy * 0.5));
        ctx.globalAlpha = clamp(f * dim * (0.5 + energy * 0.5), 0, 1);
        ctx.drawImage(this.sprite(key, true), p.sx - c, p.sy - c, c * 2, c * 2);
      }
    }

    // Traffic: packets with a short trail.
    const pa: Projected = { sx: 0, sy: 0, scale: 0, depth: 0 };
    const pb: Projected = { sx: 0, sy: 0, scale: 0, depth: 0 };
    for (const pk of this.packets) {
      if (!this.visible(pk.hops[pk.hops.length - 1])) continue;
      if (!this.point(pk.hops, pk.at, pa, w)) continue;
      this.point(pk.hops, Math.max(0, pk.at - 38 * pk.size), pb, w);
      const col = this.color(pk.tone);
      const f = this.fog(pa);
      const strong = pk.loop || pk.size >= 1;
      ctx.globalAlpha = 1;
      ctx.strokeStyle = rgba(col, (strong ? 0.8 : 0.45) * f);
      ctx.lineWidth = clamp(pa.scale * 2.4 * pk.size, 0.8, 3.6);
      ctx.beginPath();
      ctx.moveTo(pb.sx, pb.sy);
      ctx.lineTo(pa.sx, pa.sy);
      ctx.stroke();
      const R = clamp(pa.scale * 13 * pk.size, 3, 22);
      ctx.globalAlpha = f * (strong ? 1 : 0.6);
      ctx.drawImage(this.sprite(pk.tone, true), pa.sx - R, pa.sy - R, R * 2, R * 2);
    }

    // Labels: the selected chain first, then findings, then the busiest assets.
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 1;
    const labels: Array<[PathNode, Projected, number]> = [];
    for (const n of g.nodes.values()) {
      if (!this.visible(n)) continue;
      const p = this.proj.get(n);
      if (!p || p.scale === 0) continue;
      let rank = 0;
      if (n === this.selected) rank = 100;
      else if (n === this.hovered) rank = 95;
      else if (this.chainSet.has(n)) rank = 80 - this.chainHops.indexOf(n) * 0.1;
      else if (n.kind === "entry") rank = 70;
      else if (n.kind === "finding" && n.risk > 0) rank = 40 + n.risk;
      else if (n.kind !== "finding" && n.subtreeRisk > 0) rank = 20 + Math.min(15, n.subtreeRisk);
      else if (n.kind !== "finding" && (g.assets <= 40 || p.scale > base * 1.5)) rank = 5;
      if (rank > 0) labels.push([n, p, rank]);
    }
    labels.sort((a, b) => b[2] - a[2]);
    ctx.textBaseline = "middle";
    const placed: Array<[number, number, number, number]> = [];
    let drawn = 0;
    for (const [n, p, rank] of labels) {
      if (drawn >= 22) break;
      const text = shortLabel(n);
      const big = rank >= 80;
      ctx.font = `${big ? 500 : 400} ${big ? 12 : 11}px ${this.font}`;
      const tw = ctx.measureText(text).width;
      const x = p.sx + 11 + tw > w - 6 ? p.sx - 11 - tw : p.sx + 11;
      const y = p.sy;
      if (y < 56 && rank < 95) continue;
      if (placed.some(([ax, ay, aw, ah]) => x < ax + aw && x + tw > ax && y - 7 < ay + ah && y + 7 > ay)) continue;
      placed.push([x - 4, y - 8, tw + 8, 16]);
      const f = rank >= 80 ? 1 : Math.max(0.55, this.fog(p));
      ctx.fillStyle = `rgba(8,6,16,${0.6 * f})`;
      ctx.fillRect(x - 4, y - 8, tw + 8, 16);
      if (n.kind === "finding") {
        ctx.fillStyle = rgba(this.color(this.nodeColor(n)), f);
        ctx.fillRect(x - 4, y - 8, 2, 16);
      }
      ctx.fillStyle = `rgba(244,242,255,${0.92 * f})`;
      ctx.fillText(text, x, y + 0.5);
      drawn++;
    }
    if (this.selected) {
      const p = this.proj.get(this.selected);
      if (p && p.scale > 0) {
        const r = BASE_SIZE[this.selected.kind] * p.scale * 2 + 7;
        ctx.strokeStyle = rgba(this.color("entry"), 0.9);
        ctx.lineWidth = 1.5;
        ctx.setLineDash([4, 4]);
        ctx.lineDashOffset = -t * 12;
        ctx.beginPath();
        ctx.arc(p.sx, p.sy, r, 0, Math.PI * 2);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }
  }

  private pick(x: number, y: number): PathNode | null {
    const g = this.graph;
    if (!g) return null;
    let best: PathNode | null = null;
    let bestD = Infinity;
    for (const n of g.nodes.values()) {
      if (!this.visible(n)) continue;
      const p = this.proj.get(n);
      if (!p || p.scale === 0) continue;
      const r = Math.max(8, BASE_SIZE[n.kind] * p.scale * 1.7);
      const d = Math.hypot(p.sx - x, p.sy - y);
      const score = d - (n.kind === "finding" ? 2 : 0);
      if (d <= r && score < bestD) {
        best = n;
        bestD = score;
      }
    }
    return best;
  }

  private bindInput() {
    const c = this.canvas;
    const local = (e: { clientX: number; clientY: number }) => {
      const r = c.getBoundingClientRect();
      return [e.clientX - r.left, e.clientY - r.top] as const;
    };
    const down = (e: PointerEvent) => {
      const [x, y] = local(e);
      this.drag = { x, y, px: x, py: y, pan: e.button !== 0 || e.shiftKey, moved: false };
      c.setPointerCapture(e.pointerId);
    };
    const move = (e: PointerEvent) => {
      const [x, y] = local(e);
      const d = this.drag;
      if (!d) {
        const n = this.pick(x, y);
        if (n !== this.hovered) {
          this.hovered = n;
          c.style.cursor = n ? "pointer" : "grab";
        }
        this.onHover(n ? { x, y, kind: n.kind, label: shortLabel(n), severity: n.severity } : null);
        return;
      }
      const dx = x - d.px;
      const dy = y - d.py;
      d.px = x;
      d.py = y;
      if (!d.moved && Math.hypot(x - d.x, y - d.y) > 4) {
        d.moved = true;
        c.style.cursor = "grabbing";
      }
      if (!d.moved) return;
      this.lastInteract = this.time;
      if (d.pan) {
        const s = (Math.min(this.w - this.inset, this.h) * 0.9) / this.cam.dist;
        const cy = Math.cos(this.cam.yaw);
        const sy = Math.sin(this.cam.yaw);
        const cp = Math.cos(this.cam.pitch);
        const sp = Math.sin(this.cam.pitch);
        this.goal.tx -= (dx * cy + dy * -sp * sy) / s;
        this.goal.ty -= (dy * cp) / s;
        this.goal.tz -= (dx * -sy + dy * -sp * cy) / s;
      } else {
        this.cam.yaw += dx * 0.006;
        this.swayBase = this.cam.yaw;
        this.cam.pitch = clamp(this.cam.pitch + dy * 0.006, -1.35, 1.35);
      }
    };
    const up = (e: PointerEvent) => {
      const d = this.drag;
      this.drag = null;
      c.style.cursor = this.hovered ? "pointer" : "grab";
      if (c.hasPointerCapture(e.pointerId)) c.releasePointerCapture(e.pointerId);
      if (d && !d.moved && e.button === 0) {
        const [x, y] = local(e);
        this.select(this.pick(x, y));
      }
    };
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      this.lastInteract = this.time;
      this.goal.dist = clamp(this.goal.dist * Math.exp(e.deltaY * 0.0012), 40, 9000);
    };
    const dbl = (e: MouseEvent) => {
      const [x, y] = local(e);
      if (!this.pick(x, y)) {
        this.select(null);
        this.fit();
      }
    };
    const leave = () => {
      this.hovered = null;
      this.onHover(null);
    };
    const ctxMenu = (e: MouseEvent) => e.preventDefault();
    c.addEventListener("pointerdown", down);
    c.addEventListener("pointermove", move);
    c.addEventListener("pointerup", up);
    c.addEventListener("pointercancel", up);
    c.addEventListener("pointerleave", leave);
    c.addEventListener("wheel", wheel, { passive: false });
    c.addEventListener("dblclick", dbl);
    c.addEventListener("contextmenu", ctxMenu);
    c.style.cursor = "grab";
    c.style.touchAction = "none";
    this.cleanups.push(() => {
      c.removeEventListener("pointerdown", down);
      c.removeEventListener("pointermove", move);
      c.removeEventListener("pointerup", up);
      c.removeEventListener("pointercancel", up);
      c.removeEventListener("pointerleave", leave);
      c.removeEventListener("wheel", wheel);
      c.removeEventListener("dblclick", dbl);
      c.removeEventListener("contextmenu", ctxMenu);
    });
  }
}

function emptyCoverage(): CoverageGraphData {
  return { nodes: [], edges: [] };
}

export function AttackPathView({ taskId, className }: { taskId: string; className?: string }) {
  const wrapRef = React.useRef<HTMLDivElement>(null);
  const canvasRef = React.useRef<HTMLCanvasElement>(null);
  const probeRef = React.useRef<HTMLDivElement>(null);
  const rendererRef = React.useRef<AttackRenderer | null>(null);
  const graphRef = React.useRef<AttackGraph | null>(null);
  const [palette, setPalette] = React.useState<Record<ColorKey, RGB>>(FALLBACK);
  const [phase, setPhase] = React.useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = React.useState("");
  const [chains, setChains] = React.useState<AttackChain[]>([]);
  const [chain, setChain] = React.useState<AttackChain | null>(null);
  const [stats, setStats] = React.useState<Stats>({
    assets: 0,
    findings: 0,
    closed: 0,
    chains: 0,
    critical: 0,
    high: 0,
    dropped: 0,
    kinds: ["entry"],
  });
  const [refreshedAt, setRefreshedAt] = React.useState<Date | null>(null);
  const [selected, setSelected] = React.useState<NodeInfo | null>(null);
  const [hover, setHover] = React.useState<HoverInfo | null>(null);
  const [sway, setSway] = React.useState(true);
  const [paused, setPaused] = React.useState(false);
  const [showClosed, setShowClosed] = React.useState(true);
  const [fullscreen, setFullscreen] = React.useState(false);
  const [cardOpen, setCardOpen] = React.useState(true);

  const pickChainRef = React.useRef<(c: AttackChain | null) => void>(() => undefined);
  const pickChain = React.useCallback((c: AttackChain | null, fly: boolean) => {
    const r = rendererRef.current;
    setChain(c);
    r?.setChain(c);
    if (c && fly) r?.fitChain(c);
  }, []);

  React.useEffect(() => {
    const canvas = canvasRef.current;
    const wrap = wrapRef.current;
    const probe = probeRef.current;
    if (!canvas || !wrap || !probe) return;
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const renderer = new AttackRenderer(canvas, reduced);
    rendererRef.current = renderer;
    setSway(renderer.sway);
    const pal = readPalette(probe);
    renderer.setPalette(pal);
    setPalette(pal);
    renderer.onSelect = (n) => {
      setSelected(n ? infoFor(n) : null);
      const g = graphRef.current;
      if (n && g && n.kind === "finding") {
        const c = chainTo(g, n);
        if (c) {
          setCardOpen(true);
          setChain(c);
          renderer.setChain(c);
        }
      }
    };
    let hoverRaf = 0;
    renderer.onHover = (h) => {
      cancelAnimationFrame(hoverRaf);
      hoverRaf = requestAnimationFrame(() => setHover(h));
    };
    const ro = new ResizeObserver(([entry]) => {
      const { width, height } = entry.contentRect;
      renderer.resize(width, height);
    });
    ro.observe(wrap);
    const rect = wrap.getBoundingClientRect();
    renderer.resize(rect.width, rect.height);
    renderer.start();

    let alive = true;
    let sig = "";
    let known: Set<string> | null = null;
    let chainEnd = "";
    let userPicked = false;

    const load = async () => {
      const [coverage, findings, exploration] = await Promise.all([
        api.taskCoverageGraph(taskId).catch(emptyCoverage),
        api.findings(taskId),
        api.explorationGraph(taskId).catch(() => ({ nodes: [] as TaskNode[], edges: [] as Edge[] })),
      ]);
      if (!alive) return;
      const nextSig = JSON.stringify([
        coverage.nodes.map((n) => [n.key, n.tested, n.in_scope]),
        coverage.edges.length,
        findings.map((f) => [f.id, f.severity, f.status, f.verification_status, f.assets?.length ?? 0]),
        exploration.edges.length,
      ]);
      setRefreshedAt(new Date());
      if (nextSig === sig) return;
      sig = nextSig;
      const g = buildAttackGraph({
        assets: coverage.nodes,
        contains: coverage.edges,
        findings: findings.map(findingIn),
        graphNodes: (exploration.nodes ?? []).map((n) => ({ id: n.id, type: n.type })),
        graphEdges: (exploration.edges ?? []).map((e) => ({ src: e.src, dst: e.dst, rel: e.rel })),
      });
      layoutAttackGraph(g);
      const ids = new Set([...g.nodes.keys()]);
      const fresh = new Set<string>();
      if (known) for (const id of ids) if (!known.has(id)) fresh.add(id);
      const first = !known;
      known = ids;
      graphRef.current = g;
      renderer.setGraph(g, fresh);
      setChains(g.chains);
      let crit = 0;
      let high = 0;
      for (const f of g.findings) {
        if (f.risk <= 0) continue;
        if (f.severity === "critical") crit++;
        else if (f.severity === "high") high++;
      }
      setStats({
        assets: g.assets,
        findings: g.active,
        closed: g.closed,
        chains: g.chains.length,
        critical: crit,
        high,
        dropped: g.droppedAssets,
        kinds: ASSET_LEGEND.filter((k) => {
          for (const n of g.nodes.values()) if (n.kind === k) return true;
          return false;
        }),
      });
      // Keep the chain the user is looking at when it still exists.
      const keep = chainEnd ? g.nodes.get(chainEnd) : undefined;
      const next = keep && userPicked ? chainTo(g, keep) : (g.chains[0] ?? null);
      chainEnd = next?.findings[next.findings.length - 1]?.id ?? "";
      setChain(next);
      renderer.setChain(next);
      if (renderer.selected) setSelected(infoFor(renderer.selected));
      if (first) {
        renderer.fit(false);
        setPhase("ready");
      }
    };
    const unpick = renderer.onSelect;
    renderer.onSelect = (n) => {
      unpick(n);
      if (n?.kind === "finding") {
        userPicked = true;
        chainEnd = n.id;
      }
    };
    pickChainRef.current = (c) => {
      userPicked = !!c;
      chainEnd = c?.findings[c.findings.length - 1]?.id ?? "";
    };

    void load().catch((err) => {
      if (!alive) return;
      setPhase("error");
      setError((err as Error).message || t("Failed to load"));
    });
    const stopPoll = setVisibleInterval(() => load().catch(() => undefined), REFRESH_MS);
    const onFs = () => setFullscreen(document.fullscreenElement === wrap.parentElement);
    document.addEventListener("fullscreenchange", onFs);
    return () => {
      alive = false;
      stopPoll();
      cancelAnimationFrame(hoverRaf);
      ro.disconnect();
      document.removeEventListener("fullscreenchange", onFs);
      renderer.destroy();
      rendererRef.current = null;
      graphRef.current = null;
    };
  }, [taskId]);

  const r = rendererRef;
  const choose = (c: AttackChain) => {
    setCardOpen(true);
    pickChainRef.current(c);
    r.current?.select(null);
    pickChain(c, true);
  };
  const focusNode = (id: string) => {
    const n = graphRef.current?.nodes.get(id);
    if (n) r.current?.select(n);
  };
  const toggleFullscreen = () => {
    const el = wrapRef.current?.parentElement;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void el.requestFullscreen?.();
  };
  const timeFmt = React.useMemo(
    () => new Intl.DateTimeFormat(intlLocale, { hour: "2-digit", minute: "2-digit", second: "2-digit" }),
    [],
  );
  const noData = phase === "ready" && stats.assets === 0 && stats.findings + stats.closed === 0;
  const noFindings = phase === "ready" && !noData && stats.findings === 0;

  return (
    <div
      className={cn(
        "relative overflow-hidden rounded-xl bg-[#0d0b16] text-white",
        fullscreen && "rounded-none",
        className,
      )}
    >
      <div ref={probeRef} className="dark hidden" aria-hidden />
      <div ref={wrapRef} className="absolute inset-0">
        <canvas
          ref={canvasRef}
          className="block"
          role="img"
          aria-label={t("Attack paths of this task: assets, findings and the links between them")}
          tabIndex={0}
          onKeyDown={(e) => {
            if (e.key === "Escape" || e.key === "f") {
              r.current?.select(null);
              r.current?.fit();
            }
          }}
        />
      </div>

      {/* Counts */}
      <div className="pointer-events-none absolute top-3 left-3 flex max-w-[60%] flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="inline-flex items-center gap-1.5 rounded-full border border-white/15 bg-white/5 px-2 py-0.5 font-medium text-white/80 backdrop-blur">
            <span className="size-1.5 rounded-full" style={{ background: rgbCss(palette.entry) }} />
            {t("Attack paths")}
          </span>
          {refreshedAt && (
            <span className="text-[11px] text-white/45 tabular-nums">
              {t("Updated")} {timeFmt.format(refreshedAt)}
            </span>
          )}
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-white/55 tabular-nums">
          <Stat label={t("Assets")} value={stats.assets} />
          <Stat label={t("Open findings")} value={stats.findings} />
          <Stat label={t("Chains")} value={stats.chains} />
          <Stat label={t("Critical")} value={stats.critical} tone={stats.critical ? "text-rose-300" : undefined} />
          <Stat label={t("High")} value={stats.high} tone={stats.high ? "text-orange-300" : undefined} />
          {stats.closed > 0 && <Stat label={t("Closed")} value={stats.closed} />}
        </div>
        {stats.dropped > 0 && (
          <span className="text-[11px] text-amber-300/80">
            {t("Showing the assets on the way to each finding; {n} other assets left out.", {
              n: stats.dropped.toLocaleString(intlLocale),
            })}
          </span>
        )}
      </div>

      {/* Riskiest paths */}
      {chains.length > 0 && (
        <div className="absolute top-3 right-3 bottom-16 hidden w-72 flex-col gap-1.5 overflow-y-auto md:flex">
          <div className="px-1 text-[11px] font-medium tracking-wide text-white/50 uppercase">
            {t("Riskiest paths")}
          </div>
          {chains.map((c, i) => {
            const end = c.findings[c.findings.length - 1];
            const active = chain && chain.findings[chain.findings.length - 1] === end;
            const host = c.hops.find((n) => n.kind !== "entry" && n.kind !== "company");
            return (
              <button
                key={end.id}
                type="button"
                onClick={() => choose(c)}
                className={cn(
                  "rounded-md border bg-black/35 px-2.5 py-2 text-left backdrop-blur transition-colors hover:border-white/25 hover:bg-black/50",
                  active ? "border-white/30 bg-white/10" : "border-white/10",
                )}
              >
                <div className="flex items-center gap-1.5 text-[11px] text-white/55">
                  <span className="font-medium text-white/80">#{i + 1}</span>
                  <span
                    className="size-1.5 rounded-full"
                    style={{ background: rgbCss(palette[c.worst]), boxShadow: `0 0 6px ${rgbCss(palette[c.worst])}` }}
                  />
                  <span>{SEVERITY_LABEL[c.worst]}</span>
                  <span>
                    · {c.findings.length === 1 ? t("1 finding") : t("{n} findings", { n: c.findings.length })}
                  </span>
                  <span className="ml-auto tabular-nums">
                    {t("Risk")} {c.score.toLocaleString(intlLocale)}
                  </span>
                </div>
                <div className="mt-0.5 line-clamp-2 text-xs leading-snug text-white/90">
                  {c.findings.map((f) => shortLabel(f)).join(" → ")}
                </div>
                {host && host.kind !== "finding" && (
                  <div className="mt-0.5 truncate text-[11px] text-white/45">{shortLabel(host)}</div>
                )}
              </button>
            );
          })}
          <p className="px-1 pt-1 text-[10px] leading-snug text-white/35">
            {t(
              "Risk is severity weighted by triage status and verification, summed along the chain. A chain links findings when the exploration graph shows one led to the next.",
            )}
          </p>
        </div>
      )}

      {/* Legend */}
      <div className="pointer-events-none absolute right-52 bottom-3 left-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-white/60">
        {SEVERITIES.map((s) => (
          <span key={s} className="inline-flex items-center gap-1.5">
            <span
              className="size-2 rounded-full"
              style={{ background: rgbCss(palette[s]), boxShadow: `0 0 8px ${rgbCss(palette[s])}` }}
            />
            {SEVERITY_LABEL[s]}
          </span>
        ))}
        <span className="text-white/25">|</span>
        {stats.kinds.map((k) => (
          <span key={k} className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-full" style={{ background: rgbCss(palette[k]) }} />
            {KIND_LABEL[k]}
          </span>
        ))}
        <span className="hidden text-white/35 xl:inline">
          {t("Drag to turn · Shift-drag to pan · Scroll to zoom · Click a finding to trace its path")}
        </span>
      </div>

      {/* Controls */}
      <div className="absolute right-3 bottom-3 flex items-center gap-1">
        <IconToggle
          label={showClosed ? t("Hide closed findings") : t("Show closed findings")}
          active={!showClosed}
          onClick={() => {
            const v = !showClosed;
            setShowClosed(v);
            if (r.current) r.current.showClosed = v;
          }}
        >
          {showClosed ? <Eye /> : <EyeOff />}
        </IconToggle>
        <IconToggle
          label={t("Auto-rotate")}
          active={sway}
          onClick={() => {
            const v = !sway;
            setSway(v);
            if (r.current) r.current.sway = v;
          }}
        >
          <RotateCw />
        </IconToggle>
        <IconToggle
          label={paused ? t("Resume motion") : t("Pause motion")}
          active={paused}
          onClick={() => {
            const v = !paused;
            setPaused(v);
            if (r.current) r.current.paused = v;
          }}
        >
          {paused ? <Play /> : <Pause />}
        </IconToggle>
        <IconToggle
          label={t("Fit everything")}
          onClick={() => {
            r.current?.select(null);
            r.current?.fit();
          }}
        >
          <Crosshair />
        </IconToggle>
        <IconToggle label={fullscreen ? t("Exit full screen") : t("Full screen")} onClick={toggleFullscreen}>
          {fullscreen ? <Minimize2 /> : <Maximize2 />}
        </IconToggle>
      </div>

      {/* Hover tooltip */}
      {hover && !selected && (
        <div
          className="pointer-events-none absolute z-10 max-w-72 rounded-md border border-white/15 bg-black/70 px-2 py-1 text-[11px] text-white backdrop-blur"
          style={{ left: hover.x + 14, top: hover.y + 14 }}
        >
          <span className="text-white/55">
            {hover.severity
              ? `${SEVERITY_LABEL[hover.severity]} ${KIND_LABEL.finding.toLowerCase()}`
              : KIND_LABEL[hover.kind]}{" "}
            ·{" "}
          </span>
          {hover.label}
        </div>
      )}

      {/* Selected chain, or the selected asset */}
      {(selected || (chain && cardOpen)) && (
        <div className="absolute bottom-14 left-3 z-10 max-h-[46%] w-72 max-w-[calc(100%-1.5rem)] overflow-y-auto rounded-lg border border-white/15 bg-black/60 p-2.5 text-white shadow-2xl backdrop-blur-md">
          {selected && selected.kind !== "finding" ? (
            <AssetCard info={selected} palette={palette} onClose={() => r.current?.select(null)} onPick={focusNode} />
          ) : (
            chain &&
            cardOpen && (
              <ChainCard
                chain={chain}
                palette={palette}
                onPick={focusNode}
                selectedId={selected?.id}
                onClose={() => {
                  if (selected) r.current?.select(null);
                  else setCardOpen(false);
                }}
              />
            )
          )}
        </div>
      )}

      {phase === "loading" && <Centered>{t("Loading attack paths…")}</Centered>}
      {phase === "error" && <Centered>{error}</Centered>}
      {noData && <Centered>{t("No assets or findings recorded for this task yet.")}</Centered>}
      {noFindings && (
        <div className="pointer-events-none absolute inset-x-0 top-16 flex justify-center px-6">
          <span className="rounded-md border border-white/10 bg-black/40 px-3 py-1.5 text-center text-xs text-white/65 backdrop-blur">
            {t("No open findings yet. Paths appear here as the task reports findings on these assets.")}
          </span>
        </div>
      )}
    </div>
  );
}

function SevDot({ sev, palette }: { sev: Severity | ""; palette: Record<ColorKey, RGB> }) {
  const c = palette[sev || "closed"];
  return (
    <span
      className="size-2 shrink-0 rounded-full"
      style={{ background: rgbCss(c), boxShadow: `0 0 6px ${rgbCss(c)}` }}
    />
  );
}

function ChainCard({
  chain,
  palette,
  onPick,
  selectedId,
  onClose,
}: {
  chain: AttackChain;
  palette: Record<ColorKey, RGB>;
  onPick: (id: string) => void;
  selectedId?: string;
  onClose?: () => void;
}) {
  return (
    <>
      <div className="flex items-center gap-2 text-[11px] text-white/60">
        <SevDot sev={chain.worst} palette={palette} />
        <span className="font-medium text-white/85">{t("Attack path")}</span>
        <span className="tabular-nums">
          {t("Risk")} {chain.score.toLocaleString(intlLocale)}
        </span>
        {onClose && (
          <button
            type="button"
            className="ml-auto rounded px-1 text-white/50 hover:bg-white/10 hover:text-white"
            onClick={onClose}
            aria-label={t("Close")}
          >
            ×
          </button>
        )}
      </div>
      <ol className="mt-1.5 space-y-0.5">
        {chain.hops.map((n, i) => (
          <li key={n.id}>
            <button
              type="button"
              onClick={() => onPick(n.id)}
              className={cn(
                "flex w-full items-start gap-2 rounded px-1 py-0.5 text-left hover:bg-white/10",
                n.id === selectedId && "bg-white/10",
              )}
            >
              <span className="mt-0.5 w-4 shrink-0 text-right text-[10px] text-white/35 tabular-nums">{i + 1}</span>
              {n.kind === "finding" ? (
                <span className="mt-1">
                  <SevDot sev={n.risk > 0 ? n.severity : ""} palette={palette} />
                </span>
              ) : (
                <span className="mt-1 size-2 shrink-0 rounded-full" style={{ background: rgbCss(palette[n.kind]) }} />
              )}
              {n.kind === "finding" ? (
                <span className="min-w-0">
                  <span className="block truncate text-xs text-white/90">{shortLabel(n)}</span>
                  <span className="block text-[10px] text-white/45">
                    {[
                      n.severity && SEVERITY_LABEL[n.severity],
                      statusMeta("finding", n.status).label,
                      n.verification === "verified" && t("verified"),
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </span>
              ) : (
                <span className="flex min-w-0 flex-1 items-baseline gap-2">
                  <span className="truncate text-xs text-white/85">{shortLabel(n)}</span>
                  <span className="ml-auto shrink-0 text-[10px] text-white/40">{KIND_LABEL[n.kind]}</span>
                </span>
              )}
            </button>
          </li>
        ))}
      </ol>
    </>
  );
}

function AssetCard({
  info,
  palette,
  onClose,
  onPick,
}: {
  info: NodeInfo;
  palette: Record<ColorKey, RGB>;
  onClose: () => void;
  onPick: (id: string) => void;
}) {
  return (
    <>
      <div className="flex items-center gap-2 text-[11px] text-white/60">
        <span className="size-2 rounded-full" style={{ background: rgbCss(palette[info.kind]) }} />
        <span className="font-medium text-white/85">{KIND_LABEL[info.kind]}</span>
        {info.kind !== "entry" && (
          <span className="rounded bg-white/10 px-1.5 py-px">
            {info.inScope ? (info.tested ? t("Tested") : t("Not tested yet")) : t("Outside scope")}
          </span>
        )}
        <button
          type="button"
          className="ml-auto rounded px-1 text-white/50 hover:bg-white/10 hover:text-white"
          onClick={onClose}
          aria-label={t("Close")}
        >
          ×
        </button>
      </div>
      <p className="mt-1.5 line-clamp-3 font-mono text-xs leading-snug break-all text-white/90">
        {info.kind === "entry" ? t("Where every path starts: the task's scope as recorded.") : info.label}
      </p>
      <div className="mt-2 grid grid-cols-3 gap-2 text-[11px] text-white/55 tabular-nums">
        <div>
          <div className="text-white/85">{info.findingCount}</div>
          {t("Open findings")}
        </div>
        <div>
          <div className="text-white/85">{info.worst ? SEVERITY_LABEL[info.worst] : "–"}</div>
          {t("Worst")}
        </div>
        <div>
          <div className="text-white/85">{info.subtreeRisk.toLocaleString(intlLocale)}</div>
          {t("Risk")}
        </div>
      </div>
      {info.top.length > 0 && (
        <ul className="mt-2 space-y-0.5 border-t border-white/10 pt-1.5">
          {info.top.map((f) => (
            <li key={f.id}>
              <button
                type="button"
                onClick={() => onPick(f.id)}
                className="flex w-full items-center gap-2 rounded px-1 py-0.5 text-left text-xs text-white/85 hover:bg-white/10"
              >
                <SevDot sev={f.severity} palette={palette} />
                <span className="truncate">{f.label}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
