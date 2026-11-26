"use client";

import { BrainView } from "@/components/brain-view";

export function BrainTab({ taskId }: { taskId: string }) {
  return <BrainView taskId={taskId} className="h-[76vh] min-h-[480px]" />;
}
