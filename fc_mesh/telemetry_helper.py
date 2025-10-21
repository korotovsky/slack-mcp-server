# fc_mesh/telemetry_helper.py
from __future__ import annotations
import json, uuid
from datetime import datetime
try:
    from zoneinfo import ZoneInfo  # py3.9+
except ImportError:
    ZoneInfo = None

TZ = ZoneInfo("Europe/Amsterdam") if ZoneInfo else None

def _now_iso() -> str:
    dt = datetime.now(TZ) if TZ else datetime.utcnow()
    return dt.isoformat() if TZ else dt.isoformat() + "Z"

def build_telemetry(
    *,
    app: str,
    signal: str,
    ok: bool,
    result_code: int,
    route: str | None = None,
    latency_ms: float | None = None,
    capsule: str | None = None,
    module: str | None = None,
    mandate: str | None = None,
    metrics_json: dict | None = None,
    trace_id: str | None = None
) -> dict:
    """Build a FortuneCat-standard telemetry record (Snow rules: trace_id + ts, 200/422)."""
    if result_code not in (200, 422):
        raise ValueError("result_code must be 200 or 422")

    event = {
        "ts": _now_iso(),
        "trace_id": trace_id or str(uuid.uuid4()),
        "app": app,
        "signal": signal,
        "ok": bool(ok),
        "result_code": result_code
    }
    if latency_ms is not None: event["latency_ms"] = float(latency_ms)
    if route: event["route"] = route
    if capsule: event["capsule"] = capsule
    if module: event["module"] = module
    if mandate: event["mandate"] = mandate
    if metrics_json: event["metrics_json"] = metrics_json
    return event

def emit_telemetry(event: dict) -> None:
    """Print JSON line (stdout).  Future hooks: Notion / Relevance AI ingestion."""
    print(json.dumps(event, ensure_ascii=False, separators=(",", ":")))

# Smoke test
if __name__ == "__main__":
    e = build_telemetry(
        app="slack-mcp",
        signal="telemetry_init",
        ok=True,
        result_code=200,
        route="health",
        module="fc-mesh",
        mandate="Proof & Trust",
        metrics_json={"build": "fc-mesh-001"}
    )
    emit_telemetry(e)
