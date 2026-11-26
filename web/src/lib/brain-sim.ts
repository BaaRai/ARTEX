// Brain simulation for the task "Brain" view.
//
// Pure model: no DOM and no imports, so the same file runs in the browser and
// under `node --test` (scripts/brain-sim.test.mjs). Everything it holds comes
// from the task's real exploration graph and activity stream: graph nodes
// become structural neurons, each recorded tool call becomes a small neuron
// sprouting from the intent that made it, and each activity event fires a
// signal that travels down the parent chain to the neuron that acted.

export type NeuronKind =
  | "core"
  | "task"
  | "begin"
  | "goal"
  | "hint"
  | "intent"
  | "fact"
  | "finding"
  | "digest"
  | "tool";

export type SignalTone = "think" | "tool" | "return" | "error" | "finding" | "grow" | "user" | "done";

export interface GraphNodeIn {
  id: string;
  type: string;
  state: string;
  priority: number;
  label: string;
}

export interface GraphEdgeIn {
  src: string;
  dst: string;
  rel: string;
}

export interface ActivityIn {
  seq: number;
  intent_id?: string;
  worker: string;
  kind: string;
  tool?: string;
  tool_use_id?: string;
  is_error?: boolean;
  summary: string;
  ts: string;
}

export interface Neuron {
  id: string;
  kind: NeuronKind;
  state: string;
  priority: number;
  label: string;
  x: number;
  y: number;
  z: number;
  vx: number;
  vy: number;
  vz: number;
  parent: Neuron | null;
  depth: number;
  /** Activation, bumped by events and decaying back to 0. */
  energy: number;
  /** Sprout animation, 0 → 1. */
  growth: number;
  /** Glow on the link to the parent after a signal passes it. */
  linkHeat: number;
  error: boolean;
  fires: number;
  lastFire: number;
  /** Tool neurons attached to this neuron so far (slot counter). */
  tools: number;
  /** This tool neuron's slot around its parent. */
  slot: number;
  /** Last recorded activity summary for this neuron. */
  lastSummary: string;
}

export interface Synapse {
  a: Neuron;
  b: Neuron;
  rel: string;
  traffic: number;
}

export interface Signal {
  path: Neuron[];
  seg: number;
  t: number;
  speed: number;
  tone: SignalTone;
  size: number;
}

export interface Shock {
  n: Neuron;
  age: number;
  life: number;
  tone: SignalTone;
  size: number;
}

export interface BrainOptions {
  maxTools?: number;
  maxSignals?: number;
  seed?: number;
}

export const CORE_ID = "core";
const STRUCT_REL = new Set(["spawns", "derived_from", "yields", "proves", "covers"]);
const SHELL_BASE = 70;
const SHELL_STEP = 78;

function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Deterministic unit vector from an id, so layouts are stable across reloads. */
export function dirFromId(id: string): [number, number, number] {
  const h = hash(id);
  const u = (h & 0xffff) / 0xffff;
  const v = ((h >>> 16) & 0xffff) / 0xffff;
  const theta = u * Math.PI * 2;
  const z = v * 2 - 1;
  const r = Math.sqrt(Math.max(0, 1 - z * z));
  return [r * Math.cos(theta), z * 0.8, r * Math.sin(theta)];
}

export function neuronKind(type: string, state: string): NeuronKind {
  if (type === "fact" && state === "origin") return "begin";
  switch (type) {
    case "task":
    case "begin":
    case "goal":
    case "hint":
    case "intent":
    case "fact":
    case "finding":
    case "digest":
      return type;
    default:
      return "fact";
  }
}

function makeNeuron(id: string, kind: NeuronKind, label: string): Neuron {
  return {
    id,
    kind,
    state: "",
    priority: 0,
    label,
    x: 0,
    y: 0,
    z: 0,
    vx: 0,
    vy: 0,
    vz: 0,
    parent: null,
    depth: 0,
    energy: 0,
    growth: 1,
    linkHeat: 0,
    error: false,
    fires: 0,
    lastFire: -1e9,
    tools: 0,
    slot: 0,
    lastSummary: "",
  };
}

export class Brain {
  readonly neurons = new Map<string, Neuron>();
  readonly core: Neuron;
  synapses: Synapse[] = [];
  signals: Signal[] = [];
  shocks: Shock[] = [];
  time = 0;
  /** Layout heat: forces run while it is above a small threshold. */
  alpha = 1;
  toolCalls = 0;
  private readonly synIndex = new Map<string, Synapse>();
  private readonly toolOrder: string[] = [];
  private readonly pendingTools = new Map<string, Neuron>();
  private readonly maxTools: number;
  private readonly maxSignals: number;
  private readonly rand: () => number;

  constructor(opts: BrainOptions = {}) {
    this.maxTools = opts.maxTools ?? 2500;
    this.maxSignals = opts.maxSignals ?? 500;
    this.rand = mulberry32(opts.seed ?? 7);
    this.core = makeNeuron(CORE_ID, "core", "Agent core");
    this.core.state = "running";
    this.neurons.set(CORE_ID, this.core);
  }

  get structuralCount(): number {
    return this.neurons.size - this.toolOrder.length;
  }

  get toolCount(): number {
    return this.toolOrder.length;
  }

  /**
   * Bring the structural neurons in line with the exploration graph. New
   * neurons sprout next to their parent; with `animate`, each one also gets a
   * growth signal from the core so arrivals are visible. Returns what changed.
   */
  syncGraph(nodes: GraphNodeIn[], edges: GraphEdgeIn[], animate = true): { added: Neuron[]; removed: number } {
    const seen = new Set<string>([CORE_ID]);
    const added: Neuron[] = [];
    for (const n of nodes) {
      seen.add(n.id);
      const kind = neuronKind(n.type, n.state);
      let neuron = this.neurons.get(n.id);
      if (!neuron) {
        neuron = makeNeuron(n.id, kind, n.label);
        this.neurons.set(n.id, neuron);
        added.push(neuron);
      }
      neuron.kind = kind;
      neuron.state = n.state;
      neuron.priority = n.priority;
      neuron.label = n.label;
    }

    let removed = 0;
    for (const [id, neuron] of this.neurons) {
      if (neuron.kind === "tool" || seen.has(id)) continue;
      this.neurons.delete(id);
      removed++;
    }
    if (removed) {
      for (const id of this.toolOrder) {
        const tool = this.neurons.get(id);
        if (tool?.parent && !this.neurons.has(tool.parent.id)) tool.parent = this.core;
      }
    }

    this.rebuildTree(edges);

    for (const neuron of added) {
      const p = neuron.parent ?? this.core;
      const [dx, dy, dz] = dirFromId(neuron.id);
      const r = SHELL_BASE + neuron.depth * SHELL_STEP;
      if (p === this.core) {
        neuron.x = dx * r;
        neuron.y = dy * r;
        neuron.z = dz * r;
      } else {
        neuron.x = p.x + dx * 30;
        neuron.y = p.y + dy * 30;
        neuron.z = p.z + dz * 30;
      }
      if (animate) {
        neuron.growth = 0;
        this.emit(this.pathTo(neuron), neuron.kind === "finding" ? "finding" : "grow", 1.2);
        this.shock(neuron, neuron.kind === "finding" ? "finding" : "grow", neuron.kind === "finding" ? 2.2 : 1);
      }
    }
    if (added.length || removed) this.alpha = Math.max(this.alpha, animate ? 0.6 : 1);
    return { added, removed };
  }

  private rebuildTree(edges: GraphEdgeIn[]) {
    const out = new Map<string, string[]>();
    const indeg = new Map<string, number>();
    this.synapses = [];
    this.synIndex.clear();
    for (const e of edges) {
      const a = this.neurons.get(e.src);
      const b = this.neurons.get(e.dst);
      if (!a || !b || a === b || a.kind === "tool" || b.kind === "tool") continue;
      const key = `${a.id}|${b.id}`;
      if (this.synIndex.has(key)) continue;
      const syn: Synapse = { a, b, rel: e.rel, traffic: 0 };
      this.synapses.push(syn);
      this.synIndex.set(key, syn);
      if (!STRUCT_REL.has(e.rel)) continue;
      const list = out.get(a.id);
      if (list) list.push(b.id);
      else out.set(a.id, [b.id]);
      indeg.set(b.id, (indeg.get(b.id) ?? 0) + 1);
    }

    // Breadth-first from the core: graph roots hang off the core, and every
    // other neuron takes the first structural parent that reaches it.
    const assigned = new Set<string>([CORE_ID]);
    const queue: Neuron[] = [];
    const roots: Neuron[] = [];
    for (const n of this.neurons.values()) {
      if (n.kind === "tool" || n === this.core) continue;
      if (!indeg.get(n.id)) roots.push(n);
    }
    roots.sort((a, b) => rootRank(a) - rootRank(b) || a.id.localeCompare(b.id));
    for (const r of roots) {
      r.parent = this.core;
      r.depth = 1;
      assigned.add(r.id);
      queue.push(r);
    }
    while (queue.length) {
      const n = queue.shift() as Neuron;
      for (const id of out.get(n.id) ?? []) {
        if (assigned.has(id)) continue;
        const child = this.neurons.get(id);
        if (!child) continue;
        child.parent = n;
        child.depth = n.depth + 1;
        assigned.add(id);
        queue.push(child);
      }
    }
    // Neurons only reachable through a cycle.
    for (const n of this.neurons.values()) {
      if (n.kind === "tool" || assigned.has(n.id)) continue;
      n.parent = this.core;
      n.depth = 1;
    }
  }

  /** Chain of neurons from the core down to `n` (at most 9 hops, nearest end kept). */
  pathTo(n: Neuron): Neuron[] {
    const path: Neuron[] = [];
    let cur: Neuron | null = n;
    const guard = new Set<Neuron>();
    while (cur && !guard.has(cur)) {
      guard.add(cur);
      path.push(cur);
      cur = cur.parent;
    }
    path.reverse();
    return path.length > 9 ? path.slice(path.length - 9) : path;
  }

  private emit(path: Neuron[], tone: SignalTone, size = 1) {
    if (path.length < 2) return;
    if (this.signals.length >= this.maxSignals) this.signals.shift();
    this.signals.push({ path, seg: 0, t: 0, speed: 2.6 + this.rand() * 1.2, tone, size });
  }

  private shock(n: Neuron, tone: SignalTone, size = 1) {
    if (this.shocks.length > 80) this.shocks.shift();
    this.shocks.push({ n, age: 0, life: 1.1 + size * 0.4, tone, size });
  }

  private fire(n: Neuron, amount: number, summary: string) {
    n.energy = Math.min(3, n.energy + amount);
    n.fires++;
    n.lastFire = this.time;
    if (summary) n.lastSummary = summary;
  }

  /**
   * Apply one real activity event. With `animate` false the event only builds
   * structure (used for history on load). Returns the neuron that acted.
   */
  ingest(a: ActivityIn, animate = true): Neuron {
    const target = (a.intent_id ? this.neurons.get(a.intent_id) : undefined) ?? this.core;
    const summary = (a.summary ?? "").trim();
    switch (a.kind) {
      case "tool_use": {
        const id = `tool:${a.tool_use_id ? a.tool_use_id : a.seq}`;
        let tool = this.neurons.get(id);
        if (!tool) {
          tool = makeNeuron(id, "tool", a.tool ? a.tool : "tool");
          tool.parent = target;
          tool.depth = target.depth + 1;
          tool.slot = target.tools++;
          tool.state = "running";
          tool.x = target.x;
          tool.y = target.y;
          tool.z = target.z;
          tool.growth = animate ? 0 : 1;
          this.neurons.set(id, tool);
          this.toolOrder.push(id);
          this.toolCalls++;
          this.trimTools();
        }
        if (a.tool_use_id) this.pendingTools.set(a.tool_use_id, tool);
        this.fire(target, 0.6, summary);
        this.fire(tool, 1, summary);
        if (animate) this.emit(this.pathTo(tool), "tool", 1);
        return tool;
      }
      case "tool_result": {
        const tool = a.tool_use_id ? this.pendingTools.get(a.tool_use_id) : undefined;
        if (tool && this.neurons.has(tool.id)) {
          if (a.tool_use_id) this.pendingTools.delete(a.tool_use_id);
          tool.state = a.is_error ? "error" : "done";
          tool.error = !!a.is_error;
          this.fire(tool, 0.8, summary);
          this.fire(tool.parent ?? target, 0.5, "");
          if (animate) {
            this.emit([tool, tool.parent ?? target], a.is_error ? "error" : "return", 1);
            if (a.is_error) this.shock(tool, "error", 0.8);
          }
          return tool;
        }
        this.fire(target, 0.6, summary);
        if (animate && a.is_error) this.shock(target, "error", 0.8);
        return target;
      }
      case "thinking":
      case "text": {
        this.fire(target, a.kind === "thinking" ? 0.5 : 0.7, summary);
        if (animate) {
          if (target.parent) this.emit([target.parent, target], "think", 0.7);
          this.shock(target, "think", 0.5);
        }
        return target;
      }
      case "result": {
        this.fire(target, 1, summary);
        if (animate) {
          const path = this.pathTo(target).reverse();
          this.emit(path, "done", 1.1);
        }
        return target;
      }
      case "user": {
        this.fire(this.core, 1.2, summary);
        if (animate) this.shock(this.core, "user", 1.4);
        return this.core;
      }
      case "intent":
      case "round": {
        this.fire(target, 0.8, summary);
        if (animate) {
          this.emit(this.pathTo(target), "grow", 0.9);
          this.shock(target, "grow", 0.8);
        }
        return target;
      }
      case "llm_failover":
      case "intercept_request": {
        this.fire(target, 0.8, summary);
        if (animate) this.shock(target, "error", 1);
        return target;
      }
      default: {
        // usage, llm_switch and future kinds: a quiet pulse, no new structure.
        if (a.kind !== "usage") this.fire(target, 0.3, summary);
        return target;
      }
    }
  }

  private trimTools() {
    while (this.toolOrder.length > this.maxTools) {
      const id = this.toolOrder.shift() as string;
      this.neurons.delete(id);
    }
  }

  /** Advance the simulation by `dt` seconds. */
  step(dt: number) {
    const d = Math.min(dt, 0.05);
    this.time += d;
    if (this.alpha > 0.004) {
      this.layout();
      this.alpha *= Math.pow(0.35, d);
    }
    const decay = Math.exp(-d * 1.4);
    const heatDecay = Math.exp(-d * 2.2);
    for (const n of this.neurons.values()) {
      n.energy *= decay;
      n.linkHeat *= heatDecay;
      if (n.growth < 1) n.growth = Math.min(1, n.growth + d * 1.6);
      if (n.kind === "tool") {
        const [tx, ty, tz] = placeTool(n);
        const k = 1 - Math.exp(-d * 6);
        n.x += (tx - n.x) * k;
        n.y += (ty - n.y) * k;
        n.z += (tz - n.z) * k;
      }
    }
    for (const s of this.synapses) s.traffic *= heatDecay;

    const kept: Signal[] = [];
    for (const s of this.signals) {
      s.t += d * s.speed;
      while (s.t >= 1 && s.seg < s.path.length - 1) {
        s.t -= 1;
        const from = s.path[s.seg];
        const to = s.path[s.seg + 1];
        s.seg++;
        if (to.parent === from) to.linkHeat = 1;
        else if (from.parent === to) from.linkHeat = 1;
        const syn = this.synIndex.get(`${from.id}|${to.id}`) ?? this.synIndex.get(`${to.id}|${from.id}`);
        if (syn) syn.traffic = 1;
        to.energy = Math.min(3, to.energy + 0.25 * s.size);
      }
      if (s.seg < s.path.length - 1 && this.neurons.has(s.path[s.seg + 1].id)) kept.push(s);
    }
    this.signals = kept;

    const liveShocks: Shock[] = [];
    for (const sh of this.shocks) {
      sh.age += d;
      if (sh.age < sh.life) liveShocks.push(sh);
    }
    this.shocks = liveShocks;
  }

  private layout() {
    const list: Neuron[] = [];
    for (const n of this.neurons.values()) if (n.kind !== "tool") list.push(n);
    const a = this.alpha;
    const cell = 90;
    const grid = new Map<string, Neuron[]>();
    const keyOf = (x: number, y: number, z: number) =>
      `${Math.floor(x / cell)},${Math.floor(y / cell)},${Math.floor(z / cell)}`;
    for (const n of list) {
      const k = keyOf(n.x, n.y, n.z);
      const bucket = grid.get(k);
      if (bucket) bucket.push(n);
      else grid.set(k, [n]);
    }
    for (const n of list) {
      if (n === this.core) continue;
      let fx = 0;
      let fy = 0;
      let fz = 0;
      // Repulsion from neighbours in the 27 surrounding cells.
      const cx = Math.floor(n.x / cell);
      const cy = Math.floor(n.y / cell);
      const cz = Math.floor(n.z / cell);
      for (let i = -1; i <= 1; i++)
        for (let j = -1; j <= 1; j++)
          for (let k = -1; k <= 1; k++) {
            const bucket = grid.get(`${cx + i},${cy + j},${cz + k}`);
            if (!bucket) continue;
            for (const m of bucket) {
              if (m === n) continue;
              let dx = n.x - m.x;
              let dy = n.y - m.y;
              let dz = n.z - m.z;
              let d2 = dx * dx + dy * dy + dz * dz;
              if (d2 < 0.01) {
                const [ux, uy, uz] = dirFromId(n.id + m.id);
                dx = ux;
                dy = uy;
                dz = uz;
                d2 = 1;
              }
              if (d2 > cell * cell) continue;
              const f = 1400 / (d2 + 30);
              const inv = 1 / Math.sqrt(d2);
              fx += dx * inv * f;
              fy += dy * inv * f;
              fz += dz * inv * f;
            }
          }
      // Spring to the parent.
      const p = n.parent ?? this.core;
      {
        const dx = p.x - n.x;
        const dy = p.y - n.y;
        const dz = p.z - n.z;
        const dist = Math.sqrt(dx * dx + dy * dy + dz * dz) || 1;
        const rest = p === this.core ? SHELL_BASE : 62;
        const f = (dist - rest) * 0.06;
        fx += (dx / dist) * f;
        fy += (dy / dist) * f;
        fz += (dz / dist) * f;
      }
      // Pull onto the neuron's shell, so depth in the graph reads as distance from the core.
      {
        const r = Math.sqrt(n.x * n.x + n.y * n.y + n.z * n.z) || 1;
        const target = SHELL_BASE + (n.depth - 1) * SHELL_STEP + (n.kind === "finding" ? 26 : 0);
        const f = (target - r) * 0.08;
        fx += (n.x / r) * f;
        fy += (n.y / r) * f * 0.8;
        fz += (n.z / r) * f;
      }
      n.vx = (n.vx + fx * a) * 0.55;
      n.vy = (n.vy + fy * a) * 0.55;
      n.vz = (n.vz + fz * a) * 0.55;
    }
    // Weak pull along cross links (non-tree synapses).
    for (const s of this.synapses) {
      if (s.b.parent === s.a || s.a.parent === s.b) continue;
      const dx = s.b.x - s.a.x;
      const dy = s.b.y - s.a.y;
      const dz = s.b.z - s.a.z;
      const k = 0.004 * a;
      if (s.a !== this.core) {
        s.a.vx += dx * k;
        s.a.vy += dy * k;
        s.a.vz += dz * k;
      }
      if (s.b !== this.core) {
        s.b.vx -= dx * k;
        s.b.vy -= dy * k;
        s.b.vz -= dz * k;
      }
    }
    for (const n of list) {
      if (n === this.core) continue;
      n.x += n.vx;
      n.y += n.vy;
      n.z += n.vz;
    }
  }

  /** Settle the layout without rendering (used after the initial load). */
  settle(iterations = 160) {
    for (let i = 0; i < iterations && this.alpha > 0.004; i++) {
      this.layout();
      this.alpha *= 0.975;
    }
    for (const n of this.neurons.values()) {
      if (n.kind !== "tool") continue;
      const [x, y, z] = placeTool(n);
      n.x = x;
      n.y = y;
      n.z = z;
    }
  }

  /** Radius of a sphere around the origin that holds every neuron. */
  extent(): number {
    let r = SHELL_BASE;
    for (const n of this.neurons.values()) r = Math.max(r, Math.sqrt(n.x * n.x + n.y * n.y + n.z * n.z));
    return r;
  }
}

function rootRank(n: Neuron): number {
  switch (n.kind) {
    case "task":
    case "begin":
      return 0;
    case "goal":
      return 1;
    default:
      return 2;
  }
}

/**
 * Where a tool neuron sits: a tuft that grows outward from its parent, away
 * from the grandparent, with slots spiralling by the golden angle.
 */
export function placeTool(n: Neuron): [number, number, number] {
  const p = n.parent;
  if (!p) return [n.x, n.y, n.z];
  const g = p.parent;
  let ox = p.x - (g ? g.x : 0);
  let oy = p.y - (g ? g.y : 0);
  let oz = p.z - (g ? g.z : 0);
  let len = Math.sqrt(ox * ox + oy * oy + oz * oz);
  if (len < 1e-3) {
    [ox, oy, oz] = dirFromId(p.id);
    len = 1;
  }
  ox /= len;
  oy /= len;
  oz /= len;
  // Two vectors perpendicular to the outward direction.
  let ux = oy;
  let uy = -ox;
  let uz = 0;
  if (Math.abs(oz) > 0.9) {
    ux = 0;
    uy = oz;
    uz = -oy;
  }
  const ul = Math.sqrt(ux * ux + uy * uy + uz * uz) || 1;
  ux /= ul;
  uy /= ul;
  uz /= ul;
  const vx = oy * uz - oz * uy;
  const vy = oz * ux - ox * uz;
  const vz = ox * uy - oy * ux;
  const i = n.slot;
  const ang = i * 2.399963 + (hash(p.id) % 628) / 100;
  const spread = 7 + 4.2 * Math.sqrt(i);
  const reach = 16 + 5.5 * Math.sqrt(i);
  const c = Math.cos(ang) * spread;
  const s = Math.sin(ang) * spread;
  return [p.x + ox * reach + ux * c + vx * s, p.y + oy * reach + uy * c + vy * s, p.z + oz * reach + uz * c + vz * s];
}

export interface Camera {
  yaw: number;
  pitch: number;
  dist: number;
  tx: number;
  ty: number;
  tz: number;
}

export interface Projected {
  sx: number;
  sy: number;
  /** Pixels per world unit at this depth; 0 when behind the camera. */
  scale: number;
  /** Camera-space depth (larger is farther). */
  depth: number;
}

/** Perspective projection of a world point for a camera orbiting (tx, ty, tz). */
export function project(
  cam: Camera,
  x: number,
  y: number,
  z: number,
  w: number,
  h: number,
  out?: Projected,
): Projected {
  const o = out ?? { sx: 0, sy: 0, scale: 0, depth: 0 };
  const px = x - cam.tx;
  const py = y - cam.ty;
  const pz = z - cam.tz;
  const cy = Math.cos(cam.yaw);
  const sy = Math.sin(cam.yaw);
  const x1 = px * cy - pz * sy;
  const z1 = px * sy + pz * cy;
  const cp = Math.cos(cam.pitch);
  const sp = Math.sin(cam.pitch);
  const y2 = py * cp - z1 * sp;
  const z2 = py * sp + z1 * cp;
  const depth = z2 + cam.dist;
  const focal = Math.min(w, h) * 0.9;
  if (depth < 8) {
    o.sx = 0;
    o.sy = 0;
    o.scale = 0;
    o.depth = depth;
    return o;
  }
  const scale = focal / depth;
  o.sx = w / 2 + x1 * scale;
  o.sy = h / 2 + y2 * scale;
  o.scale = scale;
  o.depth = depth;
  return o;
}

/** Camera distance that fits a sphere of `radius` in a w×h viewport. */
export function fitDistance(radius: number, w: number, h: number): number {
  const focal = Math.min(w, h) * 0.9;
  return Math.max(120, (radius * focal) / (Math.min(w, h) * 0.46) + radius * 0.4);
}
