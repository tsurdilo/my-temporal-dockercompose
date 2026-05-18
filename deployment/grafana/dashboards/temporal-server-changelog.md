# Changelog — Temporal Server Dashboard

## v2.2.0 — 2026-05-15

### Fixed
- Excluded `_unknown_` namespace from all panels that group or filter by namespace. The `_unknown_` value is emitted by Temporal for internal/system-level requests that have no namespace context and should not appear as a selectable namespace or as a series in namespace-breakdown panels.
  - Namespace template variable query updated: `label_values(service_requests{namespace!="_unknown_"}, namespace)` — `_unknown_` no longer appears in the namespace dropdown
  - Panels patched: **Actions per Namespace** (18), **RPS per Namespace** (20), **Service Requests by Namespace and Operation** (93), **Service Errors by Namespace and Operation** (100), **Actual RPS vs Namespace Host RPS Limit** (123), **Outlier Namespaces** (2004)

---

## v2.1.0 — 2026-05-13

### Added
- New panel group **Worker Registry (In-memory)** (group 16, inserted between Visibility and Cluster Replication) with 5 panels:
  - **Workers Added** — rate of new worker registrations
  - **Workers Removed** — rate of removals across all causes (shutdown, TTL eviction, capacity eviction)
  - **Percentile of Num of Cached Entries** — estimated entry count derived from `capacity_utilization × 1e6` at the selected `$p` percentile across matching instances
  - **Percentile of Cache Utilization** — utilization as a percentage at the selected `$p` percentile, with threshold lines at 80% (orange) and 100% (red)
  - **Workers - Number of Activity Slots Used** — `histogram_quantile` of `worker_registry_activity_slots_used` at the selected `$p` percentile

### Changed
- Cluster Replication renumbered from group 16 → 17
- Authorization renumbered from group 17 → 18

---

## v2.0.0 — 2026-05-12

First versioned release. Prior changes were unversioned.

### Fixed
- Corrected metric name in Shard Movement > Shards Closed panel: `sharditem_closed_count` → `shard_closed_count`
