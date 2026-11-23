import {
  Activity,
  Ban,
  BellRing,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  FolderOpen,
  FolderSync,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Network,
  Plug,
  Radio,
  ScrollText,
  Settings2,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Wrench,
} from "lucide-react";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

// Grouped by the job the user is doing, not by implementation. Titles here are also the
// page h1s and browser tab titles (see navigation/route-meta.ts), so rename in one place.
export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    label: "Overview",
    items: [
      { id: "dashboard", title: "Dashboard", url: "/dashboard", icon: LayoutDashboard },
      { id: "chat", title: "Chat", url: "/chat", icon: MessageSquare },
    ],
  },
  {
    id: 2,
    label: "Testing",
    items: [
      { id: "tasks", title: "Tasks", url: "/function/tasks", icon: Target },
      { id: "findings", title: "Findings", url: "/function/findings", icon: Bug },
      { id: "assets", title: "Assets", url: "/function/assets", icon: Network },
      { id: "traffic", title: "Traffic", url: "/function/traffic", icon: Activity },
      { id: "commands", title: "Tool runs", url: "/function/commands", icon: Terminal },
      { id: "llm-records", title: "LLM calls", url: "/function/llm-records", icon: Radio },
    ],
  },
  {
    id: 3,
    label: "Guardrails",
    items: [
      // A live work queue (the agent is blocked until someone decides), so it leads its group
      // and carries the pending count; see use-pending-approvals.
      { id: "approvals", title: "Approvals", url: "/system/intercept/approvals", icon: ClipboardList },
      { id: "intercept", title: "Command rules", url: "/system/intercept", icon: ShieldAlert },
      { id: "asset-intercept", title: "Target blocklist", url: "/system/intercept/assets", icon: Ban },
    ],
  },
  {
    id: 4,
    label: "Data",
    items: [
      { id: "sync", title: "Asset sync", url: "/function/sync", icon: FolderSync },
      { id: "workspace", title: "Workspace", url: "/function/workspace", icon: FolderOpen },
    ],
  },
  {
    id: 5,
    label: "AI configuration",
    items: [
      { id: "llm", title: "Models", url: "/system/llm", icon: Brain },
      { id: "agents", title: "Agents", url: "/system/agents", icon: Bot },
      { id: "mcp", title: "MCP servers", url: "/system/mcp", icon: Plug },
      { id: "skills", title: "Skills", url: "/system/skills", icon: Sparkles },
      { id: "tools", title: "Tools", url: "/system/tools", icon: Wrench },
    ],
  },
  {
    id: 6,
    label: "Admin",
    items: [
      { id: "notify", title: "Notifications", url: "/system/notify", icon: BellRing },
      { id: "logs", title: "Logs", url: "/system/logs", icon: ScrollText },
      { id: "settings", title: "Settings", url: "/system/settings", icon: Settings2 },
    ],
  },
];
