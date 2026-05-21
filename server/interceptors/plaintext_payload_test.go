package interceptors

import (
	"context"
	"errors"
	"testing"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	querypb "go.temporal.io/api/query/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	updatepb "go.temporal.io/api/update/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/metrics"
	"go.temporal.io/server/common/metrics/metricstest"
	"google.golang.org/grpc"
)

// payloads builds a single-element Payloads with the given encoding.
func payloads(encoding string) *commonpb.Payloads {
	return &commonpb.Payloads{
		Payloads: []*commonpb.Payload{{
			Metadata: map[string][]byte{"encoding": []byte(encoding)},
			Data:     []byte("data"),
		}},
	}
}

// singlePayload builds a bare *Payload with the given encoding.
func singlePayload(encoding string) *commonpb.Payload {
	return &commonpb.Payload{
		Metadata: map[string][]byte{"encoding": []byte(encoding)},
		Data:     []byte("data"),
	}
}

// newTestInterceptor creates an interceptor backed by an isolated metricstest.Handler.
func newTestInterceptor(t *testing.T) (*PlainTextPayloadInterceptor, *metricstest.Handler) {
	t.Helper()
	h, err := metricstest.NewHandler(log.NewNoopLogger(), metrics.ClientConfig{})
	if err != nil {
		t.Fatalf("metricstest.NewHandler: %v", err)
	}
	return NewPlainTextPayloadInterceptor(log.NewNoopLogger(), h), h
}

// intercept calls Intercept with a no-op handler.
func intercept(t *testing.T, i *PlainTextPayloadInterceptor, method string, req any) {
	t.Helper()
	_, _ = i.Intercept(
		context.Background(), req,
		&grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, req any) (any, error) { return nil, nil },
	)
}

// otelTags returns the OTel-scope labels that the Prometheus exporter appends
// to every metric. These must be included in the exact-match tag set.
func otelTags() []metrics.Tag {
	return []metrics.Tag{
		metrics.StringTag("otel_scope_name", "temporal"),
		metrics.StringTag("otel_scope_version", ""),
	}
}

// assertCounterEmitted fails the test if the named counter with exact tags was not recorded at the expected value.
// It automatically appends the OTel-scope labels that the Prometheus exporter adds.
func assertCounterEmitted(t *testing.T, h *metricstest.Handler, want float64, tags ...metrics.Tag) {
	t.Helper()
	snap, err := h.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	allTags := append(tags, otelTags()...)
	got, err := snap.Counter(metricPlaintextPayload, allTags...)
	if err != nil {
		t.Errorf("counter %q not found with expected tags: %v", metricPlaintextPayload, err)
		return
	}
	if got != want {
		t.Errorf("counter = %v, want %v", got, want)
	}
}

// assertNoCounter fails the test if ANY counter was emitted (regardless of tags).
func assertNoCounter(t *testing.T, h *metricstest.Handler) {
	t.Helper()
	snap, err := h.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Pass no tags — the only way this succeeds is if a counter was emitted
	// with zero tags, which never happens. Any ErrMetricLabelMismatch or
	// ErrMetricNotFound both mean "counter not present with any tags", but
	// to detect a counter emitted with real tags we try with no tags.
	_, err = snap.Counter(metricPlaintextPayload)
	if err == nil {
		t.Errorf("expected no counter but one was recorded")
		return
	}
	if !errors.Is(err, metricstest.ErrMetricNotFound) && !errors.Is(err, metricstest.ErrMetricLabelMismatch) {
		t.Errorf("unexpected snapshot error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// StartWorkflowExecution
// ---------------------------------------------------------------------------

func TestStartWorkflow_PlaintextEmitsCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "StartWorkflowExecution", &workflowservice.StartWorkflowExecutionRequest{
		Namespace:    "test-ns",
		WorkflowType: &commonpb.WorkflowType{Name: "MyWorkflow"},
		TaskQueue:    &taskqueuepb.TaskQueue{Name: "my-tq"},
		Input:        payloads("json/plain"),
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("StartWorkflowExecution"),
		metricPayloadField("input"),
		metricEncoding("json/plain"),
		metricWorkflowType("MyWorkflow"),
		metricTaskQueue("my-tq"),
	)
}

func TestStartWorkflow_EncryptedNoCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "StartWorkflowExecution", &workflowservice.StartWorkflowExecutionRequest{
		Namespace:    "test-ns",
		WorkflowType: &commonpb.WorkflowType{Name: "MyWorkflow"},
		TaskQueue:    &taskqueuepb.TaskQueue{Name: "my-tq"},
		Input:        payloads("json/encrypted"),
	})
	assertNoCounter(t, h)
}

func TestStartWorkflow_BinaryNullExcluded(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "StartWorkflowExecution", &workflowservice.StartWorkflowExecutionRequest{
		Namespace:    "test-ns",
		WorkflowType: &commonpb.WorkflowType{Name: "MyWorkflow"},
		TaskQueue:    &taskqueuepb.TaskQueue{Name: "my-tq"},
		Input:        payloads("binary/null"),
	})
	assertNoCounter(t, h)
}

func TestStartWorkflow_BinaryPlainEmitsCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "StartWorkflowExecution", &workflowservice.StartWorkflowExecutionRequest{
		Namespace:    "test-ns",
		WorkflowType: &commonpb.WorkflowType{Name: "MyWorkflow"},
		TaskQueue:    &taskqueuepb.TaskQueue{Name: "my-tq"},
		Input:        payloads("binary/plain"),
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("StartWorkflowExecution"),
		metricPayloadField("input"),
		metricEncoding("binary/plain"),
		metricWorkflowType("MyWorkflow"),
		metricTaskQueue("my-tq"),
	)
}

// ---------------------------------------------------------------------------
// SignalWorkflowExecution (client → workflow, no wfType/taskqueue tags)
// ---------------------------------------------------------------------------

func TestSignalWorkflow_PlaintextEmitsCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "SignalWorkflowExecution", &workflowservice.SignalWorkflowExecutionRequest{
		Namespace: "test-ns",
		Input:     payloads("json/plain"),
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("SignalWorkflowExecution"),
		metricPayloadField("input"),
		metricEncoding("json/plain"),
		metricWorkflowType(""),
		metricTaskQueue(""),
	)
}

// ---------------------------------------------------------------------------
// QueryWorkflow
// ---------------------------------------------------------------------------

func TestQueryWorkflow_PlaintextQueryArgsEmitsCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "QueryWorkflow", &workflowservice.QueryWorkflowRequest{
		Namespace: "test-ns",
		Query: &querypb.WorkflowQuery{
			QueryArgs: payloads("json/plain"),
		},
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("QueryWorkflow"),
		metricPayloadField("query_args"),
		metricEncoding("json/plain"),
		metricWorkflowType(""),
		metricTaskQueue(""),
	)
}

// ---------------------------------------------------------------------------
// UpdateWorkflowExecution
// ---------------------------------------------------------------------------

func TestUpdateWorkflow_PlaintextArgsEmitsCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "UpdateWorkflowExecution", &workflowservice.UpdateWorkflowExecutionRequest{
		Namespace: "test-ns",
		Request: &updatepb.Request{
			Input: &updatepb.Input{
				Args: payloads("json/plain"),
			},
		},
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("UpdateWorkflowExecution"),
		metricPayloadField("update_args"),
		metricEncoding("json/plain"),
		metricWorkflowType(""),
		metricTaskQueue(""),
	)
}

// ---------------------------------------------------------------------------
// RespondWorkflowTaskCompleted — commands
// ---------------------------------------------------------------------------

func TestRespondWFTaskCompleted_ScheduleActivityPlaintext(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "RespondWorkflowTaskCompleted", &workflowservice.RespondWorkflowTaskCompletedRequest{
		Namespace: "test-ns",
		Commands: []*commandpb.Command{{
			CommandType: enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK,
			Attributes: &commandpb.Command_ScheduleActivityTaskCommandAttributes{
				ScheduleActivityTaskCommandAttributes: &commandpb.ScheduleActivityTaskCommandAttributes{
					Input: payloads("json/plain"),
				},
			},
		}},
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("RespondWorkflowTaskCompleted"),
		metricPayloadField("ScheduleActivityTask"),
		metricEncoding("json/plain"),
		metricWorkflowType(""),
		metricTaskQueue(""),
	)
}

func TestRespondWFTaskCompleted_StartChildPlaintext(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "RespondWorkflowTaskCompleted", &workflowservice.RespondWorkflowTaskCompletedRequest{
		Namespace: "test-ns",
		Commands: []*commandpb.Command{{
			CommandType: enumspb.COMMAND_TYPE_START_CHILD_WORKFLOW_EXECUTION,
			Attributes: &commandpb.Command_StartChildWorkflowExecutionCommandAttributes{
				StartChildWorkflowExecutionCommandAttributes: &commandpb.StartChildWorkflowExecutionCommandAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "ChildWorkflow"},
					TaskQueue:    &taskqueuepb.TaskQueue{Name: "child-tq"},
					Input:        payloads("json/plain"),
				},
			},
		}},
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("RespondWorkflowTaskCompleted"),
		metricPayloadField("StartChildWorkflowExecution"),
		metricEncoding("json/plain"),
		metricWorkflowType("ChildWorkflow"),
		metricTaskQueue("child-tq"),
	)
}

// ---------------------------------------------------------------------------
// RespondWorkflowTaskFailed — failure encoded_attributes
// ---------------------------------------------------------------------------

func TestRespondWFTaskFailed_EncodedAttributesPlaintext(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "RespondWorkflowTaskFailed", &workflowservice.RespondWorkflowTaskFailedRequest{
		Namespace: "test-ns",
		Failure: &failurepb.Failure{
			Message:          "boom",
			EncodedAttributes: singlePayload("json/plain"),
		},
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("RespondWorkflowTaskFailed"),
		metricPayloadField("encoded_attributes"),
		metricEncoding("json/plain"),
		metricWorkflowType(""),
		metricTaskQueue(""),
	)
}

// ---------------------------------------------------------------------------
// RespondActivityTaskFailed — failure + heartbeat details
// ---------------------------------------------------------------------------

func TestRespondActivityTaskFailed_ApplicationFailureDetails(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "RespondActivityTaskFailed", &workflowservice.RespondActivityTaskFailedRequest{
		Namespace: "test-ns",
		Failure: &failurepb.Failure{
			Message: "app error",
			FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
				ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
					Details: payloads("json/plain"),
				},
			},
		},
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("RespondActivityTaskFailed"),
		metricPayloadField("ApplicationFailureInfo.details"),
		metricEncoding("json/plain"),
		metricWorkflowType(""),
		metricTaskQueue(""),
	)
}

// ---------------------------------------------------------------------------
// RecordActivityTaskHeartbeat
// ---------------------------------------------------------------------------

func TestHeartbeat_PlaintextDetailsEmitsCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "RecordActivityTaskHeartbeat", &workflowservice.RecordActivityTaskHeartbeatRequest{
		Namespace: "test-ns",
		Details:   payloads("json/plain"),
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("RecordActivityTaskHeartbeat"),
		metricPayloadField("details"),
		metricEncoding("json/plain"),
		metricWorkflowType(""),
		metricTaskQueue(""),
	)
}

// ---------------------------------------------------------------------------
// ExecuteMultiOperation (UpdateWithStart) — delegates to inner Start
// ---------------------------------------------------------------------------

func TestExecuteMultiOperation_InnerStartPlaintext(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "ExecuteMultiOperation", &workflowservice.ExecuteMultiOperationRequest{
		Namespace: "test-ns",
		Operations: []*workflowservice.ExecuteMultiOperationRequest_Operation{{
			Operation: &workflowservice.ExecuteMultiOperationRequest_Operation_StartWorkflow{
				StartWorkflow: &workflowservice.StartWorkflowExecutionRequest{
					Namespace:    "test-ns",
					WorkflowType: &commonpb.WorkflowType{Name: "MyWorkflow"},
					TaskQueue:    &taskqueuepb.TaskQueue{Name: "my-tq"},
					Input:        payloads("json/plain"),
				},
			},
		}},
	})
	// operation tag is "ExecuteMultiOperation" — the outer op name passed to check()
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("ExecuteMultiOperation"),
		metricPayloadField("input"),
		metricEncoding("json/plain"),
		metricWorkflowType("MyWorkflow"),
		metricTaskQueue("my-tq"),
	)
}

// ---------------------------------------------------------------------------
// CreateSchedule — workflow input only, not the spec
// ---------------------------------------------------------------------------

func TestCreateSchedule_WorkflowInputPlaintext(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "CreateSchedule", &workflowservice.CreateScheduleRequest{
		Namespace: "test-ns",
		Schedule: &schedulepb.Schedule{
			Action: &schedulepb.ScheduleAction{
				Action: &schedulepb.ScheduleAction_StartWorkflow{
					StartWorkflow: &workflowpb.NewWorkflowExecutionInfo{
						WorkflowType: &commonpb.WorkflowType{Name: "ScheduledWorkflow"},
						TaskQueue:    &taskqueuepb.TaskQueue{Name: "sched-tq"},
						Input:        payloads("json/plain"),
					},
				},
			},
		},
	})
	assertCounterEmitted(t, h, 1,
		metrics.NamespaceTag("test-ns"),
		metricOperation("CreateSchedule"),
		metricPayloadField("input"),
		metricEncoding("json/plain"),
		metricWorkflowType("ScheduledWorkflow"),
		metricTaskQueue("sched-tq"),
	)
}

// ---------------------------------------------------------------------------
// Nil input — no panic, no counter
// ---------------------------------------------------------------------------

func TestStartWorkflow_NilInputNoCounter(t *testing.T) {
	i, h := newTestInterceptor(t)
	intercept(t, i, "StartWorkflowExecution", &workflowservice.StartWorkflowExecutionRequest{
		Namespace:    "test-ns",
		WorkflowType: &commonpb.WorkflowType{Name: "MyWorkflow"},
		TaskQueue:    &taskqueuepb.TaskQueue{Name: "my-tq"},
		Input:        nil,
	})
	assertNoCounter(t, h)
}
