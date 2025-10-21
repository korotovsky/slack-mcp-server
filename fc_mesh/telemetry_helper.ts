export type Telemetry = {
  ts: string; trace_id: string; app: string; signal: string; ok: boolean;
  result_code: 200 | 422; latency_ms?: number; route?: string;
  capsule?: string; module?: string; mandate?: string; metrics_json?: Record<string, unknown>;
};

const TZ = "Europe/Amsterdam";
const isoNow = () => {
  try { return new Date(new Date().toLocaleString("en-CA", { timeZone: TZ })).toISOString(); }
  catch { return new Date().toISOString(); }
};

export function buildTelemetry(
  p: Omit<Telemetry, "ts" | "trace_id"> & { trace_id?: string }
): Telemetry {
  if (![200, 422].includes(p.result_code)) throw new Error("result_code must be 200 or 422");
  const uuid = (globalThis.crypto as any)?.randomUUID?.() ??
    "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, c => {
      const r = (Math.random() * 16) | 0, v = c === "x" ? r : (r & 0x3) | 0x8; return v.toString(16);
    });
  return { ts: isoNow(), trace_id: p.trace_id ?? uuid, ...p };
}

export function emitTelemetry(e: Telemetry) {
  process.stdout.write(JSON.stringify(e) + "\n");
  // future: notionIngest(e); relevanceIngest(e);
}
