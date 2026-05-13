# Changelog — Temporal Server Dashboard

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
