# server/interceptors

Custom gRPC interceptors wired into the Temporal frontend via `temporal.WithChainedFrontendGrpcInterceptors`. Each interceptor is observe-only — requests are always allowed through.

## Table of contents

- [PlainTextPayloadInterceptor](#plaintextpayloadinterceptor)
  - [What it does](#what-it-does)
  - [Covered APIs](#covered-apis)
  - [Metric](#metric)
  - [Log output](#log-output)
  - [Grafana / PromQL](#grafana--promql)
  - [Wiring](#wiring)

---

## PlainTextPayloadInterceptor

### What it does

Scans inbound frontend API calls for payloads whose `encoding` metadata indicates they are unencrypted (`json/plain` or `binary/plain`). When an unencrypted payload is detected it:

1. Increments a Prometheus counter (`plaintext_payload_detected_total`) tagged with enough context for cluster admins to identify the offending tenant, workflow type, and task queue.
2. Logs a `WARN` line with the same fields via the server's structured logger.

The request is always allowed through. This interceptor is intended to give operators visibility into tenants who have not yet deployed a payload codec — it is not a hard enforcement gate. See [Extending to block requests](#extending-to-block-requests) if you want to harden it later.

`binary/null` (the encoding for `nil` / empty payloads) is intentionally excluded — null payloads carry no data and are not a privacy concern.

### Covered APIs

The interceptor checks the following frontend methods. Each entry shows which metric tags are populated beyond the always-present `namespace`, `operation`, `payload_field`, and `encoding` tags.

| API method | Payload field(s) checked | `workflow_type` | `task_queue` |
|---|---|---|---|
| `StartWorkflowExecution` | `input` | ✓ | ✓ |
| `SignalWithStartWorkflowExecution` | `input`, `signal_input` | ✓ | ✓ |
| `SignalWorkflowExecution` | `input` | — | — |
| `QueryWorkflow` | `query.query_args` | — | — |
| `UpdateWorkflowExecution` | `request.input.args` | — | — |
| `RespondActivityTaskCompleted` | `result` | — | — |
| `RecordActivityTaskHeartbeat` | `details` | — | — |
| `RespondWorkflowTaskCompleted` | per-command (see below) | ✓ (child/CAN only) | ✓ (child only) |
| `RespondWorkflowTaskFailed` | `failure` (see below) | — | — |
| `RespondActivityTaskFailed` | `failure` (see below), `last_heartbeat_details` | — | — |
| `ExecuteMultiOperation` | delegates to inner `StartWorkflow` and `UpdateWorkflow` operations | ✓ (from Start) | ✓ (from Start) |
| `CreateSchedule` | `schedule.action.start_workflow.input` only — spec is not scanned | ✓ | ✓ |
| `UpdateSchedule` | `schedule.action.start_workflow.input` only — spec is not scanned | ✓ | ✓ |

For `RespondWorkflowTaskCompleted`, the interceptor walks the `commands` list and checks:

| Command type | Field checked | `payload_field` tag value |
|---|---|---|
| `ScheduleActivityTask` | `input` | `ScheduleActivityTask` |
| `CompleteWorkflowExecution` | `result` | `CompleteWorkflowExecution` |
| `CancelWorkflowExecution` | `details` | `CancelWorkflowExecution` |
| `SignalExternalWorkflowExecution` | `input` | `SignalExternalWorkflowExecution` |
| `StartChildWorkflowExecution` | `input` | `StartChildWorkflowExecution` |
| `ContinueAsNewWorkflowExecution` | `input` | `ContinueAsNewWorkflowExecution` |

The `payload_field` value for commands is derived from `cmd.GetCommandType().String()` — the proto enum's own string representation — so it will always match the canonical command type name without any manual mapping.

#### Why workflowType and taskqueue are blank on worker-side operations

For worker-side calls (`RespondWorkflowTaskCompleted`, `RespondActivityTaskCompleted`, `RespondWorkflowTaskFailed`, `RespondActivityTaskFailed`, `RecordActivityTaskHeartbeat`) the `workflowType` and `taskqueue` metric tags will always be empty strings.

The reason: these requests do not carry workflow type or task queue as top-level fields. That context is encoded inside the opaque `task_token` bytes — a serialized internal server protobuf that the worker receives from `PollWorkflowTaskQueue` / `PollActivityTaskQueue` and echoes back verbatim. The server decodes it internally to route the response to the correct history shard; the frontend interceptor sees only raw bytes and cannot extract workflow or activity type without deserializing internal server data structures.

Exceptions within `RespondWorkflowTaskCompleted` commands: `StartChildWorkflowExecution` carries workflow type and task queue directly in its command attributes (the child's type and target queue), and `ContinueAsNewWorkflowExecution` carries the workflow type. These are populated where available — see the commands table above.

The tags are still always emitted as empty strings rather than omitted entirely. This is a Prometheus requirement: all time series for a given metric must share the same set of label names. If worker-side series omitted `workflowType` and `taskqueue` while client-side series included them, Prometheus would reject the inconsistent series. The empty-string value makes it unambiguous that the information is not available for this call type, rather than missing by accident.

**Why keep these metrics at all without type context?** A worker pod that ships without a data converter registered will show up as `RespondWorkflowTaskCompleted` or `RespondActivityTaskCompleted` unencrypted traffic even while `StartWorkflowExecution` remains clean — because the client still has the codec configured but the worker does not. Namespace alone is enough to identify the affected tenant and trigger an investigation.

#### Failure scanning

`RespondWorkflowTaskFailed` and `RespondActivityTaskFailed` carry a `Failure` message, which is walked recursively through its `cause` chain. The following fields are checked:

| Field | `payload_field` tag value | Notes |
|---|---|---|
| `Failure.encoded_attributes` | `encoded_attributes` | Single `Payload` produced by the failure converter. Unencrypted encoding here means the failure converter is not encrypting. |
| `ApplicationFailureInfo.details` | `ApplicationFailureInfo.details` | Application error details (`ApplicationError` in SDKs). |
| `CanceledFailureInfo.details` | `CanceledFailureInfo.details` | Cancellation details. |
| `TimeoutFailureInfo.last_heartbeat_details` | `TimeoutFailureInfo.last_heartbeat_details` | Last heartbeat payload on timeout. |

The `payload_field` values for failure-info fields are derived from the proto message descriptor name (e.g. `ApplicationFailureInfo.ProtoReflect().Descriptor().Name()`) combined with the field name, so they always match the canonical proto type name.

`RespondActivityTaskFailed` also carries `last_heartbeat_details` directly on the request — this is scanned as a regular `Payloads` field with `payload_field = "last_heartbeat_details"`.

### Metric

```
plaintext_payload_detected_total  (counter)
```

Tag key names match the conventions used by the Temporal server's own metrics package.

| Tag | Key name | Always present | Values / notes |
|---|---|---|---|
| namespace | `namespace` | ✓ | Via `metrics.NamespaceTag` |
| operation | `operation` | ✓ | Short API method name, e.g. `StartWorkflowExecution` |
| payload field | `payload_field` | ✓ | Payload field or command type: `input`, `result`, `query_args`, `ScheduleActivityTask`, etc. |
| encoding | `encoding` | ✓ | `json/plain` or `binary/plain` |
| workflow type | `workflowType` | ✓ | Populated for Start, SignalWithStart, StartChild, ContinueAsNew commands. Empty string for all other operations — see note below. |
| task queue | `taskqueue` | ✓ | Populated for Start, SignalWithStart, StartChild commands. Empty string for all other operations — see note below. |

Each counter increment represents one unencrypted `Payload` object. A single `StartWorkflowExecution` with three payloads in its input will produce three increments.

### Log output

Each detection emits a structured `WARN` line. Example (Zap JSON):

```json
{
  "level": "warn",
  "msg": "unencrypted payload detected",
  "namespace": "my-namespace",
  "operation": "StartWorkflowExecution",
  "payload_field": "input",
  "encoding": "json/plain",
  "workflow_type": "MyWorkflow",
  "task_queue": "my-task-queue"
}
```

### Grafana / PromQL

**Rate of unencrypted payloads by namespace — table panel**

```promql
sum by (namespace, operation, workflowType, taskqueue, payload_field) (
  rate(plaintext_payload_detected_total[5m])
)
```

Use as a table panel. Namespaces with a non-zero value have at least one worker or client sending unencrypted payloads.

**Alert: any unencrypted payload traffic in the last 10 minutes**

```promql
sum by (namespace, operation) (
  increase(plaintext_payload_detected_total[10m])
) > 0

```

Fire at severity `warning`. Label the alert with `namespace` so each offending tenant produces its own alert instance.

**Alert: sustained unencrypted traffic (not just a one-off)**

```promql
sum by (namespace, operation, workflowType, taskqueue) (
  rate(plaintext_payload_detected_total[30m])
) > 0
```

A 30-minute window filters out transient noise from development workflows. Use this to page the tenant.

**Breakdown by entry point — useful during a migration**

```promql
sum by (namespace, operation, payload_field, encoding) (
  rate(plaintext_payload_detected_total[5m])
)

```

Shows whether unencrypted payloads are coming in from client-side calls (`StartWorkflowExecution`, `SignalWorkflowExecution`) or from workers (`RespondWorkflowTaskCompleted`, `RespondActivityTaskCompleted`). Client-side and worker-side unencrypted traffic require different fixes from the tenant.

### Wiring

The interceptor is instantiated in `server/main.go` and passed the same `metricsHandler` used by the Temporal server and the etcd dynconfig client, so its metric is emitted into the same Prometheus registry on the same HTTP listener.

```go
plainTextInterceptor := interceptors.NewPlainTextPayloadInterceptor(logger, metricsHandler)

temporal.NewServer(
    // ...
    temporal.WithChainedFrontendGrpcInterceptors(plainTextInterceptor.Intercept),
)
```

The interceptor runs after the server's built-in auth and rate-limit interceptors, so `namespace` is already validated by the time `check` is called. No additional namespace lookup is needed.

