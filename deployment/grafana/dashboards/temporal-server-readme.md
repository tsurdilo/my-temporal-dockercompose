# Temporal Server Grafana Dashboard

A comprehensive Grafana dashboard for monitoring a self-hosted [Temporal](https://temporal.io) Server cluster using Prometheus metrics.

> **Compatibility:** Temporal Server v1.20+ · Grafana 9.0+ · Prometheus

---

## Table of Contents

- [Overview](#overview)
- [Setup](#setup)
- [Template Variables](#template-variables)
- [Threshold Reference Lines](#threshold-reference-lines)
- [Groups and Panels](#groups-and-panels)
  - [Cluster Throughput](#1-cluster-throughput)
  - [Shard and Workflow Lock Latencies](#2-shard-and-workflow-lock-latencies)
  - [Persistence Requests, Latencies and Errors](#3-persistence-requests-latencies-and-errors)
  - [Service Latencies](#4-service-latencies)
  - [Service Requests and Errors](#5-service-requests-and-errors)
  - [Throttling and Limits](#6-throttling-and-limits)
  - [Busy Workflow Throttling](#7-busy-workflow-throttling)
  - [Shard Movement](#8-shard-movement)
  - [History Timer Task Info](#9-history-timer-task-info)
  - [Workflow Stats](#10-workflow-stats)
  - [Workflow Execution History Info](#11-workflow-execution-history-info)
  - [SDK Workers Info](#12-sdk-workers-info)
  - [Pollers](#13-pollers)
  - [Visibility](#14-visibility)
  - [Cluster Replication](#15-cluster-replication)
  - [Authorization](#16-authorization)
- [Related Resources](#related-resources)

---

## Overview

This dashboard provides end-to-end observability for a self-hosted Temporal Server cluster. It is built entirely on Prometheus metrics emitted by Temporal Server and is designed to help operators:

- Monitor cluster health and throughput
- Diagnose performance bottlenecks
- Understand workload behavior across namespaces
- Alert on limit exceeded events, throttling, and errors

> **Multi-cluster deployments:** This dashboard focuses on the health of a single cluster. If you are running Temporal in a multi-cluster configuration with active replication, use this dashboard alongside the [Temporal Standby Cluster — Replication Health](#related-resources) dashboard, which provides dedicated standby-side and replication fidelity monitoring.

---

## Setup

1. Ensure your Temporal Server is configured to expose Prometheus metrics:

```yaml
metrics:
  prometheus:
    timerType: "histogram"
    listenAddress: "0.0.0.0:8000"
```

2. Optionally add static tags to all emitted metrics:

```yaml
metrics:
  tags:
    environment: production
    cluster: my-temporal-cluster
  prometheus:
    timerType: "histogram"
    listenAddress: "0.0.0.0:8000"
```

3. Import `temporal-server.json` into Grafana via **Dashboards → Import**.
4. Select your Prometheus datasource when prompted.

---

## Template Variables

| Variable | Description | Default |
|---|---|---|
| **Datasource** | Prometheus datasource to use | — |
| **Namespace** | Filters most panels to a specific Temporal namespace | — |
| **Percentile** | Histogram quantile for latency and size panels | `0.99` |

All panels that show rates use `$__rate_interval` for Prometheus rate calculations. All latency and size panels respect the selected percentile variable.

---

## Groups and Panels

---

### 1. Cluster Throughput

Tracks the overall throughput of the Temporal cluster in terms of workflow actions, frontend RPS, and state transitions. Use this group to get a high-level picture of how much work the cluster is doing.

| Panel | Description |
|---|---|
| **Actions per Namespace** | Rate of `action` metric broken down by namespace. An "action" represents a DB write resulting from operations such as starting a workflow, sending a signal, or processing an update. Useful as a proxy for write load per namespace. |
| **Total Actions** | Total rate of actions across all namespaces combined. Useful for a cluster-wide throughput view regardless of namespace filter. |
| **RPS per Namespace** | Rate of frontend service requests broken down by namespace. Useful for understanding which namespaces are driving the most API traffic. |
| **Total RPS** | Total rate of all requests to the frontend service across all namespaces. Useful as a top-level cluster load indicator. |
| **Server State Transitions** | Rate of state transitions per second for the selected namespace. A state transition occurs each time a workflow execution is created or updated. This is the most direct measure of cluster throughput in terms of execution progress. |

---

### 2. Shard and Workflow Lock Latencies

Tracks latency for acquiring history shards and workflow execution locks. These latencies are important operational signals that can indicate cluster sizing issues or database pressure.

| Panel | Description |
|---|---|
| **Shard Lock Latency** | Latency for acquiring the shard lock (`ShardInfo` semaphore) on the History service. High values often indicate the cluster is undersized relative to the workload — either too few history hosts or too few shards. This latency directly contributes to end-to-end API latency. |
| **Workflow Lock Latency** | Latency for the `HistoryCacheGetOrCreate` operation, representing the time to acquire the per-workflow execution lock. Updates to a single execution are serialized under this lock. High values can indicate a specific workflow type generating very high update rates (common in fan-out patterns) or elevated persistence latencies causing lock contention. |

---

### 3. Persistence Requests, Latencies and Errors

Tracks all interactions with the primary Temporal persistence database. Database performance has a direct and significant effect on overall cluster performance and end-to-end workflow latencies.

| Panel | Description |
|---|---|
| **Persistence Requests per Namespace and Operation** | Rate of persistence requests for the selected namespace broken down by operation (e.g. `GetWorkflowExecution`, `UpdateWorkflowExecution`, `AppendHistoryNodes`). Useful for understanding which database operations are most frequent for a specific namespace. |
| **Persistence Requests Total** | Total rate of persistence requests across the entire cluster regardless of namespace. Useful for a cluster-wide view of overall database load. |
| **Persistence Requests Total per Operation** | Total rate of persistence requests across the entire cluster broken down by operation. Useful for identifying which operation types are driving the most database load cluster-wide. |
| **Persistence Latencies** | Persistence latency broken down by operation at the selected percentile. High persistence latency is one of the most common root causes of elevated end-to-end Temporal API latencies. |
| **Persistence Errors by Namespace and Operation** | Rate of persistence errors for the selected namespace broken down by operation and error type. Any sustained error rate here is a serious signal — persistence errors can cause workflow task failures, retries, and increased cluster load. |
| **SQL DB Connection Pool** | Current state of the SQL database connection pool: configured maximum, open, idle, and in-use connections. A pool consistently at its maximum indicates DB saturation. **Only applicable for SQL backends (MySQL, PostgreSQL). Not emitted for Cassandra.** |
| **Persistence Errors Total by Operation** | Total rate of persistence errors across the entire cluster broken down by operation and error type. Useful for identifying cluster-wide error patterns regardless of namespace filter. |
| **Persistence Availability** | Percentage of persistence requests that succeeded (no errors), shown as a gauge. Thresholds: 99% green, 95% orange. Any value below 99% should be investigated. |

---

### 4. Service Latencies

Service Latencies target latencies for different operations, by service type. Use this group to identify slow operations across the Frontend, History, and Matching services at the selected percentile.

| Panel | Description |
|---|---|
| **Frontend Service Latency** | RPC service latency for the Frontend service broken down by operation at the selected percentile. Frontend latency is directly experienced by SDK clients and is a key SLO signal. High values are often correlated with persistence latency or throttling. |
| **History Service Latency** | RPC service latency for the History service broken down by operation at the selected percentile. History service handles all workflow execution state mutations. Elevated latency here typically points to persistence pressure or shard contention. |
| **Service Latency No-User Latency** | Frontend service latency excluding user processing time, broken down by operation at the selected percentile. Isolates server-side latency from SDK/user-side processing time. Useful for distinguishing cluster-side slowness from worker-side slowness. |
| **Service Latency User Latency** | Frontend service latency attributable to user processing time, broken down by operation at the selected percentile. High values relative to no-user latency indicate that SDK worker processing time is the dominant contributor to end-to-end latency. |
| **Matching Service Latency** | RPC service latency for the Matching service broken down by operation at the selected percentile. Matching service is responsible for dispatching tasks to SDK worker pollers. High latency here can directly increase schedule-to-start latencies for activities and workflow tasks. |

---

### 5. Service Requests and Errors

Tracks service request rates, error rates, and connection health for the Temporal Frontend service.

| Panel | Description |
|---|---|
| **Service Requests by Namespace** | Rate of frontend service requests for the selected namespace. |
| **Service Requests by Namespace and Operation** | Rate of frontend service requests broken down across all namespaces and operations. Useful for identifying which namespaces and API operations are responsible for the most traffic. |
| **Service Errors by Namespace** | Rate of service errors on the Frontend service for the selected namespace. These are not resource exhaustion or business logic errors — they typically indicate infrastructure issues. |
| **Service Errors by Namespace and Operation** | Rate of frontend service errors broken down across all namespaces and operations. Useful for pinpointing which specific namespace and operation combinations are generating errors. |
| **Active gRPC Connections** | Current number of active gRPC TCP connections on the Frontend service. An unexpected increase can indicate SDK clients not releasing connections properly. |
| **gRPC Connection Churn** | Rate of gRPC TCP connections being accepted and closed. High churn may indicate SDK misconfiguration or clients repeatedly reconnecting. Accepted and closed rates should track closely under normal conditions. |
| **Service Panics** | Rate of panics across all Temporal services. **Any value above zero is a critical signal** and should be investigated immediately — it indicates an unrecoverable error in a service goroutine. |
| **Server Errors by Type** | Rate of frontend service errors broken down by error type (e.g. `invalid_argument`, `not_found`, `resource_exhausted`). Useful for distinguishing client-side errors from server-side issues. |

---

### 6. Throttling and Limits

Tracks API throttling events and the current configured RPS limits. Use this group to understand whether the cluster is rejecting requests due to rate limits and how close actual traffic is to those limits.

| Panel | Description |
|---|---|
| **Resource Exhausted with Cause** | Rate of resource exhausted errors broken down by operation and cause. Key causes: `RpsLimit`, `QpsLimit`, `ConcurrentLimit` (too many pollers), `SystemOverload` (DB overload). |
| **Actual RPS vs Host RPS Limit** | Actual frontend request rate per instance overlaid with the configured host-level RPS limit (`host_rps_limit`). When traffic approaches the limit line, expect `RpsLimit` throttle errors. Adjust `frontend.rps` dynamic config if needed. |
| **Actual RPS vs Namespace Host RPS Limit** | Actual frontend request rate per namespace overlaid with the configured namespace-level host RPS limit (`namespace_host_rps_limit`). Useful for identifying which namespaces are approaching their per-namespace limits. Adjust `frontend.namespaceRPS` dynamic config if needed. |

---

### 7. Busy Workflow Throttling

Tracks throttling events specific to the `BusyWorkflow` cause. This occurs when the cluster cannot process updates for a specific workflow execution fast enough, which is common in fan-out use cases or when DB latency is elevated.

> **Note:** Some dynamic configs such as `EnableWorkflowIdReuseStartTimeValidation` can also introduce `BusyWorkflow` throttling for operations like `Start`/`SignalWithStart`.

| Panel | Description |
|---|---|
| **Transfer Active Task Errors Discarded** | Rate of active transfer task errors that were discarded. Indicates the cluster gave up retrying a task after repeated failures. |
| **Transfer Active Task Errors Limit Exceeded** | Rate of active transfer task errors caused by internal processing rate limit exceeded conditions. |
| **Transfer Active Task Errors Workflow Busy** | Rate of active transfer task errors caused by `WorkflowBusy`. **Primary signal for busy workflow throttling.** A sustained rate typically indicates a workflow type receiving very high update rates, or elevated DB latency causing workflow locks to be held longer than expected. |
| **Transfer Active Task Errors Throttled** | Rate of throttled active transfer task errors broken down by namespace and resource exhausted cause. Useful for identifying which namespaces are experiencing the most busy workflow throttling. |

---

### 8. Shard Movement

Tracks history service shard creation, removal, and closing. Shard movement most commonly occurs during cluster restarts and history host scaling events, but can also be triggered by elevated DB latency. Affected executions may experience temporarily elevated latencies during shard movement.

| Panel | Description |
|---|---|
| **Shards Created** | Rate of shard items being created on the History service. A spike typically correlates with a cluster restart or a new history host coming online. |
| **Shards Removed** | Rate of shard items being removed. Combined with Shards Created, gives a picture of shard rebalancing activity. |
| **Shards Closed** | Rate of shard items being closed. Shards are closed when a history host loses ownership, typically during a restart or scale-down event. |
| **Service Restarts** | Rate of service restarts across all Temporal services broken down by service name. Frequent restarts of the History service in particular can cause repeated shard movement and elevated latencies. |

---

### 9. History Timer Task Info

Tracks metrics for timer tasks in the History service. Use this group to monitor timer task throughput, error rates, and latencies which can indicate issues with scheduled workflow timers and activity timeouts.

| Panel | Description |
|---|---|
| **Total Timer Tasks Processed** | Total rate of timer tasks processed across all active timer operations on the History service. A sustained drop may indicate the timer queue is stalling. |
| **Total Timer Tasks Errors** | Total rate of errors for timer tasks across all active timer operations. Any sustained error rate warrants investigation as it can delay workflow timers and activity timeouts. |
| **Timer Task Processing Latency** | Processing latency for active timer tasks on the History service broken down by operation at the selected percentile. High values indicate the History service is slow to execute timer tasks after picking them up. |
| **Timer Task Scheduling Latency** | Scheduling lag for timer tasks reflecting how far behind the timer queue is from its scheduled fire time. High values mean timers are firing later than expected, which directly affects workflow timer and timeout accuracy. |

---

### 10. Workflow Stats

Tracks workflow completion outcomes and limit exceeded events.

| Panel | Description |
|---|---|
| **Workflow Success** | Rate of successfully completed workflow executions for the selected namespace. |
| **Workflow Cancel** | Rate of cancelled workflow executions. |
| **Workflow Failed** | Rate of failed workflow executions. Turns red at any non-zero value. |
| **Workflow Timeout** | Rate of workflow executions that timed out, indicating they exceeded their configured execution timeout. |
| **Workflow Terminate** | Rate of workflow executions that were explicitly terminated. A high rate may indicate operator intervention or automated termination policies. |
| **Workflow Continued As New** | Rate of workflow executions that continued as new. A normal pattern for long-running workflows to reset their history size. |
| **Workflow Limit Exceeded** | Rate of workflow tasks failed because they would exceed internal Temporal limits: `wf_too_many_pending_activities` (default limit: 2,000), `wf_too_many_pending_child_workflows` (default: 1,000), `wf_too_many_pending_cancel_requests` (default: 500). These limits exist to protect cluster stability. |
| **Blob Size Errors** | Rate of requests failed because a payload exceeded the configured blob size limit (`BlobSizeLimitError`). Any value above zero indicates SDK-side payloads that are too large and should be moved to external storage. |

---

### 11. Workflow Execution History Info

Tracks the size of workflow execution history, event counts, mutable state, and payload sizes. Use this group to identify workflows with growing history or oversized payloads that could impact cluster performance.

| Panel | Description |
|---|---|
| **Workflow History Size** | P-selected percentile of workflow execution history size in bytes by namespace. Workflows approaching the history size limit (default: 50MB error, 10MB warning) should be refactored to use continue-as-new. |
| **Workflow History Event Count** | P-selected percentile of the number of events in workflow execution history by namespace (`history_count` metric). Workflows approaching the event count limit (default: 51,200 events) should use continue-as-new. |
| **Mutable State Size** | P-selected percentile of the mutable state size in bytes, emitted on every read and write. Includes all pending activities, timers, child workflows, signals, and other in-flight state. Large mutable state increases per-operation DB write size and workflow lock hold time. |
| **Persisted Mutable State Size** | P-selected percentile of the mutable state size emitted only on DB writes. A more focused view of the DB write cost per execution update, not inflated by read operations. |
| **Event Blob Size** | P-selected percentile of individual workflow history event blob sizes. Large event blobs (e.g. from large activity inputs or results stored in history) increase the cost of history appends and reads. |
| **Search Attributes Size** | P-selected percentile of search attributes payload size. Large payloads increase visibility indexing costs. |
| **Memo Size** | P-selected percentile of workflow memo payload size. Large memos increase the size of workflow metadata stored in the persistence layer. |

---

### 12. SDK Workers Info

Tracks many metrics useful for troubleshooting SDK workers, including task dispatch latencies, task backlogs, timeouts, and sync match rates.

| Panel | Description |
|---|---|
| **Schedule to Start Latencies** | Latency from when a task is scheduled to when it is picked up by an SDK worker poller, by task type and operation. High values are the primary indicator of insufficient worker provisioning. Thresholds: 200ms orange, 1s red. |
| **Tasks Persisted to DB (can indicate task backlog)** | Rate of `CreateTasks` persistence requests. When tasks cannot be dispatched to a worker within the sync match window (default 500ms), they are persisted as a backlog. A sustained increase indicates workers are not keeping up with the task dispatch rate. |
| **Sync Match Latency** | Sync match latency by operation on the Matching service. Sync match dispatches tasks directly to a waiting poller within the sync match duration. A high sync match rate with low latency indicates healthy worker connectivity. |
| **Activity StartToClose Timeout** | Rate of activity executions that exceeded their `StartToClose` timeout. May indicate activities taking longer than expected or workers crashing during execution. |
| **Activity ScheduleToStart Timeout** | Rate of activity executions that exceeded their `ScheduleToStart` timeout. A high rate is a strong signal that workers are not polling fast enough to pick up activity tasks in time. |
| **Activity Heartbeat Timeout** | Rate of activity executions that exceeded their heartbeat timeout. Typically indicates workers crashing or hanging during long-running activities. |
| **Workflow Task StartToClose Timeouts (sticky tq)** | Rate of workflow task `StartToClose` timeouts on sticky task queues. Typically indicates sticky workers restarting or being overwhelmed. |
| **Approximate Task Backlog** | Approximate number of tasks waiting in the Matching service queue, by namespace and task type. A growing backlog is a key signal for worker scaling decisions. |

---

### 13. Pollers

Tracks the number of concurrent long-poll requests from SDK workers to the Frontend service, reflecting how many workers are actively waiting for tasks.

| Panel | Description |
|---|---|
| **Total Concurrent Pollers** | Total number of concurrent long-poll (pending) requests on the Frontend service for the selected namespace. A sudden drop to zero means all workers have disconnected. |
| **Max Concurrent Pollers per Frontend Pod** | Maximum number of concurrent long-poll requests per frontend instance. Useful for identifying uneven poller distribution across frontend instances, which may indicate load balancer misconfiguration. |

---

### 14. Visibility

Tracks the performance and availability of the Temporal Visibility store, which powers workflow search and listing APIs. Backed by either Elasticsearch (advanced visibility) or the primary database (standard visibility).

| Panel | Description |
|---|---|
| **Visibility Latencies per Operation** | Latency of visibility tasks on the History service broken down by operation at the selected percentile. High values affect the freshness of workflow search results and the performance of list/search APIs. |
| **Visibility Availability** | Percentage of visibility-related service requests that succeeded, shown as a gauge. Covers `ListWorkflowExecutions`, `CountWorkflowExecutions`, `ScanWorkflowExecutions` and similar. Thresholds: 99% green, 95% orange. |
| **Visibility Task End-to-End Latencies** | End-to-end queue latency for visibility tasks — from when a task is generated to when it is processed. High values mean workflow state changes are taking longer to appear in visibility search results. |
| **Visibility Task Processing by Operation** | Processing latency of individual visibility tasks broken down by operation. Isolates the time spent in the visibility write itself, separate from time spent waiting in the queue. |

---

### 15. Cluster Replication

Tracks metrics related to multi-cluster replication from the **active cluster's perspective** — task generation, send throughput, and stream health toward standby clusters. This group is only relevant when running Temporal in a multi-cluster configuration with active replication between clusters.

> **Standby-side monitoring:** This section shows what the active cluster is sending. To monitor replication fidelity and readiness from the standby side — including lag, DLQ depth, standby task retries, and failover readiness — use the dedicated [Temporal Standby Cluster — Replication Health](#related-resources) dashboard.

> **DLQ panels:** Panels marked **⚠️ Cassandra Only** rely on the history task DLQ which is only implemented for Cassandra persistence backends. On PostgreSQL or MySQL these panels will not emit data. The `history.TaskDLQEnabled` dynamic config must also be `true` (default).

| Panel | Description |
|---|---|
| **Replication Task Throughput** | Rate of replication tasks received, applied, failed, and skipped on a single panel. The received and applied rates should track closely. A growing gap or a sustained failed rate indicates replication processing problems. |
| **Backfill and Duplicate Events** | Rate of backfill replication tasks processed and duplicate replication events detected. Backfill is used to catch up after replication lag. A sustained high duplicate rate outside of failover may indicate a replication loop. |
| **Send and Recv Backlog** | Average backlog depth on both the sender and receiver sides. A growing send backlog means the source cluster is generating tasks faster than they are being sent. A growing recv backlog means the destination cluster is receiving tasks faster than it can apply them. |
| **Tasks Fetched per Batch and Attempts per Task** | Average tasks fetched per batch and average attempts per task. A high attempts-per-task value indicates tasks are repeatedly failing and being retried. |
| **Replication Latencies** | End-to-end, send, queue, processing, and transmission latencies at the selected percentile on a single panel. End-to-end latency is the primary replication health SLO metric. |
| **Backfill Latency** | Latency of replication backfill task processing. High values indicate the cluster is struggling to catch up on historical replication tasks. |
| **Replication Errors by Type** | Rate of replication task errors broken down by error type. Useful for diagnosing specific causes of replication failures. |
| **Stream Health** | Rate of stream-level signals: `replication_stream_stuck` (most critical — stream has stopped making progress), `replication_stream_error`, `replication_stream_panic`, and `replication_stream_channel_full` (buffer pressure). Any non-zero value on stream stuck warrants immediate investigation. |
| **Sender Rate Limit Latency** | Time the active cluster spent rate-limiting sends to standby clusters, controlled by the `history.ReplicationEnableRateLimit` dynamic config. Elevated values here are the active-side cause of standby lag that may have no other visible signal on the standby itself. |
| **Replication Task Generation and Load Latency** | Generation latency measures the time from a workflow event to replication task creation. Load latency measures the persistence schedule-to-start for replication tasks. High values here indicate the active cluster is slow to produce replication tasks, which contributes to end-to-end replication lag. |
| **Outlier Namespaces** | Namespaces that are disproportionately contributing to replication problems. Useful for isolating a single namespace as the root cause of broader replication degradation. |
| **DLQ Writes and Failures ⚠️ Cassandra Only** | Rate of tasks being written to the history task DLQ and failures to write to the DLQ. A non-zero DLQ enqueue failure rate is more severe than tasks landing in DLQ — it means tasks have failed all retries AND cannot be preserved for later inspection. Use `tdbg dlq` to inspect DLQ contents. |

---

### 16. Authorization

Tracks authorization-related metrics including denied requests, authorization system failures, and latency of authorization checks. Only relevant when an authorization plugin is configured on the cluster.

| Panel | Description |
|---|---|
| **Unauthorized Requests** | Rate of requests denied by the authorization system, broken down by namespace and operation. Means auth worked and said no. A sustained rate may indicate misconfigured permissions or clients using incorrect credentials. |
| **Authorization System Failures** | Rate of authorization system failures (e.g. plugin crash, misconfiguration, network failure to an external auth service). This is distinct from a request being denied and is **the more urgent signal**. Turns red at any value above zero. |
| **Authorization Check Latency** | Latency of authorization checks by operation at the selected percentile. High auth latency adds directly to end-to-end API latency. Thresholds: 300ms orange, 500ms red. |

---

---

## Threshold Reference Lines

Several panels in this dashboard include **visual threshold reference lines** — horizontal lines drawn across the chart at meaningful latency, size, or count values. These are distinct from alert rules: they are passive visual guides that help you spot when a metric is approaching or exceeding a meaningful boundary without requiring any alerting infrastructure.

> **These thresholds are starting points, not absolute rules.** Every Temporal deployment is different — cluster size, workload characteristics, persistence backend performance, and SLO requirements all vary. Treat the values below as a baseline to get you oriented. You should expect to adjust them over time as you observe your own cluster's normal operating ranges. A threshold that fires constantly on a large busy cluster may be perfectly appropriate for a smaller one.

> **Threshold lines only appear when your data is close to or above them.** Grafana auto-scales the y-axis to fit your actual data, so a threshold line set at 300ms will not be visible on a panel where all values are below 30ms — the line exists above the visible chart area. If you don't see the dashed lines, it means your cluster is performing well within the threshold bounds, which is good. To make the lines always visible as a reference, lower the threshold values to be closer to your observed normal operating range.

The following panels have threshold reference lines configured:

### Shard and Workflow Lock Latencies

| Panel | Orange | Red |
|---|---|---|
| **Shard Lock Latency** | 150ms | 300ms |
| **Workflow Lock Latency** | 200ms | 400ms |

### Persistence

| Panel | Orange | Red |
|---|---|---|
| **Persistence Latencies** | 300ms | 1s |

### Service Latencies

| Panel | Orange | Red | Notes |
|---|---|---|---|
| **Frontend Service Latency** | 300ms | 2s | `PollWorkflowTaskQueue` and `PollActivityTaskQueue` excluded — long-poll operations can legitimately run up to 60–70s |
| **History Service Latency** | 400ms | 2s | |
| **Matching Service Latency** | 400ms | 2s | `MatchingClientGetTaskQueueUserData` excluded — can legitimately run up to 5 minutes |

### History Timer Task Info

| Panel | Orange | Red |
|---|---|---|
| **Timer Task Processing Latency** | 300ms | 2s |

### Workflow Execution History Info

| Panel | Orange | Red | Notes |
|---|---|---|---|
| **Workflow History Size** | 4 MB | 30 MB | Dynamic config warn limit is 10MB, error limit is 50MB — reference lines set conservatively below those |
| **Workflow History Event Count** | 4,096 events | 30,720 events | Dynamic config warn limit is 10,240, error limit is 51,200 |
| **Mutable State Size** | 2 MB | 10 MB | Dynamic config warn limit is 1MB, error limit is 8MB — adjust if your workloads regularly carry large pending state |

### SDK Workers Info

| Panel | Orange | Red |
|---|---|---|
| **Schedule to Start Latencies** | 200ms | 1s |
| **Tasks Persisted to DB** | 1,000 req/s | 2,000 req/s |
| **Approximate Task Backlog** | 1,000 tasks | 2,000 tasks |

### Visibility

| Panel | Orange | Red |
|---|---|---|
| **Visibility Latencies per Operation** | 3s | 5s |
| **Visibility Task End-to-End Latencies** | 3s | 5s |
| **Visibility Task Processing by Operation** | 2s | 5s |

### Cluster Replication

| Panel | Orange | Red |
|---|---|---|
| **Replication Latencies** | 2s | 4s |
| **Sender Rate Limit Latency** | 2s | 5s |
| **Replication Task Generation and Load Latency** | 2s | 5s |

### Authorization

| Panel | Orange | Red |
|---|---|---|
| **Authorization Check Latency** | 300ms | 500ms |

---

## Related Resources

- [Temporal Standby Cluster — Replication Health Dashboard](./temporal-standby-replication-README.md)
- [Temporal Server Metrics Reference](https://docs.temporal.io/references/cluster-metrics)
- [Temporal Server metric_defs.go](https://github.com/temporalio/temporal/blob/main/common/metrics/metric_defs.go)
- [Temporal Self-Hosted Monitoring Guide](https://docs.temporal.io/self-hosted-guide/monitoring)
- [Temporal Community Forum](https://community.temporal.io)