"use client";

import { create } from "zustand";
import { persist } from "zustand/middleware";

// The sidebar's company switcher. One company narrows every page that reads
// assets (findings, dashboard, traffic, tasks); null means "all companies".
// The asset graph already carries company_id, so the narrowing rides on the
// existing asset_scope filter (node key c:<id>) rather than a new parameter.
type CompanyState = {
  companyId: number | null;
  companyName: string;
  setCompany: (id: number | null, name?: string) => void;
};

export const useCompanyStore = create<CompanyState>()(
  persist(
    (set) => ({
      companyId: null,
      companyName: "",
      setCompany: (id, name = "") => set({ companyId: id, companyName: id === null ? "" : name }),
    }),
    { name: "artex-active-company" },
  ),
);

// useActiveCompanyScope returns the asset_scope node key for the active company,
// or undefined when every company is in view.
export function useActiveCompanyScope(): string | undefined {
  const companyId = useCompanyStore((s) => s.companyId);
  return companyId === null ? undefined : `c:${companyId}`;
}

// useActiveCompanyID is the numeric id, for filters that take an id.
export function useActiveCompanyID(): number | null {
  return useCompanyStore((s) => s.companyId);
}
