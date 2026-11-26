"use client";

import { AttackPathView } from "@/components/attack-path-view";

export function AttackPathsTab({ taskId }: { taskId: string }) {
  return <AttackPathView taskId={taskId} className="h-[76vh] min-h-[480px]" />;
}
