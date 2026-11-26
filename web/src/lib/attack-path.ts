// Attack paths of one task, built only from what Artex has already recorded:
//
// - the task's asset coverage graph (company → root domain → host → service →
//   endpoint), which says what contains what;
// - the task's findings and the assets each one was anchored to;
// - the exploration graph, which shows when the work that produced one finding
//   started from another finding (finding → … → intent → finding).
//
// A path runs from the task's entry point down the containment chain to the
// asset a finding affects, then to the finding. It continues to a second
// finding only when the exploration graph shows the first one led to it. The
// riskiest path is the chain with the highest summed risk, where risk is the
// finding's severity weighted by its triage status and verification. Nothing
// here probes or predicts anything: it re-draws recorded data.
//
// Kept free of imports and the DOM so the web test scripts can load it alone.

export type AssetKind = "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint" | "asset";
export type PathKind = "entry" | AssetKind | "finding";
export type Severity = "critical" | "high" | "medium" | "low";

export interface AssetIn {
  key: string;
  kind: string;
  label: string;
  in_scope?: boolean;
  tested?: boolean;
}
/** Containment link from a child asset to its parent, as the coverage graph returns it. */
export interface ContainIn {
  src: string;
  dst: string;
}
export interface FindingIn {
  id: string;
  name?: string;
  vulnclass?: string;
  severity: string;
  status: string;
  verification_status?: string;
  summary?: string;
  assets?: Array<{ id: string; type: string; label: string }>;
  inherited?: boolean;
}
export interface ExpNodeIn {
  id: string;
  type: string;
}
export interface ExpEdgeIn {
  src: string;
  dst: string;
  rel: string;
}
export interface AttackInput {
  assets: AssetIn[];
  contains: ContainIn[];
  findings: FindingIn[];
  graphNodes?: ExpNodeIn[];
  graphEdges?: ExpEdgeIn[];
}

export interface PathNode {
  id: string;
  kind: PathKind;
  label: string;
  parent: PathNode | null;
  children: PathNode[];
  /** Position along the path axis: 0 is the entry point. */
  depth: number;
  inScope: boolean;
  tested: boolean;
  // Findings only.
  findingId: string;
  severity: Severity | "";
  status: string;
  verification: string;
  summary: string;
  inherited: boolean;
  /** Severity × status × verification; 0 for closed or false-positive findings. */
  risk: number;
  /** The asset a finding hangs off (the entry point when it names none). */
  asset: PathNode | null;
  /** Findings whose work led to this one, from the exploration graph. */
  enabledBy: PathNode[];
  enables: PathNode[];
  // Assets: the findings attached directly, and live totals for the subtree.
  findings: PathNode[];
  subtreeRisk: number;
  worst: Severity | "";
  findingCount: number;
  // Best chain ending here.
  chainScore: number;
  best: PathNode | null;
  // Layout.
  x: number;
  y: number;
  z: number;
  angle: number;
}

export type PathEdgeKind = "contains" | "affects" | "enables";
export interface PathEdge {
  a: PathNode;
  b: PathNode;
  kind: PathEdgeKind;
}

export interface AttackChain {
  /** Findings in the order the work reached them. */
  findings: PathNode[];
  /** Every node from the entry point to the last finding. */
  hops: PathNode[];
  score: number;
  worst: Severity;
}

export interface AttackGraph {
  nodes: Map<string, PathNode>;
  entry: PathNode;
  edges: PathEdge[];
  findings: PathNode[];
  chains: AttackChain[];
  /** Findings that still carry risk (open, not a false positive). */
  active: number;
  /** Findings left out of paths: fixed, resolved, ignored, duplicates, false positives. */
  closed: number;
  assets: number;
  droppedAssets: number;
  maxDepth: number;
}

export const ENTRY_ID = "entry";
export const SEVERITY_WEIGHT: Record<Severity, number> = { critical: 10, high: 6, medium: 3, low: 1 };
const SEVERITY_ORDER: Severity[] = ["low", "medium", "high", "critical"];
const STATUS_FACTOR: Record<string, number> = {
  confirmed: 1,
  in_progress: 0.85,
  pending: 0.7,
  risk_accepted: 0.6,
  resolved: 0,
  fixed: 0,
  false_positive: 0,
  ignored: 0,
  duplicate: 0,
};
const ASSET_KINDS = new Set<string>(["company", "root_domain", "subdomain", "ip", "service", "app", "endpoint"]);
const CHAIN_RELS = new Set(["spawns", "derived_from", "yields", "proves"]);

function sevOf(s: string): Severity {
  return s === "critical" || s === "high" || s === "medium" || s === "low" ? s : "low";
}

function worse(a: Severity | "", b: Severity | ""): Severity | "" {
  if (!a) return b;
  if (!b) return a;
  return SEVERITY_ORDER.indexOf(a) >= SEVERITY_ORDER.indexOf(b) ? a : b;
}

/** Risk of one finding: severity weight × triage status × verification. */
export function findingRisk(f: { severity: string; status: string; verification_status?: string }): number {
  if (f.verification_status === "false_positive") return 0;
  const status = f.status in STATUS_FACTOR ? STATUS_FACTOR[f.status] : 0.7;
  const verified = f.verification_status === "verified" ? 1.25 : 1;
  return Math.round(SEVERITY_WEIGHT[sevOf(f.severity)] * status * verified * 100) / 100;
}

function blank(id: string, kind: PathKind, label: string): PathNode {
  return {
    id,
    kind,
    label,
    parent: null,
    children: [],
    depth: 0,
    inScope: true,
    tested: false,
    findingId: "",
    severity: "",
    status: "",
    verification: "",
    summary: "",
    inherited: false,
    risk: 0,
    asset: null,
    enabledBy: [],
    enables: [],
    findings: [],
    subtreeRisk: 0,
    worst: "",
    findingCount: 0,
    chainScore: 0,
    best: null,
    x: 0,
    y: 0,
    z: 0,
    angle: 0,
  };
}

function assetKind(kind: string): AssetKind {
  return ASSET_KINDS.has(kind) ? (kind as AssetKind) : "asset";
}

/** Deterministic order: higher chain score, then worse severity, then id. */
function rankFindings(a: PathNode, b: PathNode): number {
  if (b.chainScore !== a.chainScore) return b.chainScore - a.chainScore;
  const s = SEVERITY_ORDER.indexOf(b.severity || "low") - SEVERITY_ORDER.indexOf(a.severity || "low");
  if (s) return s;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

export function buildAttackGraph(
  input: AttackInput,
  opts: { maxAssets?: number; maxChains?: number } = {},
): AttackGraph {
  const maxAssets = opts.maxAssets ?? 1500;
  const maxChains = opts.maxChains ?? 6;
  const nodes = new Map<string, PathNode>();
  const entry = blank(ENTRY_ID, "entry", "Entry point");
  nodes.set(ENTRY_ID, entry);

  // Containment: child → parent, ignoring links to unknown or self.
  const assetIn = new Map<string, AssetIn>();
  for (const a of input.assets) if (a.key && !assetIn.has(a.key) && a.key !== ENTRY_ID) assetIn.set(a.key, a);
  const parentOf = new Map<string, string>();
  for (const e of input.contains) {
    if (e.src === e.dst || parentOf.has(e.src)) continue;
    if (assetIn.has(e.src) && assetIn.has(e.dst)) parentOf.set(e.src, e.dst);
  }

  // Large scopes keep every asset on the way to a finding, then fill up in order.
  const findingAssetKeys = new Set<string>();
  for (const f of input.findings) for (const a of f.assets ?? []) findingAssetKeys.add(`a:${a.id}`);
  let keep: Set<string>;
  if (assetIn.size > maxAssets) {
    keep = new Set();
    for (const key of findingAssetKeys) {
      let k: string | undefined = key;
      let guard = 0;
      while (k && assetIn.has(k) && !keep.has(k) && guard++ < 64) {
        keep.add(k);
        k = parentOf.get(k);
      }
    }
    for (const key of assetIn.keys()) {
      if (keep.size >= maxAssets) break;
      keep.add(key);
    }
    // A kept child needs its parent kept too.
    for (const key of [...keep]) {
      let k = parentOf.get(key);
      let guard = 0;
      while (k && !keep.has(k) && guard++ < 64) {
        keep.add(k);
        k = parentOf.get(k);
      }
    }
  } else keep = new Set(assetIn.keys());

  for (const key of keep) {
    const a = assetIn.get(key);
    if (!a) continue;
    const n = blank(key, assetKind(a.kind), a.label || key);
    n.inScope = a.in_scope !== false;
    n.tested = !!a.tested;
    nodes.set(key, n);
  }
  const attach = (child: PathNode, parent: PathNode) => {
    child.parent = parent;
    parent.children.push(child);
  };
  for (const key of keep) {
    const n = nodes.get(key);
    if (!n) continue;
    // Walk up to make sure the parent chain ends at the entry point, not in a loop.
    let p = parentOf.get(key);
    const seen = new Set<string>([key]);
    let ok = !!p && nodes.has(p);
    while (ok && p) {
      if (seen.has(p)) {
        ok = false;
        break;
      }
      seen.add(p);
      p = parentOf.get(p);
      if (p && !nodes.has(p)) break;
    }
    const parentKey = parentOf.get(key);
    attach(n, ok && parentKey ? (nodes.get(parentKey) as PathNode) : entry);
  }

  // Depth of every asset along the containment chain.
  const queue: PathNode[] = [entry];
  for (let i = 0; i < queue.length; i++) {
    const n = queue[i];
    for (const c of n.children) {
      c.depth = n.depth + 1;
      queue.push(c);
    }
  }

  // Findings, each on the deepest asset it names.
  const findings: PathNode[] = [];
  for (const f of input.findings) {
    const id = `f:${f.id}`;
    if (!f.id || nodes.has(id)) continue;
    const n = blank(id, "finding", f.name || f.vulnclass || `Finding ${f.id}`);
    n.findingId = f.id;
    n.severity = sevOf(f.severity);
    n.status = f.status || "";
    n.verification = f.verification_status || "";
    n.summary = f.summary || "";
    n.inherited = !!f.inherited;
    n.risk = findingRisk(f);
    let asset: PathNode | null = null;
    for (const a of f.assets ?? []) {
      const cand = nodes.get(`a:${a.id}`);
      if (cand && cand.kind !== "finding" && (!asset || cand.depth > asset.depth)) asset = cand;
    }
    if (!asset && f.assets?.length) {
      // Named an asset the coverage graph doesn't hold (outside scope, or trimmed).
      const a = f.assets[0];
      const key = `a:${a.id}`;
      asset = blank(key, assetKind(a.type), a.label || key);
      asset.inScope = false;
      asset.depth = 1;
      attach(asset, entry);
      nodes.set(key, asset);
    }
    asset = asset ?? entry;
    n.asset = asset;
    asset.findings.push(n);
    nodes.set(id, n);
    findings.push(n);
  }

  // Chains from the exploration graph: the nearest findings upstream of each finding.
  const byGraphId = new Map<string, PathNode>();
  for (const n of findings) byGraphId.set(n.findingId, n);
  if (input.graphNodes && input.graphEdges && findings.length > 1) {
    const isFinding = new Set<string>();
    for (const g of input.graphNodes) if (g.type === "finding") isFinding.add(g.id);
    const up = new Map<string, string[]>();
    for (const e of input.graphEdges) {
      if (!CHAIN_RELS.has(e.rel)) continue;
      const list = up.get(e.dst);
      if (list) list.push(e.src);
      else up.set(e.dst, [e.src]);
    }
    for (const f of findings) {
      const seen = new Set<string>([f.findingId]);
      const stack = [...(up.get(f.findingId) ?? [])];
      let steps = 0;
      while (stack.length && steps++ < 20000) {
        const u = stack.pop() as string;
        if (seen.has(u)) continue;
        seen.add(u);
        const pred = byGraphId.get(u);
        if (pred && isFinding.has(u)) {
          if (!f.enabledBy.includes(pred)) {
            f.enabledBy.push(pred);
            pred.enables.push(f);
          }
          continue; // the nearest finding upstream; its own chain carries on from there
        }
        for (const s of up.get(u) ?? []) if (!seen.has(s)) stack.push(s);
      }
      f.enabledBy.sort((a, b) => (a.id < b.id ? -1 : 1));
    }
  }

  // Finding depth: past its asset, and past every finding that led to it.
  const placed = new Set<PathNode>();
  const onStack = new Set<PathNode>();
  const placeFinding = (f: PathNode): number => {
    if (placed.has(f)) return f.depth;
    onStack.add(f);
    let d = (f.asset?.depth ?? 0) + 1;
    for (const p of f.enabledBy) if (!onStack.has(p)) d = Math.max(d, placeFinding(p) + 1);
    onStack.delete(f);
    placed.add(f);
    f.depth = d;
    return d;
  };
  for (const f of findings) placeFinding(f);

  // Best chain ending at each finding. Closed findings neither score nor pass a chain on.
  const scored = new Set<PathNode>();
  const scoring = new Set<PathNode>();
  const score = (f: PathNode): number => {
    if (scored.has(f)) return f.chainScore;
    if (f.risk <= 0) {
      scored.add(f);
      f.chainScore = 0;
      return 0;
    }
    scoring.add(f);
    let best: PathNode | null = null;
    let bestScore = 0;
    for (const p of f.enabledBy) {
      if (scoring.has(p) || p.risk <= 0) continue;
      const s = score(p);
      if (s > bestScore || (s === bestScore && best && rankFindings(p, best) < 0)) {
        best = p;
        bestScore = s;
      }
    }
    scoring.delete(f);
    scored.add(f);
    f.best = best;
    f.chainScore = Math.round((f.risk + bestScore) * 100) / 100;
    return f.chainScore;
  };
  for (const f of findings) score(f);

  // Live totals up the containment chain.
  let active = 0;
  for (const f of findings) {
    if (f.risk <= 0) continue;
    active++;
    let a: PathNode | null = f.asset;
    while (a) {
      a.subtreeRisk += f.risk;
      a.findingCount++;
      a.worst = worse(a.worst, f.severity);
      a = a.parent;
    }
  }

  // Top chains, each ending on a finding no higher chain already covers.
  const chains: AttackChain[] = [];
  const covered = new Set<PathNode>();
  for (const end of findings.filter((f) => f.risk > 0).sort(rankFindings)) {
    if (chains.length >= maxChains) break;
    if (covered.has(end)) continue;
    const list: PathNode[] = [];
    const seen = new Set<PathNode>();
    for (let n: PathNode | null = end; n && !seen.has(n); n = n.best) {
      seen.add(n);
      list.unshift(n);
    }
    for (const n of list) covered.add(n);
    const lead: PathNode[] = [];
    for (let a: PathNode | null = list[0].asset; a && a !== entry; a = a.parent) lead.unshift(a);
    let worst: Severity | "" = "";
    for (const n of list) worst = worse(worst, n.severity);
    chains.push({
      findings: list,
      hops: [entry, ...lead, ...list],
      score: end.chainScore,
      worst: worst || "low",
    });
  }

  const edges: PathEdge[] = [];
  let maxDepth = 0;
  for (const n of nodes.values()) {
    maxDepth = Math.max(maxDepth, n.depth);
    if (n.kind === "finding") {
      if (n.asset) edges.push({ a: n.asset, b: n, kind: "affects" });
      for (const p of n.enabledBy) edges.push({ a: p, b: n, kind: "enables" });
    } else if (n.parent) edges.push({ a: n.parent, b: n, kind: "contains" });
  }

  return {
    nodes,
    entry,
    edges,
    findings,
    chains,
    active,
    closed: findings.length - active,
    assets: nodes.size - 1 - findings.length,
    droppedAssets: assetIn.size - keep.size,
    maxDepth,
  };
}

/** The chain that ends at a finding, following its best upstream finding. */
export function chainTo(g: AttackGraph, end: PathNode): AttackChain | null {
  if (end.kind !== "finding") return null;
  const hit = g.chains.find((c) => c.findings[c.findings.length - 1] === end);
  if (hit) return hit;
  const list: PathNode[] = [];
  const seen = new Set<PathNode>();
  for (let n: PathNode | null = end; n && !seen.has(n); n = n.best) {
    seen.add(n);
    list.unshift(n);
  }
  const lead: PathNode[] = [];
  for (let a: PathNode | null = list[0].asset; a && a !== g.entry; a = a.parent) lead.unshift(a);
  let worst: Severity | "" = "";
  for (const n of list) worst = worse(worst, n.severity);
  return { findings: list, hops: [g.entry, ...lead, ...list], score: end.chainScore, worst: worst || "low" };
}

function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return (h >>> 0) / 4294967295;
}

export const LAYER_GAP = 135;

/**
 * Deterministic 3D layout: a cone tree along the x axis. The entry point sits
 * on the axis at the left, each layer of the path is a ring further right, and
 * every branch keeps an angular sector sized by how many leaves it holds, so a
 * host's services and findings fan out next to it.
 */
export function layoutAttackGraph(g: AttackGraph): void {
  const kids = (n: PathNode): PathNode[] => n.children.concat(n.findings);
  // Leaf weights for sector sizes.
  const weight = new Map<PathNode, number>();
  const weigh = (n: PathNode, guard: number): number => {
    let w = 0;
    if (guard < 64) for (const c of kids(n)) w += weigh(c, guard + 1);
    w = Math.max(1, w);
    weight.set(n, w);
    return w;
  };
  weigh(g.entry, 0);

  const order = (a: PathNode, b: PathNode) =>
    b.subtreeRisk + b.risk - (a.subtreeRisk + a.risk) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  const sector = (n: PathNode, a0: number, a1: number, guard: number) => {
    n.angle = (a0 + a1) / 2;
    if (guard >= 64) return;
    const list = kids(n).sort(order);
    const total = list.reduce((s, c) => s + (weight.get(c) ?? 1), 0) || 1;
    // Keep a little air between sibling sectors.
    const span = a1 - a0;
    let at = a0;
    for (const c of list) {
      const part = (span * (weight.get(c) ?? 1)) / total;
      sector(c, at + part * 0.04, at + part * 0.96, guard + 1);
      at += part;
    }
  };
  sector(g.entry, 0, Math.PI * 2, 0);
  // Findings reached through another finding sit in that finding's sector.
  for (const f of g.findings) {
    if (f.best && f.best.asset === f.asset) f.angle = f.best.angle + (hash(f.id) - 0.5) * 0.12;
  }

  const perLayer = new Map<number, number>();
  for (const n of g.nodes.values()) perLayer.set(n.depth, (perLayer.get(n.depth) ?? 0) + 1);
  const ring = (d: number) => (d === 0 ? 0 : Math.max(64 + d * 46, ((perLayer.get(d) ?? 1) * 30) / (Math.PI * 2)));
  const shift = (g.maxDepth * LAYER_GAP) / 2;
  for (const n of g.nodes.values()) {
    const h = hash(n.id);
    const r = ring(n.depth) * (n.depth === 0 ? 0 : 0.9 + h * 0.2);
    n.x = n.depth * LAYER_GAP - shift + (n.depth ? (h - 0.5) * 22 : 0);
    n.y = r ? r * Math.cos(n.angle) : 0;
    n.z = r ? r * Math.sin(n.angle) : 0;
  }
}

/** Distance from the layout's centre that holds most of the graph. */
export function attackExtent(g: AttackGraph): number {
  let r = 60;
  for (const n of g.nodes.values()) r = Math.max(r, Math.hypot(n.x, n.y, n.z));
  return r;
}
