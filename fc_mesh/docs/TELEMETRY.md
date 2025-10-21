# FortuneCat Telemetry (fc-mesh)

**Purpose:** Standard JSON event for ObservationOS with Snow Rules (trace_id + ts) and deterministic result codes (200/422).

## Contract (JSON Schema)
See: `fc_mesh/telemetry.schema.json`

**Required:** `ts, trace_id, app, signal, ok, result_code`  
**Optional:** `latency_ms, route, capsule, module, mandate, metrics_json`

## Example
```json
{
  "ts": "2025-10-21T16:10:00+02:00",
  "trace_id": "00000000-0000-4000-8000-000000000001",
  "app": "slack-mcp",
  "signal": "telemetry_init",
  "ok": true,
  "result_code": 200,
  "route": "health",
  "module": "fc-mesh",
  "mandate": "Proof & Trust",
  "metrics_json": { "build": "fc-mesh-001" }
}
