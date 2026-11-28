"use client";

import * as React from "react";

import Link from "next/link";

import {
  ArrowUpRightIcon,
  BugIcon,
  ChevronRightIcon,
  CircleCheckIcon,
  ClockIcon,
  DownloadIcon,
  FileUpIcon,
  InfoIcon,
  SearchIcon,
  ShieldAlertIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { FindingRetestDialog } from "@/components/finding-retest-dialog";
import { StatusBadge } from "@/components/status-badge";
import { TablePagination } from "@/components/table-pagination";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { api } from "@/lib/api";
import { formatShortDateTime } from "@/lib/format";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";
import { statusMeta } from "@/lib/status";
import type {
  ActiveFindingRetest,
  Finding,
  FindingGroup,
  FindingReportState,
  FindingStatus,
  Severity,
  VerificationStatus,
} from "@/lib/types";
import { cn } from "@/lib/utils";
import { useActiveCompany } from "@/stores/company/company-store";

import { AssetTree, assetPathOf } from "./_components/asset-tree";
import {
  FINDING_REPORT_STATES,
  FINDING_STATUSES,
  FindingsTable,
  findingRowKey,
  isSameFinding,
  REPORT_STATE_META,
  SEVERITIES,
  UNASSIGNED_TASK,
  VERIFICATION_META,
  VERIFICATION_STATUSES,
} from "./_components/findings-table";
import { setVisibleInterval } from "@/lib/polling";
import { t } from "@/i18n";

import {
  type ExportFormat,
  type ExportScope,
  FINDING_VIEWS,
  type FindingEditUpdate,
  type FindingListPreferences,
  type FindingView,
  createInitialFindingsPageState,
  findingsPageReducer,
} from "./_state/findings-page.reducer";

const FINDING_LIST_PREFERENCE_KEY = "artex_finding_list_preferences";

function findingGroupKey(group: FindingGroup) {
  return group.task_id === null ? UNASSIGNED_TASK : String(group.task_id);
}

export default function FindingsPage() {
  // The sidebar's company switcher narrows everything below on the server: the
  // flat and grouped lists, the asset tree and the stat cards all send `company`.
  // Undefined means all companies.
  const company = useActiveCompany();
  const [state, dispatch] = React.useReducer(findingsPageReducer, undefined, createInitialFindingsPageState);
  const { view, filters, flat, asset, grouped, exportDialog, deepen, retest, expandedRow, reports, requests } = state;
  const { severity, status, reportState, verificationStatus, vulnclass, task, sort, search, query } = filters;
  const { page: flatPage, size: flatPageSize } = state.pagination;
  const { tree: assetTree, scope: assetScope } = asset;
  const { groups, groupTotal, expanded: expandedGroups, findings: groupFindings } = grouped;
  const { page, size: pageSize } = state.groupPagination;
  const { data: stats, loaded: statsLoaded } = state.stats;
  const { preferencesHydrated } = state;
  const selectedIds = state.selection;
  const bulkBusy = requests.bulkUpdate.pending;
  const { key: expanded, edit } = expandedRow;
  const saving = requests.save.pending;
  const exporting = requests.export.pending;
  const deepenFinding = deepen.finding;
  const deepening = requests.deepen.pending;
  const retestFinding = retest.finding;
  const activeRetests = retest.active;
  const activeRetestFingerprint = Object.values(activeRetests)
    .map((item) => item.id)
    .join(",");
  const retestRefreshVersion = React.useRef(0);
  const filterFingerprint = JSON.stringify([
    severity,
    status,
    reportState,
    verificationStatus,
    vulnclass,
    task,
    sort,
    query,
    company,
  ]);
  const activeFilterFingerprint = React.useRef(filterFingerprint);
  activeFilterFingerprint.current = filterFingerprint;

  // One lightweight request covers all rows/views, avoiding per-row fetches of full retest history; wait for the previous round to finish before polling again.
  React.useEffect(() => {
    let disposed = false;
    let failed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refreshRetests() {
      const version = retestRefreshVersion.current;
      try {
        const rows = await api.activeFindingRetests();
        if (disposed || version !== retestRefreshVersion.current) return;
        dispatch({
          type: "activeRetestsLoaded",
          active: Object.fromEntries(rows.map((item) => [item.finding_id, item])),
        });
        failed = false;
      } catch (error) {
        if (!disposed && !failed)
          toast.error(t("Failed to load retest status: {error}", { error: (error as Error).message }));
        failed = true;
      } finally {
        if (!disposed) timer = setTimeout(() => void refreshRetests(), 3000);
      }
    }
    void refreshRetests();
    return () => {
      disposed = true;
      clearTimeout(timer);
    };
  }, []);

  React.useEffect(() => {
    const raw = getLocalStorageValue(FINDING_LIST_PREFERENCE_KEY);
    let restoredView: FindingView | undefined;
    const restored: Partial<FindingListPreferences> = {};
    if (raw) {
      try {
        const parsed = JSON.parse(raw) as {
          view?: unknown;
          severity?: unknown;
          status?: unknown;
          reportState?: unknown;
          verificationStatus?: unknown;
          vulnclass?: unknown;
          task?: unknown;
          sort?: unknown;
        };
        if (FINDING_VIEWS.includes(parsed.view as FindingView)) restoredView = parsed.view as FindingView;
        if (parsed.severity === "all" || SEVERITIES.includes(parsed.severity as Severity)) {
          restored.severity = parsed.severity as "all" | Severity;
        }
        if (parsed.status === "all" || FINDING_STATUSES.includes(parsed.status as FindingStatus)) {
          restored.status = parsed.status as "all" | FindingStatus;
        }
        if (parsed.reportState === "all" || FINDING_REPORT_STATES.includes(parsed.reportState as FindingReportState)) {
          restored.reportState = parsed.reportState as "all" | FindingReportState;
        }
        if (
          parsed.verificationStatus === "all" ||
          VERIFICATION_STATUSES.includes(parsed.verificationStatus as VerificationStatus)
        ) {
          restored.verificationStatus = parsed.verificationStatus as "all" | VerificationStatus;
        }
        if (typeof parsed.vulnclass === "string" && parsed.vulnclass) restored.vulnclass = parsed.vulnclass;
        if (typeof parsed.task === "string" && parsed.task) restored.task = parsed.task;
        if (parsed.sort === "severity" || parsed.sort === "time") restored.sort = parsed.sort;
      } catch {
        // Ignore malformed or legacy preferences and retain the defaults.
      }
    }
    dispatch({ type: "preferencesRestored", view: restoredView, filters: restored });
  }, []);

  React.useEffect(() => {
    if (!preferencesHydrated) return;
    setLocalStorageValue(
      FINDING_LIST_PREFERENCE_KEY,
      JSON.stringify({ view, severity, status, reportState, verificationStatus, vulnclass, task, sort }),
    );
  }, [preferencesHydrated, reportState, severity, sort, status, task, verificationStatus, view, vulnclass]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: the reducer reads the latest search; re-arm on every keystroke.
  React.useEffect(() => {
    const timer = window.setTimeout(() => dispatch({ type: "searchDebounced" }), 300);
    return () => window.clearTimeout(timer);
  }, [search]);

  // setFindings rewrites the same finding in both view caches at once, so switching views never shows stale state.
  const setFindings = React.useCallback((update: (current: Finding[]) => Finding[]) => {
    dispatch({ type: "findingsUpdated", update });
  }, []);

  const toggleSelected = React.useCallback((id: string, checked: boolean) => {
    dispatch({ type: "selectionToggled", ids: [id], checked });
  }, []);

  const toggleSelectedPage = React.useCallback((ids: string[], checked: boolean) => {
    dispatch({ type: "selectionToggled", ids, checked });
  }, []);

  // When opening the export dialog, default the scope to "Selected" if any rows are checked, else "Current filter".
  function openExport() {
    dispatch({ type: "exportOpened" });
  }

  async function doExport() {
    dispatch({ type: "request/started", key: "export" });
    try {
      await api.exportFindings({
        format: exportDialog.format,
        scope: exportDialog.scope,
        filters: {
          severity,
          status,
          reportState,
          verificationStatus,
          vulnclass,
          task,
          query,
          sort,
          assetScope: activeAssetScope ?? undefined,
          company,
        },
        ids: [...selectedIds],
      });
      dispatch({ type: "exportDialogChanged", patch: { open: false } });
      toast.success(t("Export download started"));
    } catch (e) {
      toast.error(t("Export failed: {error}", { error: (e as Error).message }));
    } finally {
      dispatch({ type: "request/finished", key: "export" });
    }
  }

  const flatRequest = React.useRef(0);
  const assetTreeRequest = React.useRef(0);
  const groupRequests = React.useRef<Record<string, number>>({});
  const groupsRequest = React.useRef(0);
  const flatStateRef = React.useRef(flat);
  const expandedGroupsRef = React.useRef(expandedGroups);
  const groupFindingsRef = React.useRef(groupFindings);
  const visibleGroupKeysRef = React.useRef<Set<string>>(new Set());
  flatStateRef.current = flat;
  expandedGroupsRef.current = expandedGroups;
  groupFindingsRef.current = groupFindings;
  visibleGroupKeysRef.current = new Set(groups.map(findingGroupKey));

  // A company switch drops the picked asset node: the node may belong to the
  // company we just left, and the tree is about to be reloaded under the new one.
  React.useEffect(() => {
    // company is a trigger here, not a value the effect reads.
    void company;
    dispatch({ type: "assetScopeSelected", scope: null });
  }, [company]);

  // The asset view's right-hand list = the flat list + a filter for the selected subtree, so both views share one list state.
  const activeAssetScope = view === "asset" ? assetScope : null;

  // loadFlat fetches the flat view's current page; the task filter goes to the backend, sharing the same filter set as the grouped view.
  const loadFlat = React.useCallback(async () => {
    const requestFilter = filterFingerprint;
    if (activeFilterFingerprint.current !== requestFilter) return;
    const request = ++flatRequest.current;
    dispatch({ type: "list/loadStarted" });
    try {
      const result = await api.findingsPage({
        page: flatPage,
        pageSize: flatPageSize,
        severity,
        status,
        reportState,
        verificationStatus,
        vulnclass,
        task,
        query,
        sort,
        assetScope: activeAssetScope ?? undefined,
        company,
      });
      if (request !== flatRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      dispatch({ type: "list/loaded", items: result.items, total: result.total });
      dispatch({ type: "list/loadFinished" });
    } catch {
      if (request !== flatRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      // Polling keeps the last successful snapshot visible; a failed first load keeps showing the spinner.
      dispatch({ type: "list/loadFailed" });
    }
  }, [
    activeAssetScope,
    company,
    filterFingerprint,
    flatPage,
    flatPageSize,
    severity,
    status,
    reportState,
    verificationStatus,
    vulnclass,
    task,
    query,
    sort,
  ]);

  // loadAssetTree fetches the whole asset tree. The tree doesn't change with the selected node (otherwise
  // picking one would collapse it into a single chain), so no picked node here — only the active company.
  const loadAssetTree = React.useCallback(async () => {
    const requestFilter = filterFingerprint;
    if (activeFilterFingerprint.current !== requestFilter) return;
    const request = ++assetTreeRequest.current;
    dispatch({ type: "assetTreeLoadStarted" });
    try {
      const result = await api.findingAssetTree({
        severity,
        status,
        reportState,
        verificationStatus,
        vulnclass,
        task,
        query,
        sort,
        company,
      });
      if (request !== assetTreeRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      dispatch({
        type: "assetTreeLoaded",
        nodes: result.nodes ?? [],
        findingTotal: result.finding_total ?? 0,
        truncated: Boolean(result.truncated),
        droppedKinds: result.dropped_kinds ?? [],
      });
    } catch (e) {
      if (request !== assetTreeRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      dispatch({ type: "assetTreeLoadFailed" });
      toast.error(t("Failed to load the asset tree: {error}", { error: (e as Error).message }));
    }
  }, [
    company,
    filterFingerprint,
    reportState,
    severity,
    status,
    verificationStatus,
    vulnclass,
    task,
    query,
    sort,
  ]);

  const refreshGroups = React.useCallback(async () => {
    const requestFilter = filterFingerprint;
    if (activeFilterFingerprint.current !== requestFilter) return;
    const request = ++groupsRequest.current;
    try {
      const result = await api.findingGroups({
        page,
        pageSize,
        severity,
        status,
        reportState,
        verificationStatus,
        vulnclass,
        task,
        query,
        sort,
        company,
      });
      if (request !== groupsRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      dispatch({
        type: "groupsLoaded",
        groups: result.items,
        groupTotal: result.total,
        findingTotal: result.finding_total,
      });
    } catch {
      // Polling keeps the last successful snapshot visible.
    }
  }, [
    company,
    filterFingerprint,
    page,
    pageSize,
    reportState,
    severity,
    status,
    verificationStatus,
    vulnclass,
    task,
    query,
    sort,
  ]);

  const loadGroup = React.useCallback(
    async (key: string, groupPage: number, groupPageSize: number) => {
      const request = (groupRequests.current[key] ?? 0) + 1;
      const requestFilter = filterFingerprint;
      if (activeFilterFingerprint.current !== requestFilter) return;
      groupRequests.current[key] = request;
      dispatch({ type: "groupLoadStarted", key, page: groupPage, pageSize: groupPageSize });
      try {
        const result = await api.findingsPage({
          page: groupPage,
          pageSize: groupPageSize,
          severity,
          status,
          reportState,
          verificationStatus,
          vulnclass,
          task: key,
          query,
          sort,
          company,
        });
        if (groupRequests.current[key] !== request || activeFilterFingerprint.current !== requestFilter) return;
        dispatch({
          type: "groupLoaded",
          key,
          items: result.items,
          total: result.total,
          page: result.page,
          pageSize: result.page_size,
        });
      } catch {
        if (groupRequests.current[key] !== request || activeFilterFingerprint.current !== requestFilter) return;
        dispatch({ type: "groupLoadFailed", key, page: groupPage, pageSize: groupPageSize });
      }
    },
    [company, filterFingerprint, reportState, severity, status, verificationStatus, vulnclass, query, sort],
  );

  React.useEffect(() => {
    for (const [key, state] of Object.entries(groupFindings)) {
      if (!state.loaded || state.loading) continue;
      const lastPage = Math.max(1, Math.ceil(state.total / state.pageSize));
      if (state.page > lastPage) void loadGroup(key, lastPage, state.pageSize);
    }
  }, [groupFindings, loadGroup]);

  const toggleGroup = React.useCallback(
    (key: string) => {
      const opening = !expandedGroups.has(key);
      dispatch({ type: "groupToggled", key, open: opening });
      const state = groupFindings[key];
      if (opening && !state?.loaded && !state?.loading) {
        void loadGroup(key, state?.page ?? 1, state?.pageSize ?? 10);
      }
    },
    [expandedGroups, groupFindings, loadGroup],
  );

  // Refresh the current view after an inline mutation: the flat view reloads its page, the grouped view refreshes group headers + the finding's group.
  const refreshAfterMutation = React.useCallback(
    (finding: Finding, removed = false) => {
      if (view === "asset") {
        // The asset view doesn't poll, so recount the tree after a mutation too.
        void loadFlat();
        void loadAssetTree();
        return;
      }
      if (view === "flat") {
        // When the last page is emptied, the out-of-range correction effect rolls the page number back and reloads.
        void loadFlat();
        return;
      }
      void refreshGroups();
      const key = finding.task_id ?? UNASSIGNED_TASK;
      const state = groupFindingsRef.current[key];
      if (state?.loaded) {
        const nextTotal = Math.max(0, state.total - (removed ? 1 : 0));
        const lastPage = Math.max(1, Math.ceil(nextTotal / state.pageSize));
        void loadGroup(key, Math.min(state.page, lastPage), state.pageSize);
      }
    },
    [loadAssetTree, loadFlat, loadGroup, refreshGroups, view],
  );

  // Reset every view's pagination and expansion when a shared finding filter changes.
  React.useEffect(() => {
    void filterFingerprint;
    // Filters change the tree too; the previously selected node may no longer be in it, so the reducer falls back to "All assets".
    dispatch({ type: "filterFingerprintChanged" });
  }, [filterFingerprint]);

  // Switching asset nodes means a different result set; go back to page one.
  React.useEffect(() => {
    void assetScope;
    dispatch({ type: "pagination/pageChanged", page: 1 });
  }, [assetScope]);

  // The asset tree is queried once on view entry / filter change (plus actively re-pulled by
  // refreshAfterMutation after this page mutates a finding); no polling.
  React.useEffect(() => {
    if (!preferencesHydrated || view !== "asset") return;
    void activeRetestFingerprint; // a finished retest may change asset counts under the status filter.
    void loadAssetTree();
  }, [activeRetestFingerprint, loadAssetTree, preferencesHydrated, view]);

  // Poll only the current view: the flat view refreshes its page, the grouped view refreshes group headers and every expanded group (their paginations are independent).
  // The asset view queries once (see the return below); its left tree is a navigation structure, no need to recompute it every 5 seconds.
  // Wait for preferences to hydrate before the first request, otherwise one fetch fires with the default view/filters for nothing.
  React.useEffect(() => {
    if (!preferencesHydrated) return;
    void activeRetestFingerprint; // including the non-polling asset view, refresh triage status after a retest finishes.
    const refresh = () => {
      if (view === "flat" || view === "asset") {
        if (!flatStateRef.current.loading) void loadFlat();
        return;
      }
      void refreshGroups();
      for (const key of expandedGroupsRef.current) {
        if (!visibleGroupKeysRef.current.has(key)) continue;
        const state = groupFindingsRef.current[key];
        if (state?.loaded && !state.loading) void loadGroup(key, state.page, state.pageSize);
      }
    };
    refresh();
    if (view === "asset") return;
    const stopPolling = setVisibleInterval(refresh, 5000);
    return stopPolling;
  }, [activeRetestFingerprint, loadFlat, loadGroup, preferencesHydrated, refreshGroups, view]);

  React.useEffect(() => {
    const lastPage = Math.max(1, Math.ceil(groupTotal / pageSize));
    if (page > lastPage)
      dispatch({ type: "groupPagination", action: { type: "pagination/pageChanged", page: lastPage } });
  }, [groupTotal, page, pageSize]);

  React.useEffect(() => {
    if (!flat.loaded) return;
    const lastPage = Math.max(1, Math.ceil(flat.total / flatPageSize));
    if (flatPage > lastPage) dispatch({ type: "pagination/pageChanged", page: lastPage });
  }, [flat.loaded, flat.total, flatPage, flatPageSize]);

  // Whole-table aggregates (stat cards + vuln-class options) — independent of the
  // current page, so they stay exact.
  React.useEffect(() => {
    let alive = true;
    const load = () => {
      api
        .findingStats(company)
        .then((s) => {
          if (alive) dispatch({ type: "statsLoaded", stats: s });
        })
        .catch(() => {
          // Keep the previous aggregate snapshot until the next poll.
        });
    };
    load();
    const stopPolling = setVisibleInterval(load, 5000);
    return () => {
      alive = false;
      stopPolling();
    };
  }, [company]);

  React.useEffect(() => {
    if (!statsLoaded) return;
    if (vulnclass !== "all" && !stats.vulnclasses.includes(vulnclass)) {
      dispatch({ type: "filterChanged", patch: { vulnclass: "all" } });
    }
    if (
      task !== "all" &&
      task !== UNASSIGNED_TASK &&
      !(stats.tasks ?? []).some((option) => String(option.id) === task)
    ) {
      dispatch({ type: "filterChanged", patch: { task: "all" } });
    }
  }, [stats, statsLoaded, task, vulnclass]);

  // updateStatus optimistically flips one finding's triage state, reverting on error.
  const updateStatus = React.useCallback(
    async (f: Finding, next: FindingStatus) => {
      if (!f.finding_id || next === f.status) return;
      const prev = f.status;
      setFindings((cur) => cur.map((x) => (isSameFinding(x, f) ? { ...x, status: next } : x)));
      try {
        await api.setFindingStatus(f.finding_id, next);
        toast.success(t('Marked as "{label}"', { label: statusMeta("finding", next).label }));
        // refresh stat cards (pending count) and drop the row if it no longer matches the status filter
        api
          .findingStats(company)
          .then((s) => dispatch({ type: "statsRefreshed", stats: s }))
          .catch(() => {
            // The row update remains valid even if the aggregate refresh fails.
          });
        if (status !== "all" && next !== status) {
          setFindings((cur) => cur.filter((x) => !isSameFinding(x, f)));
          dispatch({ type: "findingTotalsDecremented" });
        }
        refreshAfterMutation(f);
      } catch (e) {
        setFindings((cur) => cur.map((x) => (isSameFinding(x, f) ? { ...x, status: prev } : x)));
        toast.error(t("Update failed: {error}", { error: (e as Error).message }));
      }
    },
    [refreshAfterMutation, setFindings, status, company],
  );

  const updateReportState = React.useCallback(
    async (f: Finding, next: FindingReportState) => {
      if (!f.finding_id || next === (f.report_state ?? "none")) return;
      const prev = f.report_state ?? "none";
      setFindings((cur) => cur.map((x) => (isSameFinding(x, f) ? { ...x, report_state: next } : x)));
      try {
        await api.setFindingReportState(f.finding_id, next);
        toast.success(t('Report stage set to "{label}"', { label: REPORT_STATE_META[next].label }));
        api
          .findingStats(company)
          .then((s) => dispatch({ type: "statsRefreshed", stats: s }))
          .catch(() => {
            // The row update remains valid even if the aggregate refresh fails.
          });
        if (reportState !== "all" && next !== reportState) {
          setFindings((cur) => cur.filter((x) => !isSameFinding(x, f)));
          dispatch({ type: "findingTotalsDecremented" });
        }
        refreshAfterMutation(f);
      } catch (e) {
        setFindings((cur) => cur.map((x) => (isSameFinding(x, f) ? { ...x, report_state: prev } : x)));
        toast.error(t("Update failed: {error}", { error: (e as Error).message }));
      }
    },
    [refreshAfterMutation, reportState, setFindings, company],
  );

  // applyBulkStatus sets the same triage status on every selected finding, then
  // reloads the current view so rows hidden by the status filter disappear.
  const applyBulkStatus = React.useCallback(
    async (next: FindingStatus) => {
      if (selectedIds.size === 0 || bulkBusy) return;
      dispatch({ type: "request/started", key: "bulkUpdate" });
      try {
        const r = await api.bulkSetFindingStatus([...selectedIds], next);
        dispatch({ type: "selection/cleared" });
        const label = statusMeta("finding", next).label;
        toast.success(t('Marked {count} findings as "{label}"', { count: r.updated, label }));
        void loadFlat();
        if (view === "asset") void loadAssetTree();
        api
          .findingStats(company)
          .then((s) => dispatch({ type: "statsRefreshed", stats: s }))
          .catch(() => {
            // The list reload remains valid even if the aggregate refresh fails.
          });
      } catch (e) {
        toast.error(t("Bulk update failed: {error}", { error: (e as Error).message }));
      } finally {
        dispatch({ type: "request/finished", key: "bulkUpdate" });
      }
    },
    [bulkBusy, loadAssetTree, loadFlat, selectedIds, view, company],
  );

  const applyBulkReportState = React.useCallback(
    async (next: FindingReportState) => {
      if (selectedIds.size === 0 || bulkBusy) return;
      dispatch({ type: "request/started", key: "bulkUpdate" });
      try {
        const r = await api.bulkSetFindingReportState([...selectedIds], next);
        dispatch({ type: "selection/cleared" });
        const label = REPORT_STATE_META[next].label;
        toast.success(t('Set report stage of {count} findings to "{label}"', { count: r.updated, label }));
        void loadFlat();
        if (view === "asset") void loadAssetTree();
        api
          .findingStats(company)
          .then((s) => dispatch({ type: "statsRefreshed", stats: s }))
          .catch(() => {
            // The list reload remains valid even if the aggregate refresh fails.
          });
      } catch (e) {
        toast.error(t("Bulk update failed: {error}", { error: (e as Error).message }));
      } finally {
        dispatch({ type: "request/finished", key: "bulkUpdate" });
      }
    },
    [bulkBusy, loadAssetTree, loadFlat, selectedIds, view, company],
  );

  // The inline expanded detailed-report cache is keyed by the globally stable row key. report is long-form
  // Markdown the list query doesn't include, so it is fetched once by finding_id on expand; done with empty text = no report yet.
  // Inline editable buffer: the expanded row's name/category/severity, initialized from the row on expand and cleared on collapse.
  // Only one row expands at a time, so one buffer suffices.
  // FindingsTable's onEditChange takes a SetStateAction (it uses functional updates); the reducer applies it to the current buffer.
  const setEdit = React.useCallback((next: FindingEditUpdate) => dispatch({ type: "editChanged", edit: next }), []);

  // toggle expands/collapses a row; a fresh expand initializes the edit buffer and (if not yet fetched) pulls the report cache once by finding_id.
  const toggleRow = React.useCallback(
    (f: Finding) => {
      const key = findingRowKey(f);
      const willOpen = expanded !== key;
      if (!willOpen) {
        dispatch({ type: "rowCollapsed" });
        return;
      }
      dispatch({
        type: "rowExpanded",
        key,
        edit: { name: f.name ?? "", vulnclass: f.vulnclass, severity: f.severity },
      });
      if (!f.finding_id || reports[key]) return;
      const fid = f.finding_id;
      dispatch({ type: "reportLoadStarted", key });
      api
        .getFinding(fid)
        .then((full) => dispatch({ type: "reportLoaded", key, text: full.report ?? "" }))
        .catch(() => dispatch({ type: "reportLoadFailed", key }));
    },
    [expanded, reports],
  );

  // saveEdit saves the expanded row's name/category/severity, writes back to the local list and refreshes stats (the category dropdown / severity counts may change).
  const saveEdit = React.useCallback(
    async (f: Finding) => {
      if (!f.finding_id || !edit) return;
      dispatch({ type: "request/started", key: "save" });
      try {
        const updated = await api.updateFinding(f.finding_id, {
          name: edit.name.trim(),
          vulnclass: edit.vulnclass.trim(),
          severity: edit.severity,
        });
        setFindings((cur) =>
          cur.map((x) =>
            isSameFinding(x, f)
              ? { ...x, name: updated.name, vulnclass: updated.vulnclass, severity: updated.severity }
              : x,
          ),
        );
        toast.success(t("Saved"));
        api
          .findingStats(company)
          .then((s) => dispatch({ type: "statsRefreshed", stats: s }))
          .catch(() => {
            // The edit remains valid even if the aggregate refresh fails.
          });
        refreshAfterMutation(f);
      } catch (e) {
        toast.error(t("Save failed: {error}", { error: (e as Error).message }));
      } finally {
        dispatch({ type: "request/finished", key: "save" });
      }
    },
    [edit, refreshAfterMutation, setFindings, company],
  );

  // deleteFinding deletes one vulnerability (with confirmation): on success it removes it from the list, collapses the row, and refreshes stats.
  const deleteFinding = React.useCallback(
    async (f: Finding) => {
      if (!f.finding_id) return;
      try {
        await api.deleteFinding(f.finding_id);
        setFindings((cur) => cur.filter((x) => !isSameFinding(x, f)));
        const rowKey = findingRowKey(f);
        dispatch({ type: "findingDeleted", findingId: f.finding_id as string, rowKey });
        toast.success(t("Vulnerability deleted"));
        api
          .findingStats(company)
          .then((s) => dispatch({ type: "statsRefreshed", stats: s }))
          .catch(() => {
            // The deletion remains valid even if the aggregate refresh fails.
          });
        refreshAfterMutation(f, true);
      } catch (e) {
        toast.error(t("Delete failed: {error}", { error: (e as Error).message }));
      }
    },
    [refreshAfterMutation, setFindings, company],
  );

  const openDeepen = React.useCallback((f: Finding) => {
    dispatch({ type: "deepenOpened", finding: f });
  }, []);

  const setRetestFinding = React.useCallback((finding: Finding | null) => {
    dispatch({ type: "retestTargetChanged", finding });
  }, []);

  const setAssetScope = React.useCallback((scope: string | null) => {
    dispatch({ type: "assetScopeSelected", scope });
  }, []);

  async function submitDeepen() {
    if (!deepenFinding?.finding_id || !deepen.description.trim() || deepening) return;
    dispatch({ type: "request/started", key: "deepen" });
    try {
      const result = await api.deepenFinding(deepenFinding.finding_id, deepen.description.trim());
      toast.success(
        result.queued
          ? t("Deepen intent #{id} has entered the task queue", { id: result.intent_id })
          : t("Created high-priority worker intent #{id}", { id: result.intent_id }),
      );
      refreshAfterMutation(deepenFinding);
      dispatch({ type: "deepenClosed" });
    } catch (error) {
      toast.error(t("Submit failed: {error}", { error: (error as Error).message }));
    } finally {
      dispatch({ type: "request/finished", key: "deepen" });
    }
  }

  const statCards = [
    { label: t("Total findings"), value: stats.total, icon: BugIcon },
    { label: t("Pending"), value: stats.pending, tone: "text-amber-500", icon: ClockIcon },
    { label: t("Submitted"), value: stats.submitted, tone: "text-amber-500", icon: FileUpIcon },
    { label: t("Accepted"), value: stats.accepted, tone: "text-emerald-500", icon: CircleCheckIcon },
    { label: t("Critical"), value: stats.critical, tone: "text-rose-600", icon: ShieldAlertIcon },
    { label: t("High"), value: stats.high, tone: "text-red-500", icon: TriangleAlertIcon },
    { label: t("Medium"), value: stats.medium, tone: "text-amber-500", icon: TriangleAlertIcon },
    { label: t("Low"), value: stats.low, tone: "text-slate-500", icon: InfoIcon },
  ];

  // The "current filter" count in the export dialog: both views share the same filters, only the counting source differs.
  // The flat and asset views share the flat list state; the grouped view's figure comes from the group API's finding_total.
  const filteredTotal = view === "grouped" ? grouped.findingTotal : flat.total;
  const assetPath = React.useMemo(
    () => (view === "asset" ? assetPathOf(assetTree.nodes, assetScope) : []),
    [assetScope, assetTree.nodes, view],
  );

  const rowProps = {
    selectedIds,
    onToggleSelected: toggleSelected,
    onToggleSelectedPage: toggleSelectedPage,
    expandedKey: expanded,
    onToggleRow: toggleRow,
    reports,
    edit,
    onEditChange: setEdit,
    saving,
    onSave: saveEdit,
    onStatusChange: updateStatus,
    onReportStateChange: updateReportState,
    onRetest: setRetestFinding,
    activeRetests,
    onDeepen: openDeepen,
    onDelete: deleteFinding,
  };

  // The flat view and the asset view's right side are the same table + pagination, differing only in filter conditions.
  const flatListCard = (
    <Card className="gap-0 py-0">
      <CardContent className="px-0">
        {flat.loading && !flat.loaded ? (
          <div className="flex min-h-36 items-center justify-center">
            <Spinner />
          </div>
        ) : (
          <>
            <FindingsTable items={flat.items} selectAllLabel={t("Select all on this page")} {...rowProps} />
            <TablePagination
              page={flatPage}
              pageSize={flatPageSize}
              total={flat.total}
              onPageChange={(nextPage) => dispatch({ type: "pagination/pageChanged", page: nextPage })}
              onPageSizeChange={(nextSize) => {
                dispatch({ type: "pagination/sizeChanged", size: nextSize });
              }}
              pageSizeOptions={[10, 20, 50, 100]}
            />
          </>
        )}
      </CardContent>
    </Card>
  );

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">{t("Findings")}</h1>
          <p className="text-muted-foreground text-sm">{t("Cross-task vulnerability overview")}</p>
        </div>
        <Tabs value={view} onValueChange={(v) => dispatch({ type: "viewChanged", view: v as FindingView })}>
          <TabsList>
            <TabsTrigger value="flat">{t("All findings")}</TabsTrigger>
            <TabsTrigger value="grouped">{t("By task")}</TabsTrigger>
            <TabsTrigger value="asset">{t("By asset")}</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>
      <div className="flex flex-1 flex-col gap-4 md:gap-6">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4 lg:grid-cols-8">
          {statCards.map((stat) => {
            const StatIcon = stat.icon;
            return (
              <Card key={stat.label} className="gap-1 py-4">
                <CardHeader className="px-4">
                  <CardDescription>{stat.label}</CardDescription>
                  <CardTitle className={cn("flex items-center gap-2 text-2xl tabular-nums", stat.tone)}>
                    <StatIcon className="size-5" aria-hidden="true" />
                    {stat.value}
                  </CardTitle>
                </CardHeader>
              </Card>
            );
          })}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <InputGroup className="w-full sm:w-72">
            <InputGroupInput
              type="search"
              value={search}
              onChange={(event) => dispatch({ type: "filterChanged", patch: { search: event.target.value } })}
              placeholder={t("Search findings")}
              aria-label={t("Search findings")}
            />
            <InputGroupAddon>
              <SearchIcon aria-hidden="true" />
            </InputGroupAddon>
          </InputGroup>

          <ToggleGroup
            type="single"
            value={severity}
            onValueChange={(value) =>
              value && dispatch({ type: "filterChanged", patch: { severity: value as "all" | Severity } })
            }
            variant="outline"
            size="sm"
            spacing={0}
          >
            {(
              [
                ["all", t("All")],
                ["critical", t("Critical")],
                ["high", t("High")],
                ["medium", t("Medium")],
                ["low", t("Low")],
              ] as const
            ).map(([val, label]) => (
              <ToggleGroupItem key={val} value={val} aria-label={t("Filter by {label} severity", { label })}>
                {label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <Select
            value={status}
            onValueChange={(v) => dispatch({ type: "filterChanged", patch: { status: v as "all" | FindingStatus } })}
          >
            <SelectTrigger size="sm" className="w-32">
              <SelectValue placeholder={t("Status")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All statuses")}</SelectItem>
              {FINDING_STATUSES.map((st) => (
                <SelectItem key={st} value={st}>
                  {statusMeta("finding", st).label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Select
            value={reportState}
            onValueChange={(v) =>
              dispatch({ type: "filterChanged", patch: { reportState: v as "all" | FindingReportState } })
            }
          >
            <SelectTrigger size="sm" className="w-40">
              <SelectValue placeholder={t("Report stage")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All report stages")}</SelectItem>
              {FINDING_REPORT_STATES.map((st) => (
                <SelectItem key={st} value={st}>
                  {REPORT_STATE_META[st].label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Select
            value={verificationStatus}
            onValueChange={(v) =>
              dispatch({ type: "filterChanged", patch: { verificationStatus: v as "all" | VerificationStatus } })
            }
          >
            <SelectTrigger size="sm" className="w-48">
              <SelectValue placeholder={t("Verification")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All verification states")}</SelectItem>
              {VERIFICATION_STATUSES.map((st) => (
                <SelectItem key={st} value={st}>
                  {VERIFICATION_META[st].label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Select value={vulnclass} onValueChange={(v) => dispatch({ type: "filterChanged", patch: { vulnclass: v } })}>
            <SelectTrigger size="sm" className="w-40">
              <SelectValue placeholder={t("Vulnerability type")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All types")}</SelectItem>
              {stats.vulnclasses.map((vc) => (
                <SelectItem key={vc} value={vc}>
                  {vc}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Select value={task} onValueChange={(v) => dispatch({ type: "filterChanged", patch: { task: v } })}>
            <SelectTrigger size="sm" className="w-48">
              <SelectValue placeholder={t("Task")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All tasks")}</SelectItem>
              <SelectItem value={UNASSIGNED_TASK}>{t("Unlinked / task deleted")}</SelectItem>
              {(stats.tasks ?? []).map((option) => {
                const id = String(option.id);
                const label = option.name || option.description || t("Task #{id} (deleted)", { id });
                return (
                  <SelectItem key={id} value={id}>
                    <span className="flex w-full items-center gap-2">
                      <span className="max-w-[14rem] truncate" title={label}>
                        {label}
                      </span>
                      <span className="inline-flex items-center gap-1 text-muted-foreground tabular-nums">
                        <BugIcon className="size-3.5" aria-hidden="true" />
                        {option.count}
                      </span>
                    </span>
                  </SelectItem>
                );
              })}
            </SelectContent>
          </Select>

          <Select
            value={sort}
            onValueChange={(v) => dispatch({ type: "filterChanged", patch: { sort: v as "severity" | "time" } })}
          >
            <SelectTrigger size="sm" className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="severity">{t("By severity")}</SelectItem>
              <SelectItem value="time">{t("By time")}</SelectItem>
            </SelectContent>
          </Select>

          <div className="ml-auto flex items-center gap-3">
            {selectedIds.size > 0 && (
              <div className="flex items-center gap-2">
                <span className="text-xs text-muted-foreground tabular-nums">
                  {t("{count} selected", { count: selectedIds.size })}
                </span>
                <Select
                  value=""
                  onValueChange={(v) => {
                    if (v) void applyBulkStatus(v as FindingStatus);
                  }}
                  disabled={bulkBusy}
                >
                  <SelectTrigger size="sm" className="w-40" aria-label={t("Set the status of the selected findings")}>
                    <SelectValue placeholder={bulkBusy ? t("Updating…") : t("Set status")} />
                  </SelectTrigger>
                  <SelectContent>
                    {FINDING_STATUSES.map((st) => (
                      <SelectItem key={st} value={st}>
                        {statusMeta("finding", st).label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select
                  value=""
                  onValueChange={(v) => {
                    if (v) void applyBulkReportState(v as FindingReportState);
                  }}
                  disabled={bulkBusy}
                >
                  <SelectTrigger
                    size="sm"
                    className="w-44"
                    aria-label={t("Set the report stage of the selected findings")}
                  >
                    <SelectValue placeholder={bulkBusy ? t("Updating…") : t("Set report stage")} />
                  </SelectTrigger>
                  <SelectContent>
                    {FINDING_REPORT_STATES.map((st) => (
                      <SelectItem key={st} value={st}>
                        {REPORT_STATE_META[st].label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            )}
            <Button size="sm" variant="outline" onClick={openExport}>
              <DownloadIcon /> {t("Export")}
            </Button>
          </div>
        </div>

        {view === "flat" && flatListCard}

        {view === "asset" && (
          <div className="grid min-h-0 items-start gap-4 lg:grid-cols-[20rem_minmax(0,1fr)] xl:grid-cols-[24rem_minmax(0,1fr)]">
            <Card className="gap-0 py-3 lg:sticky lg:top-4">
              <CardContent className="flex flex-col px-3">
                {assetTree.loading && !assetTree.loaded ? (
                  <div className="flex min-h-36 items-center justify-center">
                    <Spinner />
                  </div>
                ) : (
                  <AssetTree
                    nodes={assetTree.nodes}
                    selected={assetScope}
                    onSelect={setAssetScope}
                    loading={assetTree.loading}
                    truncated={assetTree.truncated}
                    droppedKinds={assetTree.droppedKinds}
                    findingTotal={assetTree.findingTotal}
                    onRefresh={() => void loadAssetTree()}
                  />
                )}
              </CardContent>
            </Card>
            <div className="flex min-w-0 flex-col gap-2">
              <div className="flex min-w-0 flex-wrap items-center gap-1 text-sm text-muted-foreground">
                <button
                  type="button"
                  className={cn("hover:text-foreground", assetScope === null && "font-medium text-foreground")}
                  onClick={() => setAssetScope(null)}
                >
                  {t("All assets")}
                </button>
                {assetPath.map((node) => (
                  <React.Fragment key={node.key}>
                    <ChevronRightIcon className="size-3.5 shrink-0" aria-hidden="true" />
                    <button
                      type="button"
                      className={cn(
                        "max-w-[16rem] truncate hover:text-foreground",
                        node.key === assetScope && "font-medium text-foreground",
                      )}
                      title={node.label}
                      onClick={() => setAssetScope(node.key)}
                    >
                      {node.display}
                    </button>
                  </React.Fragment>
                ))}
                <span className="ml-auto shrink-0 text-xs tabular-nums">
                  {t("{count} in total", { count: flat.total })}
                </span>
              </div>
              {flatListCard}
            </div>
          </div>
        )}

        {view === "grouped" && (
          <div className="flex flex-col gap-3">
            {groups.map((group) => {
              const key = findingGroupKey(group);
              const groupOpen = expandedGroups.has(key);
              const state = groupFindings[key] ?? {
                items: [],
                total: group.count,
                page: 1,
                pageSize: 10,
                loaded: false,
                loading: false,
              };
              return (
                <Card key={key} className="gap-0 py-0">
                  <CardHeader className="px-4 py-3">
                    <div className="flex min-w-0 flex-wrap items-center gap-3">
                      <button
                        type="button"
                        className="flex min-w-0 flex-1 items-center gap-3 text-left"
                        aria-expanded={groupOpen}
                        onClick={() => toggleGroup(key)}
                      >
                        <ChevronRightIcon
                          className={cn(
                            "size-4 shrink-0 text-muted-foreground transition-transform",
                            groupOpen && "rotate-90",
                          )}
                        />
                        <div className="flex min-w-0 flex-col gap-1">
                          <CardTitle className="truncate text-sm">
                            {group.task_id === null
                              ? t("Unlinked / task deleted")
                              : group.task_name
                                ? t("{name} (task #{id})", { name: group.task_name, id: group.task_id })
                                : t("Task #{id}", { id: group.task_id })}
                          </CardTitle>
                          <CardDescription className="truncate" title={group.task_description}>
                            {group.task_description || t("Source task unavailable")}
                          </CardDescription>
                        </div>
                      </button>
                      <div className="flex flex-wrap items-center gap-2">
                        {group.task_status && <StatusBadge domain="task" value={group.task_status} dot />}
                        {SEVERITIES.map((level) => {
                          const count = group[level];
                          if (count === 0) return null;
                          return (
                            <span key={level} className="inline-flex items-center gap-1">
                              <StatusBadge domain="severity" value={level} dot />
                              <span className="text-xs tabular-nums text-muted-foreground">{count}</span>
                            </span>
                          );
                        })}
                        <span className="text-xs tabular-nums text-muted-foreground">
                          {formatShortDateTime(group.last_found_at, { seconds: false })}
                        </span>
                        {group.task_id !== null && (
                          <Button size="icon-sm" variant="ghost" asChild>
                            <Link
                              href={`/function/tasks/detail?id=${group.task_id}`}
                              aria-label={t("View task #{id}", { id: group.task_id })}
                            >
                              <ArrowUpRightIcon />
                            </Link>
                          </Button>
                        )}
                      </div>
                    </div>
                  </CardHeader>
                  {groupOpen && (
                    <CardContent className="px-0">
                      {state.loading && !state.loaded ? (
                        <div className="flex min-h-36 items-center justify-center">
                          <Spinner />
                        </div>
                      ) : (
                        <>
                          <FindingsTable
                            items={state.items}
                            selectAllLabel={t("Select all on this page in this group")}
                            {...rowProps}
                          />
                          <TablePagination
                            page={state.page}
                            pageSize={state.pageSize}
                            total={state.total}
                            onPageChange={(nextPage) => void loadGroup(key, nextPage, state.pageSize)}
                            onPageSizeChange={(nextSize) => void loadGroup(key, 1, nextSize)}
                          />
                        </>
                      )}
                    </CardContent>
                  )}
                </Card>
              );
            })}
            {groups.length === 0 && (
              <Card>
                <CardContent className="py-12 text-center text-sm text-muted-foreground">
                  {t("No matching findings.")}
                </CardContent>
              </Card>
            )}
            <TablePagination
              page={page}
              pageSize={pageSize}
              total={groupTotal}
              onPageChange={(nextPage) =>
                dispatch({ type: "groupPagination", action: { type: "pagination/pageChanged", page: nextPage } })
              }
              onPageSizeChange={(nextPageSize) => {
                dispatch({ type: "groupPagination", action: { type: "pagination/sizeChanged", size: nextPageSize } });
              }}
              pageSizeOptions={[5, 10, 20]}
            />
          </div>
        )}
      </div>

      {retestFinding?.finding_id ? (
        <FindingRetestDialog
          key={retestFinding.finding_id}
          findingId={retestFinding.finding_id}
          findingName={retestFinding.name || retestFinding.vulnclass || retestFinding.summary}
          onStarted={(retest) => {
            const findingId = retestFinding.finding_id;
            if (!findingId || retest.conversation_id == null || !["pending", "running"].includes(retest.status)) return;
            retestRefreshVersion.current++;
            const active: ActiveFindingRetest = {
              id: retest.id,
              finding_id: findingId,
              conversation_id: retest.conversation_id,
              status: retest.status === "pending" ? "pending" : "running",
            };
            dispatch({ type: "retestStarted", retest: active });
          }}
          onClose={() => setRetestFinding(null)}
        />
      ) : null}

      <Dialog
        open={deepenFinding !== null}
        onOpenChange={(open) => {
          if (open || deepening) return;
          dispatch({ type: "deepenClosed" });
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("Deepen exploitation")}</DialogTitle>
            <DialogDescription className="break-words">
              {t(
                "A priority-10 worker intent will be created in the original task #{id} to run secondary verification based on this vulnerability:",
                { id: deepenFinding?.task_id ?? "" },
              )}{" "}
              {deepenFinding?.name || deepenFinding?.vulnclass || deepenFinding?.summary}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="finding-deepen-description">{t("Exploitation description")}</FieldLabel>
              <Textarea
                id="finding-deepen-description"
                value={deepen.description}
                onChange={(event) => dispatch({ type: "deepenDescriptionChanged", description: event.target.value })}
                maxLength={4000}
                placeholder={t(
                  "Describe the exploitation path to verify, boundary conditions, targets, or expected evidence",
                )}
                disabled={deepening}
              />
              <FieldDescription className="flex justify-between gap-3">
                <span>{t("The new intent inherits this vulnerability's asset anchors.")}</span>
                <span className="shrink-0 tabular-nums">{deepen.description.length} / 4000</span>
              </FieldDescription>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                dispatch({ type: "deepenClosed" });
              }}
              disabled={deepening}
            >
              {t("Cancel")}
            </Button>
            <Button onClick={submitDeepen} disabled={deepening || !deepen.description.trim()}>
              {deepening && <Spinner data-icon="inline-start" />}
              {t("Create deepen intent")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={exportDialog.open}
        onOpenChange={(open) => dispatch({ type: "exportDialogChanged", patch: { open } })}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("Export findings")}</DialogTitle>
            <DialogDescription>
              {t("Choose the export scope and format; the browser downloads the file automatically once generated.")}
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-5 py-1">
            <div className="flex flex-col gap-2">
              <span className="text-xs text-muted-foreground">{t("Export scope")}</span>
              <RadioGroup
                value={exportDialog.scope}
                onValueChange={(v) => dispatch({ type: "exportDialogChanged", patch: { scope: v as ExportScope } })}
              >
                <label htmlFor="export-scope-filtered" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-scope-filtered" value="filtered" />{" "}
                  {t("Export the current filter results ({count} items)", { count: filteredTotal })}
                </label>
                <label htmlFor="export-scope-all" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-scope-all" value="all" /> {t("Export all")}
                </label>
                <label
                  htmlFor="export-scope-selected"
                  className={cn("flex items-center gap-2 text-sm", selectedIds.size === 0 && "text-muted-foreground")}
                >
                  <RadioGroupItem id="export-scope-selected" value="selected" disabled={selectedIds.size === 0} />
                  {t("Export the {count} selected items", { count: selectedIds.size })}
                </label>
              </RadioGroup>
            </div>

            <div className="flex flex-col gap-2">
              <span className="text-xs text-muted-foreground">{t("Export format")}</span>
              <RadioGroup
                value={exportDialog.format}
                onValueChange={(v) => dispatch({ type: "exportDialogChanged", patch: { format: v as ExportFormat } })}
              >
                <label htmlFor="export-format-md-single" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-md-single" value="md-single" />{" "}
                  {t("Markdown summary report (a single .md file)")}
                </label>
                <label htmlFor="export-format-md-zip" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-md-zip" value="md-zip" />{" "}
                  {t("Markdown per finding (one .md per vulnerability, packaged as .zip)")}
                </label>
                <label htmlFor="export-format-csv" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-csv" value="csv" /> {t("CSV table (.csv)")}
                </label>
                <label htmlFor="export-format-json" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-json" value="json" /> JSON (.json)
                </label>
              </RadioGroup>
            </div>
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => dispatch({ type: "exportDialogChanged", patch: { open: false } })}
              disabled={exporting}
            >
              {t("Cancel")}
            </Button>
            <Button
              onClick={doExport}
              disabled={exporting || (exportDialog.scope === "selected" && selectedIds.size === 0)}
            >
              <DownloadIcon /> {exporting ? t("Exporting…") : t("Export")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
