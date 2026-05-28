#!/usr/bin/env bash
# deploy-temporal-metrics.sh
#
# Copies the temporal-metrics dashboard and alert files into Grafana provisioning,
# substitutes the local Grafana datasource UID, and reloads Grafana.
#
# Usage:
#   ./deploy-temporal-metrics.sh
#
# Run this any time you pull updates from ~/devel/temporal-metrics.

set -euo pipefail

METRICS_REPO=~/devel/temporal-metrics
GRAFANA_DASHBOARDS=~/devel/my-temporal-dockercompose/deployment/grafana/dashboards
GRAFANA_ALERTING=~/devel/my-temporal-dockercompose/deployment/grafana/provisioning/alerting
GRAFANA_URL=http://admin:admin@localhost:8085

# ---------------------------------------------------------------------------
# 1. Look up the Prometheus datasource UID from the running Grafana instance
# ---------------------------------------------------------------------------
echo "→ Looking up Prometheus datasource UID..."
GRAFANA_RESPONSE=$(curl -s --max-time 5 "${GRAFANA_URL}/api/datasources")
if [ -z "$GRAFANA_RESPONSE" ]; then
  echo "ERROR: No response from Grafana at ${GRAFANA_URL}. Is the stack running?"
  exit 1
fi
DS_UID=$(echo "$GRAFANA_RESPONSE" | python3 -c "
import sys, json
try:
    ds = json.load(sys.stdin)
except json.JSONDecodeError as e:
    print(f'ERROR: Grafana returned non-JSON: {e}', file=sys.stderr)
    sys.exit(1)
for d in ds:
    if d.get('type') == 'prometheus':
        print(d['uid'])
        break
")

if [ -z "$DS_UID" ]; then
  echo "ERROR: Could not find a Prometheus datasource in Grafana. Is Grafana running?"
  exit 1
fi
echo "   UID: $DS_UID"

# ---------------------------------------------------------------------------
# 2. Copy dashboard JSON
# ---------------------------------------------------------------------------
echo "→ Deploying dashboard..."
cp "${METRICS_REPO}/metrics/dashboards/server/temporal-server.json" \
   "${GRAFANA_DASHBOARDS}/temporal-server.json"

# ---------------------------------------------------------------------------
# 3. Copy alerts YAML with datasource UID substituted
# ---------------------------------------------------------------------------
echo "→ Deploying alert rules (datasourceUid: ${DS_UID})..."
sed "s/datasourceUid: Prometheus/datasourceUid: ${DS_UID}/g" \
  "${METRICS_REPO}/metrics/alerts/server/temporal-server-alerts.yaml" \
  > "${GRAFANA_ALERTING}/temporal-server-essential.yaml"

# ---------------------------------------------------------------------------
# 4. Reload Grafana provisioning (no restart needed)
# ---------------------------------------------------------------------------
echo "→ Reloading Grafana provisioning..."
curl -s -X POST "${GRAFANA_URL}/api/admin/provisioning/alerting/reload" > /dev/null
curl -s -X POST "${GRAFANA_URL}/api/admin/provisioning/dashboards/reload" > /dev/null

echo "✓ Done. Dashboard and alerts deployed."
echo "  Alert rules: ${GRAFANA_URL}/alerting/list"
