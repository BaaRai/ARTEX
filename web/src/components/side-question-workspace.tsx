"use client";

import { type ReactNode, useEffect, useRef, useState } from "react";

import { ArrowUpIcon, MessageCircleQuestionIcon, SquareIcon, Trash2Icon, XIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Markdown } from "@/components/markdown";
import { Alert, AlertDescription } from "@/components/ui/alert";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Drawer, DrawerContent, DrawerDescription, DrawerHeader, DrawerTitle } from "@/components/ui/drawer";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from "@/components/ui/input-group";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { Skeleton } from "@/components/ui/skeleton";
import { useIsMobile } from "@/hooks/use-mobile";
import type { SideQuestions } from "@/hooks/use-side-questions";
import { cn } from "@/lib/utils";

type ComposerLayout = "inline" | "stacked";

const preparationLabels = {
  preparing: "Preparing context…",
  summarizing_history: "Summarizing earlier side Q&A…",
  compressing_snapshot: "Compressing the side-question context copy…",
  retrying: "Model context exceeded, shrinking and retrying…",
  answering: "Answering…",
};

export function SideQuestionButton({ side }: { side: SideQuestions }) {
  const t = useTranslations("sideQuestionWorkspace");
  if (!side.enabled) return null;
  return (
    <Button variant="outline" size="sm" onClick={() => side.setOpen(true)} title={t("button.title")}>
      <MessageCircleQuestionIcon data-icon="inline-start" />
      {t("button.label")}
    </Button>
  );
}

function SidePanel({
  side,
  label,
  composerLayout,
}: {
  side: SideQuestions;
  label: string;
  composerLayout: ComposerLayout;
}) {
  const t = useTranslations("sideQuestionWorkspace");
  const inlineComposer = composerLayout === "inline";
  const [confirm, setConfirm] = useState(false);
  const viewport = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const tail = side.items.at(-1);
  // biome-ignore lint/correctness/useExhaustiveDependencies: New cumulative text scrolls only readers who remain at the bottom.
  useEffect(() => {
    if (pinned.current && viewport.current) viewport.current.scrollTop = viewport.current.scrollHeight;
  }, [tail?.answer, tail?.id]);
  const status = {
    running: t("status.running"),
    completed: t("status.completed"),
    failed: t("status.failed"),
    cancelled: t("status.cancelled"),
    interrupted: t("status.interrupted"),
  };
  return (
    <section className="flex h-full min-h-0 flex-col bg-background" aria-label={t("panel.ariaLabel")}>
      <div className="flex items-center gap-2 border-b p-3">
        <div className="min-w-0 flex-1">
          <p className="font-medium">
            {t.rich("panel.heading", { btw: (chunks) => <span className="text-muted-foreground">{chunks}</span> })}
          </p>
          <p className="truncate text-muted-foreground text-xs">{label}</p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => setConfirm(true)}
          disabled={!side.items.length || side.busy}
          aria-label={t("panel.clearHistoryAriaLabel")}
        >
          <Trash2Icon />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => side.setOpen(false)}
          aria-label={t("panel.closeAriaLabel")}
        >
          <XIcon />
        </Button>
      </div>
      <div className="border-b px-3 py-2 text-muted-foreground text-xs">
        {side.snapshot ? (
          <>
            <p>{side.snapshot.model.model}</p>
            <p>{t("snapshot.contextUpdated", { time: new Date(side.snapshot.captured_at).toLocaleString() })}</p>
          </>
        ) : (
          t("snapshot.waiting")
        )}
      </div>
      <div
        ref={viewport}
        onScroll={(event) => {
          const el = event.currentTarget;
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
        }}
        className="min-h-0 flex-1 overflow-y-auto p-3"
      >
        {side.nextCursor > 0 && (
          <Button variant="ghost" size="sm" onClick={() => void side.load(side.nextCursor)}>
            {t("loadEarlier")}
          </Button>
        )}
        {side.loading && <Skeleton className="h-16 w-full" />}
        {!side.loading && side.items.length === 0 && (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{t("empty.title")}</EmptyTitle>
              <EmptyDescription>{t("empty.description")}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        )}
        <div className="flex flex-col gap-5">
          {side.items.map((item) => (
            <article key={item.id} className="flex min-w-0 flex-col gap-2">
              <div className="whitespace-pre-wrap break-words rounded-lg bg-muted p-3 text-sm">{item.question}</div>
              <div className="flex flex-wrap items-center gap-2 text-muted-foreground text-xs">
                <Badge variant="secondary">{status[item.status]}</Badge>
                <span className="truncate">{item.model.model}</span>
                <time dateTime={item.snapshot_at} title={new Date(item.snapshot_at).toLocaleString()}>
                  {t("item.contextTime", { time: new Date(item.snapshot_at).toLocaleTimeString() })}
                </time>
              </div>
              {item.context?.estimated_input_tokens != null && (
                <p className="text-muted-foreground text-xs">
                  {t("item.recentExchanges", { count: item.context.recent_exchanges })}
                  {item.context.history_summarized && t("item.historySummarized")}
                  {item.context.snapshot_summarized && t("item.snapshotSummarized")}
                </p>
              )}
              {item.answer && <Markdown text={item.answer} />}
              {!item.answer && item.status === "running" && (
                <p role="status" className="text-muted-foreground text-sm">
                  {t.has(`preparationLabels.${item.context?.phase ?? "answering"}`)
                    ? t(`preparationLabels.${item.context?.phase ?? "answering"}`)
                    : preparationLabels[item.context?.phase ?? "answering"]}
                </p>
              )}
              {item.error && (
                <Alert variant="destructive">
                  <AlertDescription>{item.error}</AlertDescription>
                </Alert>
              )}
            </article>
          ))}
        </div>
      </div>
      <div className="shrink-0 border-t p-3">
        {(side.error || side.snapshot?.reason) && (
          <Alert variant="destructive" className="mb-2">
            <AlertDescription>{side.error || side.snapshot?.reason}</AlertDescription>
          </Alert>
        )}
        <InputGroup className={inlineComposer ? "min-h-10" : "min-h-9"}>
          <InputGroupTextarea
            rows={1}
            className={cn("overflow-y-auto", inlineComposer ? "max-h-40 min-h-0" : "max-h-36 min-h-9")}
            aria-label={t("composer.ariaLabel")}
            placeholder={t("composer.placeholder")}
            value={side.draft}
            maxLength={4000}
            disabled={side.busy}
            onChange={(event) => side.setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault();
                void side.ask(side.draft);
              }
            }}
          />
          <InputGroupAddon align={inlineComposer ? "inline-end" : "block-end"}>
            {!inlineComposer && <span className="text-muted-foreground text-xs">{t("composer.note")}</span>}
            {side.running ? (
              <InputGroupButton
                className="ml-auto"
                variant="destructive"
                size="icon-xs"
                onClick={() => void side.stop()}
                aria-label={t("composer.stopAriaLabel")}
              >
                <SquareIcon />
              </InputGroupButton>
            ) : (
              <InputGroupButton
                className="ml-auto"
                variant="default"
                size="icon-xs"
                onClick={() => void side.ask(side.draft)}
                disabled={side.busy || !side.draft.trim() || !side.snapshot?.available}
                aria-label={t("composer.sendAriaLabel")}
              >
                <ArrowUpIcon />
              </InputGroupButton>
            )}
          </InputGroupAddon>
        </InputGroup>
      </div>
      {inlineComposer && (
        <div className="shrink-0 truncate px-3 pt-0.5 pb-1 text-muted-foreground text-xs">{t("composer.note")}</div>
      )}
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("clearDialog.title")}</AlertDialogTitle>
            <AlertDialogDescription>{t("clearDialog.description")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("clearDialog.cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void side.clear()}>{t("clearDialog.confirm")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

export function SideQuestionWorkspace({
  side,
  label,
  children,
  composerLayout = "stacked",
}: {
  side: SideQuestions;
  label: string;
  children: ReactNode;
  composerLayout?: ComposerLayout;
}) {
  const t = useTranslations("sideQuestionWorkspace");
  const mobile = useIsMobile();
  return (
    <>
      <ResizablePanelGroup orientation="horizontal" className="min-h-0 min-w-0 flex-1">
        <ResizablePanel id="main-conversation" minSize="35%" className="flex min-h-0 min-w-0 flex-col">
          {children}
        </ResizablePanel>
        {side.open && side.enabled && !mobile && (
          <>
            <ResizableHandle withHandle />
            <ResizablePanel id="side-question" defaultSize="38%" minSize="280px" maxSize="65%">
              <SidePanel side={side} label={label} composerLayout={composerLayout} />
            </ResizablePanel>
          </>
        )}
      </ResizablePanelGroup>
      <Drawer open={mobile && side.open && side.enabled} onOpenChange={side.setOpen}>
        <DrawerContent className="h-[85svh]">
          <DrawerHeader className="sr-only">
            <DrawerTitle>{t("workspace.drawerTitle")}</DrawerTitle>
            <DrawerDescription>{t("workspace.drawerDescription", { label })}</DrawerDescription>
          </DrawerHeader>
          <SidePanel side={side} label={label} composerLayout={composerLayout} />
        </DrawerContent>
      </Drawer>
    </>
  );
}
