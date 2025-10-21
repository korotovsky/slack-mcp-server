// cmd/slack-mcp-server/health.ts
import express from "express";
import { buildTelemetry, emitTelemetry } from "../../fc_mesh";

const app = express();
const PORT = Number(process.env.HEALTH_PORT || 9615);

app.get("/health", (_req, res) => {
  const evt = buildTelemetry({
    app: "slack-mcp",
    signal: "health",
    ok: true,
    result_code: 200,
    route: "GET /health",
    module: "fc-mesh",
    mandate: "Proof & Trust",
    metrics_json: { build: "fc-mesh-001" }
  });
  emitTelemetry(evt);
  res.status(200).send("ok");
});

app.listen(PORT, () => {
  const evt = buildTelemetry({
    app: "slack-mcp",
    signal: "health_server_started",
    ok: true,
    result_code: 200,
    route: `listen:${PORT}`,
    module: "fc-mesh"
  });
  emitTelemetry(evt);
});
