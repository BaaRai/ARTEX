"use client";

// Live "brain" of one task: a 3D connectome drawn on a canvas, fed only by the
// task's real exploration graph and activity stream. Graph nodes are the
// structural neurons, every recorded tool call sprouts a neuron on the intent
// that made it, and every activity event fires a signal down the network to
// the neuron that acted. The model lives in lib/brain-sim.ts; this file draws
// it and handles the camera, the data feed and the overlay.

import * as React from "react";
import { Crosshair, Maximize2, Minimize2, Navigation, Pause, Play, RotateCw } from "lucide-react";

import { nodeSummary } from "@/components/exploration-graph";
import { Button } from "@/components/ui/button";
import { intlLocale, t } from "@/i18n";
import { api, sseUrl } from "@/lib/api";
import {
  type ActivityIn,
  Brain,
  type Camera,
  CORE_ID,
  fitDistance,
  type Neuron,
  type NeuronKind,
  type Projected,
  project,
  type SignalTone,
} from "@/lib/brain-sim";
import { setVisibleInterval } from "@/lib/polling";
import type { Activity, Edge, TaskNode } from "@/lib/types";
import { cn } from "@/lib/utils";

type RGB = [number, number, number];
type ColorKey = NeuronKind | SignalTone | "bg";

const KIND_VAR: Record<ColorKey, string> = {
  core: "--primary",
  task: "--foreground",
  begin: "--foreground",
  goal: "--chart-5",
  intent: "--chart-1",
  hint: "--chart-3",
  fact: "--chart-3",
  finding: "--chart-4",
  digest: "--chart-2",
  tool: "--chart-2",
  think: "--chart-1",
  return: "--chart-5",
  error: "--destructive",
  grow: "--primary",
  user: "--foreground",
  done: "--chart-5",
  bg: "--background",
};

// Used only if the theme variables can't be read.
const FALLBACK: Record<ColorKey, RGB> = {
  core: [176, 140, 255],
  task: [240, 240, 248],
  begin: [240, 240, 248],
  goal: [96, 210, 150],
  intent: [176, 140, 255],
  hint: [240, 190, 90],
  fact: [240, 190, 90],
  finding: [255, 110, 130],
  digest: [80, 200, 220],
  tool: [80, 200, 220],
  think: [176, 140, 255],
  return: [96, 210, 150],
  error: [255, 100, 90],
  grow: [176, 140, 255],
  user: [240, 240, 248],
  done: [96, 210, 150],
  bg: [22, 20, 32],
};

const BASE_SIZE: Record<NeuronKind, number> = {
  core: 19,
  task: 13,
  begin: 13,
  goal: 14,
  intent: 10,
  hint: 7.5,
  fact: 7.5,
  finding: 15,
  digest: 11,
  tool: 3.4,
};

const KIND_LABEL: Record<NeuronKind, string> = {
  core: t("Agent core"),
  task: t("Root task"),
  begin: t("Start"),
  goal: t("Goal"),
  hint: t("Hint"),
  intent: t("Intent"),
  fact: t("Fact"),
  finding: t("Vulnerability"),
  digest: t("Digest"),
  tool: t("Tool call"),
};

const LEGEND: NeuronKind[] = ["core", "goal", "intent", "tool", "fact", "finding"];
const DIM_STATES = new Set(["done", "abandoned", "stopped", "exhausted", "consumed", "dismissed", "deleted", "met"]);
const HISTORY_PAGE = 1000;
const HISTORY_MAX = 8000;
const REPLAY_TAIL = 48;

export interface BrainTick {
  seq: number;
  ts: string;
  kind: string;
  worker: string;
  tool?: string;
  summary: string;
  isError: boolean;
  neuronId: string;
}

export interface BrainStats {
  neurons: number;
  synapses: number;
  toolCalls: number;
  findings: number;
  running: number;
  perMinute: number;
}

interface SelectedInfo {
  id: string;
  kind: NeuronKind;
  label: string;
  state: string;
  priority: number;
  fires: number;
  tools: number;
  lastSummary: string;
  parentLabel: string;
}

interface HoverInfo {
  x: number;
  y: number;
  kind: NeuronKind;
  label: string;
}

function clamp(v: number, lo: number, hi: number) {
  return v < lo ? lo : v > hi ? hi : v;
}

function rgba([r, g, b]: RGB, a: number) {
  return `rgba(${r},${g},${b},${a})`;
}

function readPalette(probe: HTMLElement): Record<ColorKey, RGB> {
  const out = { ...FALLBACK };
  const c = document.createElement("canvas");
  c.width = 1;
  c.height = 1;
  const ctx = c.getContext("2d", { willReadFrequently: true });
  if (!ctx) return out;
  const style = getComputedStyle(probe);
  for (const key of Object.keys(KIND_VAR) as ColorKey[]) {
    const css = style.getPropertyValue(KIND_VAR[key]).trim();
    if (!css) continue;
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = "#010203";
    ctx.fillStyle = css;
    if (ctx.fillStyle === "#010203") continue; // not a colour the canvas understands
    ctx.fillRect(0, 0, 1, 1);
    const d = ctx.getImageData(0, 0, 1, 1).data;
    out[key] = [d[0], d[1], d[2]];
  }
  // The brain sits in a deep field in either theme so the glow reads as light.
  const [r, g, b] = out.bg;
  out.bg = [Math.round(r * 0.55), Math.round(g * 0.55), Math.round(b * 0.62)];
  return out;
}

function glowSprite([r, g, b]: RGB, hot: boolean): HTMLCanvasElement {
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

function toActivityIn(a: Activity): ActivityIn {
  return {
    seq: a.seq,
    intent_id: a.intent_id,
    worker: a.worker,
    kind: a.kind,
    tool: a.tool,
    tool_use_id: a.tool_use_id,
    is_error: a.is_error,
    summary: a.summary,
    ts: a.ts,
  };
}

function graphIn(nodes: TaskNode[], edges: Edge[]) {
  return {
    nodes: nodes
      .filter((n) => n.state !== "deleted")
      .map((n) => ({
        id: n.id,
        type: n.type,
        state: n.state,
        priority: n.priority,
        label: nodeSummary(n).replace(/\s+/g, " ").trim().slice(0, 160),
      })),
    edges: edges.map((e) => ({ src: e.src, dst: e.dst, rel: e.rel })),
  };
}

function shortLabel(n: Neuron): string {
  if (n.kind === "core") return KIND_LABEL.core;
  const s = n.label || KIND_LABEL[n.kind];
  return s.length > 42 ? `${s.slice(0, 41)}…` : s;
}

/** Owns the canvas, the camera and the frame loop. Kept outside React state on purpose. */
class BrainRenderer {
  readonly brain: Brain;
  readonly cam: Camera = { yaw: 0.6, pitch: -0.32, dist: 900, tx: 0, ty: 0, tz: 0 };
  goal = { tx: 0, ty: 0, tz: 0, dist: 900 };
  autoRotate = true;
  follow = false;
  paused = false;
  selected: Neuron | null = null;
  hovered: Neuron | null = null;
  focus: Neuron | null = null;
  onSelect: (n: Neuron | null) => void = () => undefined;
  onHover: (h: HoverInfo | null) => void = () => undefined;

  private ctx: CanvasRenderingContext2D;
  private w = 1;
  private inset = 0;
  private h = 1;
  private dpr = 1;
  private palette: Record<ColorKey, RGB> = FALLBACK;
  private sprites = new Map<string, HTMLCanvasElement>();
  private proj = new Map<Neuron, Projected>();
  private raf = 0;
  private last = 0;
  private lastInteract = -1e9;
  private font = "ui-sans-serif, system-ui, sans-serif";
  private drag: { x: number; y: number; px: number; py: number; pan: boolean; moved: boolean } | null = null;
  private readonly cleanups: Array<() => void> = [];

  constructor(
    private readonly canvas: HTMLCanvasElement,
    brain: Brain,
    reducedMotion: boolean,
  ) {
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("2D canvas unavailable");
    this.ctx = ctx;
    this.brain = brain;
    this.autoRotate = !reducedMotion;
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
    this.inset = this.w >= 768 ? 272 : 0;
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

  fit(animate = true) {
    let cx = 0;
    let cy = 0;
    let cz = 0;
    let count = 0;
    for (const n of this.brain.neurons.values()) {
      cx += n.x;
      cy += n.y;
      cz += n.z;
      count++;
    }
    cx /= count || 1;
    cy /= count || 1;
    cz /= count || 1;
    // Fit most of the brain, not the farthest outlier, so the body fills the view.
    const radii: number[] = [];
    for (const n of this.brain.neurons.values()) radii.push(Math.hypot(n.x - cx, n.y - cy, n.z - cz));
    radii.sort((a, b) => a - b);
    const r = Math.max(60, (radii[Math.floor(radii.length * 0.9)] ?? 0) * 1.08);
    this.goal = { tx: cx, ty: cy, tz: cz, dist: fitDistance(r, this.w - this.inset, this.h) };
    this.focus = null;
    if (!animate) Object.assign(this.cam, this.goal);
  }

  flyTo(n: Neuron) {
    this.focus = n;
    const close = n.kind === "tool" ? 70 : n.kind === "core" ? 260 : 170;
    this.goal.dist = Math.min(Math.max(close, 50), Math.max(this.cam.dist, close));
    if (this.cam.dist > close * 1.4) this.goal.dist = close;
  }

  select(n: Neuron | null, fly = true) {
    this.selected = n;
    if (n && fly) this.flyTo(n);
    if (!n) this.focus = null;
    this.onSelect(n);
  }

  /** A live event fired this neuron. */
  fired(n: Neuron) {
    if (!this.follow) return;
    const target = n.kind === "tool" && n.parent ? n.parent : n;
    if (!this.selected) this.focus = target;
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

  private neuronColor(n: Neuron): ColorKey {
    if (n.error) return "error";
    if (n.kind === "intent" && n.state === "blocked") return "error";
    return n.kind;
  }

  private frame(dt: number) {
    const { brain, cam, goal } = this;
    if (!this.paused) brain.step(dt);
    if (this.autoRotate && !this.drag && brain.time - this.lastInteract > 4) cam.yaw += dt * 0.07;
    if (this.focus) {
      if (!brain.neurons.has(this.focus.id)) this.focus = null;
      else {
        goal.tx = this.focus.x;
        goal.ty = this.focus.y;
        goal.tz = this.focus.z;
      }
    }
    const k = 1 - Math.exp(-dt * 3.2);
    cam.tx += (goal.tx - cam.tx) * k;
    cam.ty += (goal.ty - cam.ty) * k;
    cam.tz += (goal.tz - cam.tz) * k;
    cam.dist += (goal.dist - cam.dist) * k;
    this.draw();
  }

  private fog(p: Projected): number {
    // 1 at the camera target's depth, fading toward the back of the brain.
    const rel = cam_rel(p.depth, this.cam.dist);
    return clamp(1.25 - rel * 0.55, 0.12, 1);
  }

  private draw() {
    const { ctx, brain, cam, h } = this;
    // Projection width leaves room for the activity list on the right.
    const w = this.w - this.inset;
    const fullW = this.w;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 1;
    const bg = this.color("bg");
    const grad = ctx.createRadialGradient(w / 2, h / 2, 0, w / 2, h / 2, Math.max(fullW, h) * 0.75);
    grad.addColorStop(0, rgba([bg[0] + 14, bg[1] + 10, bg[2] + 22].map((v) => Math.min(255, v)) as RGB, 1));
    grad.addColorStop(1, rgba([bg[0] * 0.6, bg[1] * 0.6, bg[2] * 0.7].map(Math.round) as RGB, 1));
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, fullW, h);

    // Project every neuron once.
    for (const n of brain.neurons.values()) {
      let p = this.proj.get(n);
      if (!p) {
        p = { sx: 0, sy: 0, scale: 0, depth: 0 };
        this.proj.set(n, p);
      }
      project(cam, n.x, n.y, n.z, w, h, p);
    }
    if (this.proj.size > brain.neurons.size * 1.5) {
      for (const n of this.proj.keys()) if (!brain.neurons.has(n.id)) this.proj.delete(n);
    }
    const base = (Math.min(w, h) * 0.9) / cam.dist;

    ctx.globalCompositeOperation = "lighter";
    // Volume glow around the core: the brain's body, sized from its real extent.
    {
      const p = this.proj.get(brain.core);
      if (p && p.scale > 0) {
        const r = brain.extent() * p.scale * 1.25;
        const s = this.sprite("core");
        ctx.globalAlpha = 0.16;
        ctx.drawImage(s, p.sx - r, p.sy - r, r * 2, r * 2);
      }
    }

    // Axons and dendrites, batched into a few alpha buckets.
    const buckets: Array<Array<[Projected, Projected, number]>> = [[], [], []];
    const toolLinks: Array<[Projected, Projected]> = [];
    const hot: Array<[Projected, Projected, number, ColorKey]> = [];
    for (const n of brain.neurons.values()) {
      const parent = n.parent;
      if (!parent) continue;
      const a = this.proj.get(parent);
      const b = this.proj.get(n);
      if (!a || !b || a.scale === 0 || b.scale === 0) continue;
      if (n.linkHeat > 0.06) hot.push([a, b, n.linkHeat, n.kind === "tool" ? (n.error ? "error" : "tool") : "grow"]);
      if (n.kind === "tool") {
        toolLinks.push([a, b]);
        continue;
      }
      const f = (this.fog(a) + this.fog(b)) / 2;
      buckets[f > 0.75 ? 2 : f > 0.42 ? 1 : 0].push([a, b, n.growth]);
    }
    const linkColor = this.color("core");
    ctx.lineCap = "round";
    const alphas = [0.1, 0.22, 0.4];
    for (let i = 0; i < 3; i++) {
      if (!buckets[i].length) continue;
      ctx.strokeStyle = rgba(linkColor, alphas[i]);
      ctx.lineWidth = clamp(base * 1.1, 0.6, 2.2);
      ctx.beginPath();
      for (const [a, b, g] of buckets[i]) {
        const ex = a.sx + (b.sx - a.sx) * g;
        const ey = a.sy + (b.sy - a.sy) * g;
        const mx = (a.sx + ex) / 2 + (ey - a.sy) * 0.12;
        const my = (a.sy + ey) / 2 - (ex - a.sx) * 0.12;
        ctx.moveTo(a.sx, a.sy);
        ctx.quadraticCurveTo(mx, my, ex, ey);
      }
      ctx.stroke();
    }
    // Cross links between branches.
    ctx.strokeStyle = rgba(this.color("intent"), 0.08);
    ctx.lineWidth = clamp(base * 0.8, 0.5, 1.6);
    ctx.setLineDash([3, 5]);
    ctx.beginPath();
    for (const s of brain.synapses) {
      if (s.b.parent === s.a || s.a.parent === s.b) continue;
      const a = this.proj.get(s.a);
      const b = this.proj.get(s.b);
      if (!a || !b || a.scale === 0 || b.scale === 0) continue;
      ctx.moveTo(a.sx, a.sy);
      ctx.lineTo(b.sx, b.sy);
    }
    ctx.stroke();
    ctx.setLineDash([]);
    if (toolLinks.length) {
      ctx.strokeStyle = rgba(this.color("tool"), 0.11);
      ctx.lineWidth = clamp(base * 0.6, 0.4, 1.2);
      ctx.beginPath();
      for (const [a, b] of toolLinks) {
        ctx.moveTo(a.sx, a.sy);
        ctx.lineTo(b.sx, b.sy);
      }
      ctx.stroke();
    }
    for (const [a, b, heat, key] of hot) {
      ctx.strokeStyle = rgba(this.color(key), 0.65 * heat);
      ctx.lineWidth = clamp(base * 2.2, 1, 3.5) * (0.6 + heat * 0.6);
      ctx.beginPath();
      ctx.moveTo(a.sx, a.sy);
      ctx.lineTo(b.sx, b.sy);
      ctx.stroke();
    }

    // Shock waves from findings, errors, new structure and user turns.
    for (const sh of brain.shocks) {
      const p = this.proj.get(sh.n);
      if (!p || p.scale === 0) continue;
      const u = sh.age / sh.life;
      const r = Math.pow(u, 0.55) * 46 * sh.size * p.scale + 2;
      ctx.strokeStyle = rgba(this.color(sh.tone), (1 - u) * 0.55);
      ctx.lineWidth = 1.5 + (1 - u) * 2.5;
      ctx.beginPath();
      ctx.arc(p.sx, p.sy, r, 0, Math.PI * 2);
      ctx.stroke();
    }

    // Neurons. Additive blending makes draw order irrelevant, so no depth sort.
    const t = brain.time;
    for (const n of brain.neurons.values()) {
      const p = this.proj.get(n);
      if (!p || p.scale === 0) continue;
      if (p.sx < -80 || p.sy < -80 || p.sx > fullW + 80 || p.sy > h + 80) continue;
      const f = this.fog(p);
      let energy = n.energy;
      if (n.kind === "intent" && n.state === "running")
        energy = Math.max(energy, 0.35 + 0.25 * Math.sin(t * 3 + n.depth));
      if (n.kind === "tool" && n.state === "running") energy = Math.max(energy, 0.5);
      if (n === brain.core) energy = Math.max(energy, 0.4 + 0.15 * Math.sin(t * 1.7));
      const dim = DIM_STATES.has(n.state) ? 0.55 : 1;
      const size = BASE_SIZE[n.kind] * (1 + n.priority * 0.03) * (0.25 + 0.75 * n.growth);
      const r = size * p.scale * (1 + energy * 0.45);
      const key = this.neuronColor(n);
      const R = Math.min(150, r * (4 + energy * 1.8));
      if (R < 1.3) {
        ctx.globalAlpha = f * dim * 0.8;
        ctx.fillStyle = rgba(this.color(key), 1);
        ctx.fillRect(p.sx - 0.6, p.sy - 0.6, 1.2, 1.2);
        continue;
      }
      ctx.globalAlpha = clamp(f * dim * (0.45 + 0.55 * Math.min(1, 0.35 + energy)), 0, 1);
      ctx.drawImage(this.sprite(key), p.sx - R, p.sy - R, R * 2, R * 2);
      if (r > 1.2) {
        const c = Math.min(60, r * (0.9 + energy * 0.5));
        ctx.globalAlpha = clamp(f * dim * (0.5 + energy * 0.5), 0, 1);
        ctx.drawImage(this.sprite(key, true), p.sx - c, p.sy - c, c * 2, c * 2);
      }
    }

    // Signals in flight: a bright head with a short trail.
    const pa: Projected = { sx: 0, sy: 0, scale: 0, depth: 0 };
    const pb: Projected = { sx: 0, sy: 0, scale: 0, depth: 0 };
    for (const s of brain.signals) {
      const from = s.path[s.seg];
      const to = s.path[s.seg + 1];
      if (!from || !to) continue;
      const u = s.t;
      const u0 = Math.max(0, u - 0.3);
      project(cam, from.x + (to.x - from.x) * u, from.y + (to.y - from.y) * u, from.z + (to.z - from.z) * u, w, h, pa);
      project(
        cam,
        from.x + (to.x - from.x) * u0,
        from.y + (to.y - from.y) * u0,
        from.z + (to.z - from.z) * u0,
        w,
        h,
        pb,
      );
      if (pa.scale === 0) continue;
      const col = this.color(s.tone);
      const f = this.fog(pa);
      ctx.globalAlpha = 1;
      ctx.strokeStyle = rgba(col, 0.75 * f);
      ctx.lineWidth = clamp(pa.scale * 2.6 * s.size, 1, 4);
      ctx.beginPath();
      ctx.moveTo(pb.sx, pb.sy);
      ctx.lineTo(pa.sx, pa.sy);
      ctx.stroke();
      const R = clamp(pa.scale * 15 * s.size, 4, 26);
      ctx.globalAlpha = f;
      ctx.drawImage(this.sprite(s.tone, true), pa.sx - R, pa.sy - R, R * 2, R * 2);
    }

    // Labels and the selection ring.
    ctx.globalCompositeOperation = "source-over";
    ctx.globalAlpha = 1;
    const labels: Array<[Neuron, Projected, number]> = [];
    for (const n of brain.neurons.values()) {
      const p = this.proj.get(n);
      if (!p || p.scale === 0) continue;
      let rank = 0;
      if (n === this.selected) rank = 100;
      else if (n === this.hovered) rank = 90;
      else if (n.kind === "core") rank = 60;
      else if (n.kind === "finding") rank = 50;
      else if (n.kind === "goal") rank = 45;
      else if (n.kind !== "tool" && n.energy > 0.8) rank = 30 + n.energy;
      else if (n.kind === "tool" && n.energy > 1.2 && p.scale > base * 1.6) rank = 20;
      else if (n.kind === "intent" && p.scale > base * 1.4) rank = 10;
      if (rank > 0) labels.push([n, p, rank]);
    }
    labels.sort((a, b) => b[2] - a[2]);
    ctx.textBaseline = "middle";
    const placed: Array<[number, number, number, number]> = [];
    let drawn = 0;
    for (const [n, p, rank] of labels) {
      if (drawn >= 16) break;
      const text = shortLabel(n);
      const big = rank >= 90;
      ctx.font = `${big ? 500 : 400} ${big ? 12 : 11}px ${this.font}`;
      const tw = ctx.measureText(text).width;
      // Keep labels clear of the activity list on the right.
      const x = p.sx + 10 + tw > w - 6 ? p.sx - 10 - tw : p.sx + 10;
      const y = p.sy;
      if (y < 52 && rank < 90) continue; // keep the counters readable
      if (placed.some(([ax, ay, aw, ah]) => x < ax + aw && x + tw > ax && y - 7 < ay + ah && y + 7 > ay)) continue;
      placed.push([x, y - 7, tw, 14]);
      const f = rank >= 90 ? 1 : this.fog(p);
      ctx.fillStyle = `rgba(8,6,16,${0.55 * f})`;
      ctx.fillRect(x - 4, y - 8, tw + 8, 16);
      ctx.fillStyle = `rgba(244,242,255,${0.92 * f})`;
      ctx.fillText(text, x, y + 0.5);
      drawn++;
    }
    if (this.selected) {
      const p = this.proj.get(this.selected);
      if (p && p.scale > 0) {
        const r = BASE_SIZE[this.selected.kind] * p.scale * 1.9 + 6;
        ctx.strokeStyle = rgba(this.color("core"), 0.9);
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

  private pick(x: number, y: number): Neuron | null {
    let best: Neuron | null = null;
    let bestD = Infinity;
    for (const n of this.brain.neurons.values()) {
      const p = this.proj.get(n);
      if (!p || p.scale === 0) continue;
      const r = Math.max(n.kind === "tool" ? 5 : 9, BASE_SIZE[n.kind] * p.scale * 1.6);
      const d = Math.hypot(p.sx - x, p.sy - y);
      // Prefer structural neurons when a tool neuron is about as close.
      const score = d + (n.kind === "tool" ? 3 : 0);
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
        this.onHover(n ? { x, y, kind: n.kind, label: shortLabel(n) } : null);
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
      this.lastInteract = this.brain.time;
      if (d.pan) {
        const s = (Math.min(this.w - this.inset, this.h) * 0.9) / this.cam.dist;
        const cy = Math.cos(this.cam.yaw);
        const sy = Math.sin(this.cam.yaw);
        const cp = Math.cos(this.cam.pitch);
        const sp = Math.sin(this.cam.pitch);
        this.focus = null;
        this.goal.tx -= (dx * cy + dy * -sp * sy) / s;
        this.goal.ty -= (dy * cp) / s;
        this.goal.tz -= (dx * -sy + dy * -sp * cy) / s;
      } else {
        this.cam.yaw += dx * 0.006;
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
        const n = this.pick(x, y);
        this.select(n);
      }
    };
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      this.lastInteract = this.brain.time;
      this.goal.dist = clamp(this.goal.dist * Math.exp(e.deltaY * 0.0012), 30, 9000);
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

function cam_rel(depth: number, dist: number): number {
  return depth / Math.max(1, dist);
}

function infoFor(n: Neuron): SelectedInfo {
  return {
    id: n.id,
    kind: n.kind,
    label: n.kind === "core" ? t("Planner and main agent turns that aren't tied to one intent.") : n.label,
    state: n.state,
    priority: n.priority,
    fires: n.fires,
    tools: n.tools,
    lastSummary: n.lastSummary,
    parentLabel: n.parent ? shortLabel(n.parent) : "",
  };
}

function rgbCss(c: RGB) {
  return `rgb(${c[0]},${c[1]},${c[2]})`;
}

export function BrainView({ taskId, className }: { taskId: string; className?: string }) {
  const wrapRef = React.useRef<HTMLDivElement>(null);
  const canvasRef = React.useRef<HTMLCanvasElement>(null);
  const probeRef = React.useRef<HTMLDivElement>(null);
  const rendererRef = React.useRef<BrainRenderer | null>(null);
  const [palette, setPalette] = React.useState<Record<ColorKey, RGB>>(FALLBACK);
  const [phase, setPhase] = React.useState<"loading" | "replay" | "ready" | "error">("loading");
  const [live, setLive] = React.useState<"connecting" | "live" | "offline">("connecting");
  const [stats, setStats] = React.useState<BrainStats>({
    neurons: 0,
    synapses: 0,
    toolCalls: 0,
    findings: 0,
    running: 0,
    perMinute: 0,
  });
  const [ticker, setTicker] = React.useState<BrainTick[]>([]);
  const [selected, setSelected] = React.useState<SelectedInfo | null>(null);
  const [hover, setHover] = React.useState<HoverInfo | null>(null);
  const [autoRotate, setAutoRotate] = React.useState(true);
  const [follow, setFollow] = React.useState(false);
  const [paused, setPaused] = React.useState(false);
  const [fullscreen, setFullscreen] = React.useState(false);
  const [empty, setEmpty] = React.useState(false);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    const canvas = canvasRef.current;
    const wrap = wrapRef.current;
    const probe = probeRef.current;
    if (!canvas || !wrap || !probe) return;
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const brain = new Brain({ maxSignals: reduced ? 60 : 500 });
    const renderer = new BrainRenderer(canvas, brain, reduced);
    rendererRef.current = renderer;
    setAutoRotate(renderer.autoRotate);
    const pal = readPalette(probe);
    renderer.setPalette(pal);
    setPalette(pal);
    renderer.onSelect = (n) => setSelected(n ? infoFor(n) : null);
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
    renderer.fit(false);
    renderer.start();

    let alive = true;
    let es: EventSource | null = null;
    const timers: ReturnType<typeof setTimeout>[] = [];
    const recent: number[] = [];
    let graphTimer: ReturnType<typeof setTimeout> | undefined;
    let loadingGraph = false;
    let graphSig = "";

    const pushTick = (a: Activity, n: Neuron) => {
      if (a.kind === "usage") return;
      const tick: BrainTick = {
        seq: a.seq,
        ts: a.ts,
        kind: a.kind,
        worker: a.worker,
        tool: a.tool,
        summary: (a.summary || "").replace(/\s+/g, " ").trim(),
        isError: !!a.is_error,
        neuronId: n.id,
      };
      setTicker((prev) => (prev.some((p) => p.seq === tick.seq) ? prev : [tick, ...prev].slice(0, 9)));
    };

    const loadGraph = async (animate: boolean) => {
      if (loadingGraph) return;
      loadingGraph = true;
      try {
        const g = await api.explorationGraph(taskId);
        if (!alive) return;
        const nodes = g.nodes ?? [];
        const edges = g.edges ?? [];
        const sig = JSON.stringify([nodes.map((n) => [n.id, n.state, n.priority]), edges.length]);
        if (sig === graphSig) return;
        graphSig = sig;
        const gi = graphIn(nodes, edges);
        brain.syncGraph(gi.nodes, gi.edges, animate);
      } finally {
        loadingGraph = false;
      }
    };
    const scheduleGraph = () => {
      if (graphTimer) clearTimeout(graphTimer);
      graphTimer = setTimeout(() => void loadGraph(true).catch(() => undefined), 1500);
    };

    const ingestLive = (a: Activity) => {
      const n = brain.ingest(toActivityIn(a), true);
      renderer.fired(n);
      pushTick(a, n);
      const ts = Date.parse(a.ts);
      if (Number.isFinite(ts)) recent.push(ts);
      if (a.kind === "tool_result" || a.kind === "result" || a.kind === "intent" || a.kind === "round") scheduleGraph();
    };

    void (async () => {
      try {
        await loadGraph(false);
        // Full recorded history builds the tool-call neurons without animation.
        const history: Activity[] = [];
        let cursor = 0;
        while (alive && history.length < HISTORY_MAX) {
          const page = await api.activity(taskId, { since: cursor, limit: HISTORY_PAGE });
          history.push(...page.items);
          if (!page.items.length || page.cursor <= cursor) break;
          cursor = page.cursor;
          if (page.items.length < HISTORY_PAGE) break;
        }
        if (!alive) return;
        for (const a of history) if (a.seq > cursor) cursor = a.seq;
        const tail = history.filter((a) => a.kind !== "usage").slice(-REPLAY_TAIL);
        const tailSeqs = new Set(tail.map((a) => a.seq));
        for (const a of history) if (!tailSeqs.has(a.seq)) brain.ingest(toActivityIn(a), false);
        brain.settle(220);
        renderer.fit(false);
        setEmpty(brain.structuralCount <= 1 && history.length === 0);
        // Replay the most recent recorded events so the brain wakes up the way it last ran.
        if (tail.length) {
          setPhase("replay");
          tail.forEach((a, i) => {
            timers.push(
              setTimeout(
                () => {
                  if (!alive) return;
                  const n = brain.ingest(toActivityIn(a), true);
                  pushTick(a, n);
                  if (i === tail.length - 1) setPhase("ready");
                },
                300 + i * 85,
              ),
            );
          });
        } else setPhase("ready");

        es = new EventSource(
          sseUrl(`/api/exploration/activity/stream?task=${encodeURIComponent(taskId)}&since=${cursor}`),
        );
        es.onopen = () => alive && setLive("live");
        es.onerror = () => alive && setLive("offline");
        es.onmessage = (e) => {
          let a: Activity;
          try {
            a = JSON.parse(e.data) as Activity;
          } catch {
            return;
          }
          ingestLive(a);
          setEmpty(false);
        };
      } catch (err) {
        if (!alive) return;
        setPhase("error");
        setError((err as Error).message || t("Failed to load"));
      }
    })();

    const stopPoll = setVisibleInterval(() => loadGraph(true).catch(() => undefined), 10000);
    const statTimer = setInterval(() => {
      const now = Date.now();
      while (recent.length && now - recent[0] > 60000) recent.shift();
      let findings = 0;
      let running = 0;
      for (const n of brain.neurons.values()) {
        if (n.kind === "finding") findings++;
        else if (n.kind === "intent" && n.state === "running") running++;
      }
      setStats({
        neurons: brain.neurons.size,
        synapses: brain.synapses.length + brain.neurons.size - 1,
        toolCalls: brain.toolCalls,
        findings,
        running,
        perMinute: recent.length,
      });
      if (renderer.selected) {
        if (brain.neurons.has(renderer.selected.id)) setSelected(infoFor(renderer.selected));
        else renderer.select(null);
      }
    }, 700);

    const onFs = () => setFullscreen(document.fullscreenElement === wrap.parentElement);
    document.addEventListener("fullscreenchange", onFs);

    return () => {
      alive = false;
      es?.close();
      for (const id of timers) clearTimeout(id);
      if (graphTimer) clearTimeout(graphTimer);
      stopPoll();
      clearInterval(statTimer);
      cancelAnimationFrame(hoverRaf);
      ro.disconnect();
      document.removeEventListener("fullscreenchange", onFs);
      renderer.destroy();
      rendererRef.current = null;
    };
  }, [taskId]);

  const r = rendererRef;
  const toggleFullscreen = () => {
    const el = wrapRef.current?.parentElement;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void el.requestFullscreen?.();
  };
  const focusNeuron = (id: string) => {
    const n = r.current?.brain.neurons.get(id);
    if (n) r.current?.select(n);
  };
  const timeFmt = React.useMemo(
    () => new Intl.DateTimeFormat(intlLocale, { hour: "2-digit", minute: "2-digit", second: "2-digit" }),
    [],
  );

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
          aria-label={t("Live brain of this task: exploration graph and agent activity")}
          tabIndex={0}
          onKeyDown={(e) => {
            if (e.key === "Escape" || e.key === "f") {
              r.current?.select(null);
              r.current?.fit();
            }
          }}
        />
      </div>

      {/* Status and counts */}
      <div className="pointer-events-none absolute top-3 left-3 flex max-w-[60%] flex-col gap-2">
        <div className="flex items-center gap-2 text-xs">
          <span
            className={cn(
              "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 font-medium backdrop-blur",
              live === "live"
                ? "border-emerald-400/30 bg-emerald-400/10 text-emerald-300"
                : live === "connecting"
                  ? "border-white/15 bg-white/5 text-white/70"
                  : "border-amber-400/30 bg-amber-400/10 text-amber-300",
            )}
          >
            <span
              className={cn(
                "size-1.5 rounded-full",
                live === "live"
                  ? "animate-pulse bg-emerald-400"
                  : live === "connecting"
                    ? "bg-white/60"
                    : "bg-amber-400",
              )}
            />
            {live === "live" ? t("Live") : live === "connecting" ? t("Connecting") : t("Reconnecting")}
          </span>
          {phase === "replay" && (
            <span className="rounded-full border border-white/15 bg-white/5 px-2 py-0.5 text-white/70 backdrop-blur">
              {t("Replaying the last recorded steps")}
            </span>
          )}
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-white/55 tabular-nums">
          <Stat label={t("Neurons")} value={stats.neurons} />
          <Stat label={t("Links")} value={stats.synapses} />
          <Stat label={t("Tool calls")} value={stats.toolCalls} />
          <Stat label={t("Running intents")} value={stats.running} />
          <Stat label={t("Findings")} value={stats.findings} tone={stats.findings ? "text-rose-300" : undefined} />
          <Stat label={t("Events / min")} value={stats.perMinute} />
        </div>
      </div>

      {/* Activity ticker */}
      <div className="pointer-events-none absolute top-3 right-3 bottom-16 hidden w-64 flex-col gap-1 overflow-hidden md:flex">
        {ticker.map((tk, i) => (
          <button
            key={tk.seq}
            type="button"
            onClick={() => focusNeuron(tk.neuronId)}
            className="pointer-events-auto rounded-md border border-white/10 bg-black/35 px-2.5 py-1.5 text-left backdrop-blur transition-colors hover:border-white/25 hover:bg-black/50"
            style={{ opacity: Math.max(0.25, 1 - i * 0.06) }}
          >
            <div className="flex items-center gap-1.5 text-[10px] text-white/50">
              <span className="size-1.5 shrink-0 rounded-full" style={{ background: rgbCss(palette[tickTone(tk)]) }} />
              <span className="font-medium text-white/75">{tk.tool || kindLabel(tk.kind)}</span>
              <span className="truncate">{tk.worker}</span>
              <span className="ml-auto shrink-0 tabular-nums">{safeTime(timeFmt, tk.ts)}</span>
            </div>
            {tk.summary && (
              <div className="mt-0.5 line-clamp-1 text-[11px] leading-snug text-white/80">{tk.summary}</div>
            )}
          </button>
        ))}
      </div>

      {/* Legend */}
      <div className="pointer-events-none absolute bottom-3 left-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-white/60">
        {LEGEND.map((k) => (
          <span key={k} className="inline-flex items-center gap-1.5">
            <span
              className="size-2 rounded-full"
              style={{ background: rgbCss(palette[k]), boxShadow: `0 0 8px ${rgbCss(palette[k])}` }}
            />
            {KIND_LABEL[k]}
          </span>
        ))}
        <span className="hidden text-white/35 lg:inline">
          {t("Drag to turn · Shift-drag to pan · Scroll to zoom · Click a neuron to fly in")}
        </span>
      </div>

      {/* Controls */}
      <div className="absolute right-3 bottom-3 flex items-center gap-1">
        <IconToggle
          label={t("Follow activity")}
          active={follow}
          onClick={() => {
            const v = !follow;
            setFollow(v);
            if (r.current) r.current.follow = v;
          }}
        >
          <Navigation />
        </IconToggle>
        <IconToggle
          label={t("Auto-rotate")}
          active={autoRotate}
          onClick={() => {
            const v = !autoRotate;
            setAutoRotate(v);
            if (r.current) r.current.autoRotate = v;
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
          label={t("Fit the whole brain")}
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
          className="pointer-events-none absolute z-10 max-w-64 rounded-md border border-white/15 bg-black/70 px-2 py-1 text-[11px] text-white backdrop-blur"
          style={{ left: hover.x + 14, top: hover.y + 14 }}
        >
          <span className="text-white/55">{KIND_LABEL[hover.kind]} · </span>
          {hover.label}
        </div>
      )}

      {/* Selected neuron */}
      {selected && (
        <div className="absolute bottom-14 left-3 z-10 w-80 max-w-[calc(100%-1.5rem)] rounded-lg border border-white/15 bg-black/60 p-3 text-white shadow-2xl backdrop-blur-md">
          <div className="flex items-center gap-2 text-[11px] text-white/60">
            <span className="size-2 rounded-full" style={{ background: rgbCss(palette[selected.kind]) }} />
            <span className="font-medium text-white/85">{KIND_LABEL[selected.kind]}</span>
            {selected.id !== CORE_ID && !selected.id.startsWith("tool:") && <span>#{selected.id}</span>}
            {selected.state && <span className="rounded bg-white/10 px-1.5 py-px">{selected.state}</span>}
            <button
              type="button"
              className="ml-auto rounded px-1 text-white/50 hover:bg-white/10 hover:text-white"
              onClick={() => r.current?.select(null, false)}
              aria-label={t("Close")}
            >
              ×
            </button>
          </div>
          <p className="mt-1.5 line-clamp-4 text-xs leading-snug text-white/90">{selected.label}</p>
          <div className="mt-2 grid grid-cols-3 gap-2 text-[11px] text-white/55 tabular-nums">
            <div>
              <div className="text-white/85">{selected.fires}</div>
              {t("Events")}
            </div>
            <div>
              <div className="text-white/85">{selected.tools}</div>
              {t("Tool calls")}
            </div>
            <div>
              <div className="text-white/85">{selected.priority}</div>
              {t("Priority")}
            </div>
          </div>
          {selected.parentLabel && (
            <p className="mt-2 truncate text-[11px] text-white/50">
              {t("Branches from")} <span className="text-white/75">{selected.parentLabel}</span>
            </p>
          )}
          {selected.lastSummary && (
            <p className="mt-1 line-clamp-3 border-t border-white/10 pt-1.5 font-mono text-[11px] leading-snug text-white/70">
              {selected.lastSummary}
            </p>
          )}
        </div>
      )}

      {phase === "loading" && <Centered>{t("Loading the task's brain…")}</Centered>}
      {phase === "error" && <Centered>{error}</Centered>}
      {phase !== "loading" && phase !== "error" && empty && (
        <Centered>{t("No activity yet. Start the task to watch its brain grow.")}</Centered>
      )}
    </div>
  );
}

function tickTone(tk: BrainTick): ColorKey {
  if (tk.isError) return "error";
  switch (tk.kind) {
    case "tool_use":
      return "tool";
    case "tool_result":
      return "return";
    case "thinking":
    case "text":
      return "think";
    case "result":
      return "done";
    case "user":
      return "user";
    case "llm_failover":
    case "intercept_request":
      return "error";
    default:
      return "grow";
  }
}

const KIND_TEXT: Record<string, string> = {
  tool_use: t("Tool call"),
  tool_result: t("Tool result"),
  thinking: t("Thinking"),
  text: t("Reply"),
  result: t("Turn finished"),
  user: t("User message"),
  intent: t("Intent"),
  round: t("Planner round"),
  llm_switch: t("Model switch"),
  llm_failover: t("Model failover"),
  intercept_request: t("Approval request"),
};

function kindLabel(kind: string): string {
  return KIND_TEXT[kind] ?? kind;
}

function safeTime(fmt: Intl.DateTimeFormat, ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? "" : fmt.format(d);
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <span>
      <span className={cn("font-medium text-white/90", tone)}>{value.toLocaleString(intlLocale)}</span> {label}
    </span>
  );
}

function IconToggle({
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

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-6 text-center text-sm text-white/60">
      {children}
    </div>
  );
}
