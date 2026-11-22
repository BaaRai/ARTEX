"use client";

import * as React from "react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

export interface ConfirmOptions {
  /** Name the object: `Delete rule "dd guard"?` rather than "Are you sure?". */
  title: React.ReactNode;
  /** Say what is lost and whether it can be undone. */
  description?: React.ReactNode;
  /** Verb for the action button, e.g. "Delete". Defaults to "Delete" when destructive, else "Confirm". */
  confirmLabel?: string;
  cancelLabel?: string;
  /** Red action button. Defaults to true: this dialog exists mainly to guard destructive actions. */
  destructive?: boolean;
}

type ConfirmFn = (options: ConfirmOptions) => Promise<boolean>;

const ConfirmContext = React.createContext<ConfirmFn | null>(null);

interface PendingConfirm extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

/**
 * One confirmation pattern for the whole app. Radix focuses Cancel when the dialog
 * opens, so pressing Enter by reflex never destroys anything.
 */
export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const [pending, setPending] = React.useState<PendingConfirm | null>(null);

  const confirm = React.useCallback<ConfirmFn>(
    (options) =>
      new Promise<boolean>((resolve) => {
        setPending({ ...options, resolve });
      }),
    [],
  );

  const settle = (ok: boolean) => {
    pending?.resolve(ok);
    setPending(null);
  };

  const destructive = pending?.destructive ?? true;

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <AlertDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) settle(false);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{pending?.title}</AlertDialogTitle>
            {pending?.description ? <AlertDialogDescription>{pending.description}</AlertDialogDescription> : null}
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{pending?.cancelLabel ?? "Cancel"}</AlertDialogCancel>
            <AlertDialogAction variant={destructive ? "destructive" : "default"} onClick={() => settle(true)}>
              {pending?.confirmLabel ?? (destructive ? "Delete" : "Confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ConfirmContext.Provider>
  );
}

/** `if (!(await confirm({ title: `Delete "${name}"?` }))) return;` */
export function useConfirm(): ConfirmFn {
  const ctx = React.useContext(ConfirmContext);
  if (!ctx) throw new Error("useConfirm must be used inside <ConfirmProvider>");
  return ctx;
}
