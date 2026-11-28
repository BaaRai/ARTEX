export interface AccessAuditEvent {
  id: number;
  observed_at: string;
  source: string;
  outcome: string;
  url: string;
  host: string;
  method?: string;
  status?: number;
  task_id?: string;
  conversation_id?: number;
  agent?: string;
  run_id?: string;
  tool_use_id?: string;
  tool?: string;
  request_id?: string;
}
export interface AccessAuditPage {
  events: AccessAuditEvent[];
  next: number;
  from: string;
  to: string;
  coverage: "partial";
  retention_days: number;
  max_events: number;
  write_failures_since_start: number;
  process_started_at: string;
}
