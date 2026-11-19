"use client";

import * as React from "react";

import Link from "next/link";

import {
  ArrowUpRightIcon,
  ChevronRightIcon,
  FileTextIcon,
  FlaskConicalIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
  Trash2Icon,
} from "lucide-react";
import { useTranslations } from "next-intl";

import { CopyButton } from "@/components/copy-button";
import { Markdown } from "@/components/markdown";
import { StatusBadge } from "@/components/status-badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { type Tone, toneClasses } from "@/lib/status";
import type {
  ActiveFindingRetest,
  Finding,
  FindingReportState,
  FindingStatus,
  Severity,
  VerificationStatus,
} from "@/lib/types";
import { useStatusMeta } from "@/lib/use-status-meta";
import { cn } from "@/lib/utils";

export const SEVERITIES: Severity[] = ["critical", "high", "medium", "low"];

export const FINDING_STATUSES: FindingStatus[] = [
  "pending",
  "in_progress",
  "confirmed",
  "resolved",
  "fixed",
  "false_positive",
  "ignored",
  "duplicate",
  "risk_accepted",
];

export const VERIFICATION_STATUSES: VerificationStatus[] = [
  "verified",
  "unverified",
  "blocked",
  "not_assessed",
  "false_positive",
];

export const FINDING_REPORT_STATES: FindingReportState[] = [
  "none",
  "submitted",
  "accepted",
  "rejected",
  "duplicate",
];

export const REPORT_STATE_META: Record<FindingReportState, { label: string; tone: Tone }> = {
  none: { label: "Not submitted", tone: "slate" },
  submitted: { label: "Submitted", tone: "amber" },
  accepted: { label: "Accepted", tone: "green" },
  rejected: { label: "Rejected", tone: "red" },
  duplicate: { label: "Duplicate report", tone: "neutral" },
};

export function ReportStateBadge({ state }: { state?: FindingReportState }) {
  const t = useTranslations("findingsComponents");
  const value = state ?? "none";
  const meta = REPORT_STATE_META[value] ?? REPORT_STATE_META.none;
  return (
    <span
      className={cn(
        "inline-flex w-fit items-center whitespace-nowrap rounded-md border px-2 py-0.5 font-medium text-xs",
        toneClasses[meta.tone],
      )}
    >
      {t.has(`findingsTable.reportState.${value}`) ? t(`findingsTable.reportState.${value}`) : meta.label}
    </span>
  );
}

// Verification outcome → label/tone, on the same tone tokens StatusBadge uses.
export const VERIFICATION_META: Record<VerificationStatus, { label: string; tone: Tone }> = {
  verified: { label: "Verified", tone: "green" },
  unverified: { label: "Unverified", tone: "slate" },
  blocked: { label: "Blocked", tone: "amber" },
  not_assessed: { label: "Not assessed", tone: "red" },
  false_positive: { label: "False positive", tone: "neutral" },
};

// VerificationBadge shows the automated verification outcome ("Verified L3" when a
// proof-of-exploitation level is declared). It renders nothing when the finding carries
// no verification status (in-task nodes, payloads from before the column existed).
export function VerificationBadge({ finding }: { finding: Pick<Finding, "verification_status" | "poe_level"> }) {
  const t = useTranslations("findingsComponents");
  const meta = finding.verification_status ? VERIFICATION_META[finding.verification_status] : undefined;
  if (!meta) return null;
  const metaLabel = t.has(`findingsTable.verification.${finding.verification_status}`)
    ? t(`findingsTable.verification.${finding.verification_status}`)
    : meta.label;
  const label =
    finding.verification_status === "verified" && finding.poe_level
      ? `${metaLabel} L${finding.poe_level}`
      : metaLabel;
  return (
    <span
      className={cn(
        "inline-flex w-fit items-center whitespace-nowrap rounded-md border px-2 py-0.5 font-medium text-xs",
        toneClasses[meta.tone],
      )}
    >
      {label}
    </span>
  );
}

export function useReportStateMeta() {
  const t = useTranslations("findingsComponents");
  return React.useCallback(
    (value: FindingReportState) => {
      const meta = REPORT_STATE_META[value] ?? REPORT_STATE_META.none;
      return t.has(`findingsTable.reportState.${value}`)
        ? { ...meta, label: t(`findingsTable.reportState.${value}`) }
        : meta;
    },
    [t],
  );
}

export function useVerificationMeta() {
  const t = useTranslations("findingsComponents");
  return React.useCallback(
    (value: VerificationStatus) => {
      const meta = VERIFICATION_META[value];
      return t.has(`findingsTable.verification.${value}`)
        ? { ...meta, label: t(`findingsTable.verification.${value}`) }
        : meta;
    },
    [t],
  );
}

export const UNASSIGNED_TASK = "__unassigned__";

// Inline edit buffer: the expanded row's name/category/severity.
export interface FindingEdit {
  name: string;
  vulnclass: string;
  severity: Severity;
}

export type FindingReport = { status: "loading" | "done" | "error"; text: string };

// Exploration node ids are only unique inside a task. Prefer the persisted
// finding id and otherwise namespace the node id by task so editing one group
// cannot update a similarly-named node in another expanded group.
export function findingRowKey(finding: Finding): string {
  if (finding.finding_id) return `finding:${finding.finding_id}`;
  return `node:${finding.task_id ?? UNASSIGNED_TASK}:${finding.id}`;
}

export function isSameFinding(left: Finding, right: Finding): boolean {
  return findingRowKey(left) === findingRowKey(right);
}

export function fmtTime(ts: string) {
  return new Date(ts).toLocaleString("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const COLUMN_COUNT = 10;

interface FindingsTableProps {
  items: Finding[];
  selectedIds: Set<string>;
  onToggleSelected: (id: string, checked: boolean) => void;
  onToggleSelectedPage: (ids: string[], checked: boolean) => void;
  /** The expanded row's findingRowKey; null = all collapsed. */
  expandedKey: string | null;
  onToggleRow: (finding: Finding) => void;
  reports: Record<string, FindingReport>;
  edit: FindingEdit | null;
  onEditChange: React.Dispatch<React.SetStateAction<FindingEdit | null>>;
  saving: boolean;
  onSave: (finding: Finding) => void;
  onStatusChange: (finding: Finding, next: FindingStatus) => void;
  onReportStateChange: (finding: Finding, next: FindingReportState) => void;
  onRetest: (finding: Finding) => void;
  activeRetests: Record<string, ActiveFindingRetest>;
  onDeepen: (finding: Finding) => void;
  onDelete: (finding: Finding) => void;
  /** The select-all checkbox's accessibility label; the flat and grouped views word it differently. */
  selectAllLabel?: string;
}

// FindingsTable is the findings list's table body; the flat view and the grouped-by-task view share the same row rendering
// (selection / inline expand / rename & status change / retest / deepen / delete), differing only in the outer container and pagination.
export function FindingsTable({
  items,
  selectedIds,
  onToggleSelected,
  onToggleSelectedPage,
  expandedKey,
  onToggleRow,
  reports,
  edit,
  onEditChange,
  saving,
  onSave,
  onStatusChange,
  onReportStateChange,
  onRetest,
  activeRetests,
  onDeepen,
  onDelete,
  selectAllLabel,
}: FindingsTableProps) {
  const t = useTranslations("findingsComponents");
  const statusMeta = useStatusMeta();
  const selectableIds = items.map((finding) => finding.finding_id).filter((id): id is string => Boolean(id));
  const selectedCount = selectableIds.filter((id) => selectedIds.has(id)).length;
  let headerChecked: boolean | "indeterminate" = false;
  if (selectableIds.length > 0 && selectedCount === selectableIds.length) {
    headerChecked = true;
  } else if (selectedCount > 0) {
    headerChecked = "indeterminate";
  }

  return (
    /* Fixed column widths keep expanded content from stretching the table; narrow screens scroll horizontally inside the table only. */
    <Table className="min-w-[68rem] table-fixed">
      <TableHeader>
        <TableRow>
          <TableHead className="w-8">
            <Checkbox
              checked={headerChecked}
              onCheckedChange={(checked) => onToggleSelectedPage(selectableIds, checked === true)}
              aria-label={selectAllLabel ?? t("findingsTable.selectAllPage")}
            />
          </TableHead>
          <TableHead className="w-8" />
          <TableHead className="w-20">{t("findingsTable.columns.severity")}</TableHead>
          <TableHead>{t("findingsTable.columns.vulnerability")}</TableHead>
          <TableHead className="w-44">{t("findingsTable.columns.asset")}</TableHead>
          <TableHead className="w-28">{t("findingsTable.columns.status")}</TableHead>
          <TableHead className="w-36">{t("findingsTable.columns.report")}</TableHead>
          <TableHead className="w-32">{t("findingsTable.columns.task")}</TableHead>
          <TableHead className="w-24">{t("findingsTable.columns.time")}</TableHead>
          <TableHead className="w-48">{t("findingsTable.columns.actions")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((f) => {
          const rowKey = findingRowKey(f);
          const open = expandedKey === rowKey;
          const retest = f.finding_id ? activeRetests[f.finding_id] : undefined;
          return (
            <React.Fragment key={rowKey}>
              <TableRow
                className="cursor-pointer"
                role="button"
                tabIndex={0}
                aria-expanded={open}
                onClick={() => onToggleRow(f)}
                onKeyDown={(event) => {
                  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
                  event.preventDefault();
                  onToggleRow(f);
                }}
              >
                <TableCell onClick={(e) => e.stopPropagation()}>
                  {f.finding_id && (
                    <Checkbox
                      checked={selectedIds.has(f.finding_id)}
                      onCheckedChange={(c) => onToggleSelected(f.finding_id as string, c === true)}
                      aria-label={t("findingsTable.selectOne")}
                    />
                  )}
                </TableCell>
                <TableCell>
                  <ChevronRightIcon
                    className={cn("size-4 text-muted-foreground transition-transform", open && "rotate-90")}
                  />
                </TableCell>
                <TableCell>
                  <StatusBadge domain="severity" value={f.severity} dot />
                </TableCell>
                <TableCell className="max-w-md">
                  <div className="flex min-w-0 flex-col gap-0.5">
                    {f.finding_id ? (
                      <Link
                        href={`/function/findings/detail?id=${f.finding_id}`}
                        onClick={(e) => e.stopPropagation()}
                        className="truncate font-medium hover:text-primary hover:underline"
                        title={t("findingsTable.viewDetails")}
                      >
                        {f.name || f.vulnclass || t("findingsTable.uncategorized")}
                      </Link>
                    ) : (
                      <span className="truncate font-medium">{f.name || f.vulnclass || t("findingsTable.uncategorized")}</span>
                    )}
                    <span className="truncate text-xs text-muted-foreground">{f.summary}</span>
                    <div className="flex flex-wrap items-center gap-1">
                      <Badge variant="outline">{t("findingsTable.trafficEvidence", { count: f.traffic_count ?? 0 })}</Badge>
                      <VerificationBadge finding={f} />
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  {f.assets && f.assets.length > 0 ? (
                    <div className="flex flex-wrap gap-1">
                      {f.assets.slice(0, 3).map((a) => (
                        <code
                          key={a.id}
                          className="max-w-full truncate rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                          title={`${a.type} · ${a.label}`}
                        >
                          {a.label}
                        </code>
                      ))}
                      {f.assets.length > 3 && (
                        <span className="text-xs text-muted-foreground">+{f.assets.length - 3}</span>
                      )}
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  {f.finding_id ? (
                    <Select value={f.status} onValueChange={(v) => onStatusChange(f, v as FindingStatus)}>
                      <SelectTrigger size="sm" className="h-7 w-full border-none px-1 shadow-none focus-visible:ring-0">
                        <StatusBadge domain="finding" value={f.status} dot />
                      </SelectTrigger>
                      <SelectContent position="popper" align="end">
                        {FINDING_STATUSES.map((st) => (
                          <SelectItem key={st} value={st}>
                            {statusMeta("finding", st).label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <StatusBadge domain="finding" value={f.status} dot />
                  )}
                </TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  {f.finding_id ? (
                    <Select
                      value={f.report_state ?? "none"}
                      onValueChange={(v) => onReportStateChange(f, v as FindingReportState)}
                    >
                      <SelectTrigger size="sm" className="h-7 w-full border-none px-1 shadow-none focus-visible:ring-0">
                        <ReportStateBadge state={f.report_state} />
                      </SelectTrigger>
                      <SelectContent position="popper" align="end">
                        {FINDING_REPORT_STATES.map((st) => (
                          <SelectItem key={st} value={st}>
                            {t.has(`findingsTable.reportState.${st}`) ? t(`findingsTable.reportState.${st}`) : REPORT_STATE_META[st].label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <ReportStateBadge state={f.report_state} />
                  )}
                </TableCell>
                <TableCell>
                  {f.task_id ? (
                    <Link
                      href={`/function/tasks/detail?id=${f.task_id}`}
                      onClick={(e) => e.stopPropagation()}
                      className="inline-flex max-w-full items-center gap-1 text-primary hover:underline"
                      title={f.task_description}
                    >
                      <span className="truncate">{f.task_description}</span>
                      <ArrowUpRightIcon className="size-3 shrink-0" />
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground tabular-nums">{fmtTime(f.ts)}</TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <div className="flex items-center gap-1">
                    {retest ? (
                      <Button asChild size="sm" variant="ghost">
                        <Link href={`/chat?c=${retest.conversation_id}`} title={t("findingsTable.retest.viewInProgress")}>
                          <Spinner data-icon="inline-start" />
                          {t("findingsTable.retest.running")}
                        </Link>
                      </Button>
                    ) : null}
                    {!retest && f.finding_id && !f.inherited ? (
                      <Button size="sm" variant="ghost" onClick={() => onRetest(f)} title={t("findingsTable.retest.startTitle")}>
                        <RotateCcwIcon data-icon="inline-start" />
                        {t("findingsTable.retest.start")}
                      </Button>
                    ) : null}
                    {f.finding_id && f.task_id && (
                      <Button size="sm" variant="ghost" onClick={() => onDeepen(f)}>
                        <FlaskConicalIcon data-icon="inline-start" />
                        {t("findingsTable.deepen")}
                      </Button>
                    )}
                    {f.finding_id && (
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button
                            size="icon"
                            variant="ghost"
                            className="size-7 text-muted-foreground hover:text-destructive"
                            aria-label={t("findingsTable.delete.ariaLabel")}
                          >
                            <Trash2Icon className="size-4" />
                          </Button>
                        </AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>{t("findingsTable.delete.title")}</AlertDialogTitle>
                            <AlertDialogDescription className="break-words">
                              {t.rich("findingsTable.delete.description", {
                                name: () => (
                                  <span className="break-all">
                                    {f.name || f.vulnclass || f.summary || `#${f.finding_id}`}
                                  </span>
                                ),
                              })}
                            </AlertDialogDescription>
                          </AlertDialogHeader>
                          <AlertDialogFooter>
                            <AlertDialogCancel>{t("findingsTable.delete.cancel")}</AlertDialogCancel>
                            <AlertDialogAction onClick={() => onDelete(f)}>{t("findingsTable.delete.confirm")}</AlertDialogAction>
                          </AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    )}
                  </div>
                </TableCell>
              </TableRow>
              {open && (
                <TableRow className="hover:bg-transparent">
                  {/* whitespace-normal overrides TableCell's default nowrap, otherwise the expanded area's text
                      is forced onto one line and overflows the cell. */}
                  <TableCell colSpan={COLUMN_COUNT} className="bg-muted/30 whitespace-normal">
                    <div className="flex flex-col gap-2 px-2 py-1">
                      {/* Inline edit: name/category/severity, editable and savable (standalone finding rows only). */}
                      {f.finding_id && edit && (
                        <div className="flex flex-wrap items-end gap-3 rounded-md border bg-background px-3 py-2.5">
                          <div className="flex min-w-[12rem] flex-1 flex-col gap-1">
                            <Label className="text-xs text-muted-foreground">{t("findingsTable.edit.vulnerability")}</Label>
                            <Input
                              value={edit.name}
                              onChange={(e) => onEditChange((s) => (s ? { ...s, name: e.target.value } : s))}
                              placeholder={t("findingsTable.edit.namePlaceholder")}
                            />
                          </div>
                          <div className="flex min-w-[10rem] flex-col gap-1">
                            <Label className="text-xs text-muted-foreground">{t("findingsTable.edit.category")}</Label>
                            <Input
                              value={edit.vulnclass}
                              onChange={(e) => onEditChange((s) => (s ? { ...s, vulnclass: e.target.value } : s))}
                              placeholder={t("findingsTable.edit.categoryPlaceholder")}
                            />
                          </div>
                          <div className="flex flex-col gap-1">
                            <Label className="text-xs text-muted-foreground">{t("findingsTable.edit.severity")}</Label>
                            <Select
                              value={edit.severity}
                              onValueChange={(v) => onEditChange((s) => (s ? { ...s, severity: v as Severity } : s))}
                            >
                              <SelectTrigger size="sm" className="w-28">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {SEVERITIES.map((sv) => (
                                  <SelectItem key={sv} value={sv}>
                                    {statusMeta("severity", sv).label}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </div>
                          <Button size="sm" disabled={saving} onClick={() => onSave(f)}>
                            {saving ? t("findingsTable.edit.saving") : t("findingsTable.edit.save")}
                          </Button>
                        </div>
                      )}
                      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                        <ShieldAlertIcon className="size-3.5" />
                        {t("findingsTable.evidence.label")}
                        {f.vulnclass && (
                          <span>
                            {t("findingsTable.evidence.type")}
                            <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{f.vulnclass}</code>
                          </span>
                        )}
                        {f.param_id && <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{f.param_id}</code>}
                        {f.assets && f.assets.length > 0 && (
                          <span className="flex flex-wrap items-center gap-1">
                            {t("findingsTable.evidence.assets")}
                            {f.assets.map((a) => (
                              <code key={a.id} className="rounded bg-muted px-1.5 py-0.5 font-mono" title={a.type}>
                                {a.label}
                              </code>
                            ))}
                          </span>
                        )}
                      </div>
                      <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs whitespace-pre-wrap">
                        {f.evidence}
                      </pre>

                      {/* Detailed report (Markdown): lazily loaded by finding_id on expand, viewable without entering the detail page. */}
                      {f.finding_id && (
                        <div className="flex flex-col gap-1.5">
                          <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
                            <span className="flex items-center gap-2">
                              <FileTextIcon className="size-3.5" />
                              {t("findingsTable.report.label")}
                            </span>
                            {reports[rowKey]?.status === "done" && reports[rowKey]?.text.trim() && (
                              <CopyButton
                                text={reports[rowKey]?.text}
                                successMessage={t("findingsTable.report.copied")}
                                variant="ghost"
                                className="h-6 px-2 text-xs"
                              />
                            )}
                          </div>
                          {(() => {
                            const rep = reports[rowKey];
                            if (!rep || rep.status === "loading")
                              return <p className="text-xs text-muted-foreground">{t("findingsTable.report.loading")}</p>;
                            if (rep.status === "error")
                              return <p className="text-xs text-muted-foreground">{t("findingsTable.report.error")}</p>;
                            if (!rep.text.trim())
                              return <p className="text-xs text-muted-foreground">{t("findingsTable.report.empty")}</p>;
                            return (
                              // break-words inherits into paragraphs/lists; pre additionally gets
                              // whitespace-pre-wrap so code blocks wrap too — otherwise long code lines/URLs
                              // would stretch the colSpan cell and push the whole table into horizontal scrolling.
                              <div className="min-w-0 break-words rounded-md border bg-background px-3 py-2 [&_pre]:whitespace-pre-wrap">
                                <Markdown text={rep.text} />
                              </div>
                            );
                          })()}
                        </div>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </React.Fragment>
          );
        })}
        {items.length === 0 && (
          <TableRow>
            <TableCell colSpan={COLUMN_COUNT} className="py-12 text-center text-sm text-muted-foreground">
              {t("findingsTable.empty")}
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}
