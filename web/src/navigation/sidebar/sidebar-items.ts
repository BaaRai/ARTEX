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

import { t } from "@/i18n";

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
    label: t("Overview"),
    items: [
      { id: "dashboard", title: t("Dashboard"), url: "/dashboard", icon: LayoutDashboard },
      { id: "chat", title: t("Chat"), url: "/chat", icon: MessageSquare },
    ],
  },
  {
    id: 2,
    label: t("Testing"),
    items: [
      { id: "tasks", title: t("Tasks"), url: "/function/tasks", icon: Target },
      { id: "findings", title: t("Findings"), url: "/function/findings", icon: Bug },
      { id: "assets", title: t("Assets"), url: "/function/assets", icon: Network },
      { id: "traffic", title: t("Traffic"), url: "/function/traffic", icon: Activity },
      { id: "commands", title: t("Tool runs"), url: "/function/commands", icon: Terminal },
      { id: "llm-records", title: t("LLM calls"), url: "/function/llm-records", icon: Radio },
    ],
  },
  {
    id: 3,
    label: t("Guardrails"),
    items: [
      // A live work queue (the agent is blocked until someone decides), so it leads its group
      // and carries the pending count; see use-pending-approvals.
      { id: "approvals", title: t("Approvals"), url: "/system/intercept/approvals", icon: ClipboardList },
      { id: "intercept", title: t("Command rules"), url: "/system/intercept", icon: ShieldAlert },
      { id: "asset-intercept", title: t("Target blocklist"), url: "/system/intercept/assets", icon: Ban },
    ],
  },
  {
    id: 4,
    label: t("Data"),
    items: [
      { id: "sync", title: t("Asset sync"), url: "/function/sync", icon: FolderSync },
      { id: "workspace", title: t("Workspace"), url: "/function/workspace", icon: FolderOpen },
    ],
  },
  {
    id: 5,
    label: t("AI configuration"),
    items: [
      { id: "llm", title: t("Models"), url: "/system/llm", icon: Brain },
      { id: "agents", title: t("Agents"), url: "/system/agents", icon: Bot },
      { id: "mcp", title: t("MCP servers"), url: "/system/mcp", icon: Plug },
      { id: "skills", title: t("Skills"), url: "/system/skills", icon: Sparkles },
      { id: "tools", title: t("Tools"), url: "/system/tools", icon: Wrench },
    ],
  },
  {
    id: 6,
    label: t("Admin"),
    items: [
      { id: "notify", title: t("Notifications"), url: "/system/notify", icon: BellRing },
      { id: "logs", title: t("Logs"), url: "/system/logs", icon: ScrollText },
      { id: "settings", title: t("Settings"), url: "/system/settings", icon: Settings2 },
    ],
  },
];
