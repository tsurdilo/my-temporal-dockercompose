package interceptors

import (
	"context"

	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/server/common/api"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
	"go.temporal.io/server/common/metrics"
	"google.golang.org/grpc"
)

const metricPlaintextPayload = "plaintext_payload_detected_total"

// unencryptedEncodings is the set of encoding metadata values that indicate
// an unencrypted payload. binary/null is intentionally excluded — null payloads
// carry no data and are not a privacy concern.
var unencryptedEncodings = map[string]bool{
	"json/plain":   true,
	"binary/plain": true,
}

// Tag helpers for dimensions that have no existing function in the metrics/tag packages.
// Defined once here so the key strings are not scattered across the file.
func metricPayloadField(v string) metrics.Tag  { return metrics.StringTag("payload_field", v) }
func metricEncoding(v string) metrics.Tag      { return metrics.StringTag("encoding", v) }
func metricOperation(v string) metrics.Tag     { return metrics.StringTag("operation", v) }
func metricWorkflowType(v string) metrics.Tag  { return metrics.StringTag("workflowType", v) }
func metricTaskQueue(v string) metrics.Tag     { return metrics.StringTag("taskqueue", v) }

func logPayloadField(v string) tag.ZapTag { return tag.NewStringTag("payload_field", v) }
func logEncoding(v string) tag.ZapTag     { return tag.NewStringTag("encoding", v) }

// PlainTextPayloadInterceptor logs and increments a metric whenever a
// frontend API call carries a payload with an unencrypted encoding.
// The request is always allowed through — this interceptor is observe-only.
//
// Covered APIs: StartWorkflowExecution, SignalWorkflowExecution,
// SignalWithStartWorkflowExecution, QueryWorkflow, UpdateWorkflowExecution,
// ExecuteMultiOperation (UpdateWithStart), RecordActivityTaskHeartbeat,
// CreateSchedule, UpdateSchedule (workflow input only),
// RespondWorkflowTaskCompleted, RespondWorkflowTaskFailed,
// RespondActivityTaskCompleted, RespondActivityTaskFailed.
type PlainTextPayloadInterceptor struct {
	logger         log.Logger
	metricsHandler metrics.Handler
}

func NewPlainTextPayloadInterceptor(logger log.Logger, metricsHandler metrics.Handler) *PlainTextPayloadInterceptor {
	return &PlainTextPayloadInterceptor{
		logger:         logger,
		metricsHandler: metricsHandler,
	}
}

func (i *PlainTextPayloadInterceptor) Intercept(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	i.check(req, api.MethodName(info.FullMethod))
	return handler(ctx, req)
}

func (i *PlainTextPayloadInterceptor) check(req any, op string) {
	switch r := req.(type) {
	case *workflowservice.StartWorkflowExecutionRequest:
		i.scanPayloads(r.Namespace, r.GetWorkflowType().GetName(), r.GetTaskQueue().GetName(), op, "input", r.Input)

	case *workflowservice.SignalWithStartWorkflowExecutionRequest:
		ns, wfType, tq := r.Namespace, r.GetWorkflowType().GetName(), r.GetTaskQueue().GetName()
		i.scanPayloads(ns, wfType, tq, op, "input", r.Input)
		i.scanPayloads(ns, wfType, tq, op, "signal_input", r.SignalInput)

	case *workflowservice.SignalWorkflowExecutionRequest:
		i.scanPayloads(r.Namespace, "", "", op, "input", r.Input)

	case *workflowservice.QueryWorkflowRequest:
		i.scanPayloads(r.Namespace, "", "", op, "query_args", r.GetQuery().GetQueryArgs())

	case *workflowservice.UpdateWorkflowExecutionRequest:
		i.scanPayloads(r.Namespace, "", "", op, "update_args", r.GetRequest().GetInput().GetArgs())

	case *workflowservice.RespondActivityTaskCompletedRequest:
		i.scanPayloads(r.Namespace, "", "", op, "result", r.Result)

	case *workflowservice.RespondWorkflowTaskCompletedRequest:
		i.scanCommands(r.Namespace, op, r.Commands)

	case *workflowservice.CreateScheduleRequest:
		i.scanScheduleAction(r.Namespace, op, r.GetSchedule().GetAction())

	case *workflowservice.UpdateScheduleRequest:
		i.scanScheduleAction(r.Namespace, op, r.GetSchedule().GetAction())

	case *workflowservice.RecordActivityTaskHeartbeatRequest:
		i.scanPayloads(r.Namespace, "", "", op, "details", r.Details)

	case *workflowservice.ExecuteMultiOperationRequest:
		// UpdateWithStart: each operation is a StartWorkflow or UpdateWorkflow —
		// delegate to the existing cases so no payload logic is duplicated.
		for _, multiOp := range r.Operations {
			switch o := multiOp.Operation.(type) {
			case *workflowservice.ExecuteMultiOperationRequest_Operation_StartWorkflow:
				i.check(o.StartWorkflow, op)
			case *workflowservice.ExecuteMultiOperationRequest_Operation_UpdateWorkflow:
				i.check(o.UpdateWorkflow, op)
			}
		}

	case *workflowservice.RespondWorkflowTaskFailedRequest:
		i.scanFailure(r.Namespace, op, r.Failure)

	case *workflowservice.RespondActivityTaskFailedRequest:
		i.scanFailure(r.Namespace, op, r.Failure)
		i.scanPayloads(r.Namespace, "", "", op, "last_heartbeat_details", r.LastHeartbeatDetails)
	}
}

func (i *PlainTextPayloadInterceptor) scanCommands(ns, op string, cmds []*commandpb.Command) {
	for _, cmd := range cmds {
		location := cmd.GetCommandType().String() // e.g. "ScheduleActivityTask", "SignalExternalWorkflowExecution"
		var wfType, tq string
		var ps *commonpb.Payloads

		switch a := cmd.Attributes.(type) {
		case *commandpb.Command_ScheduleActivityTaskCommandAttributes:
			ps = a.ScheduleActivityTaskCommandAttributes.Input
		case *commandpb.Command_CompleteWorkflowExecutionCommandAttributes:
			ps = a.CompleteWorkflowExecutionCommandAttributes.Result
		case *commandpb.Command_CancelWorkflowExecutionCommandAttributes:
			ps = a.CancelWorkflowExecutionCommandAttributes.Details
		case *commandpb.Command_SignalExternalWorkflowExecutionCommandAttributes:
			ps = a.SignalExternalWorkflowExecutionCommandAttributes.Input
		case *commandpb.Command_StartChildWorkflowExecutionCommandAttributes:
			attrs := a.StartChildWorkflowExecutionCommandAttributes
			ps, wfType, tq = attrs.Input, attrs.GetWorkflowType().GetName(), attrs.GetTaskQueue().GetName()
		case *commandpb.Command_ContinueAsNewWorkflowExecutionCommandAttributes:
			attrs := a.ContinueAsNewWorkflowExecutionCommandAttributes
			ps, wfType = attrs.Input, attrs.GetWorkflowType().GetName()
		default:
			continue
		}
		i.scanPayloads(ns, wfType, tq, op, location, ps)
	}
}

// scanScheduleAction checks the workflow input payload inside a ScheduleAction.
// Only the StartWorkflow action carries a payload — the schedule spec is ignored.
func (i *PlainTextPayloadInterceptor) scanScheduleAction(ns, op string, action *schedulepb.ScheduleAction) {
	if action == nil {
		return
	}
	sw, ok := action.Action.(*schedulepb.ScheduleAction_StartWorkflow)
	if !ok || sw.StartWorkflow == nil {
		return
	}
	wf := sw.StartWorkflow
	i.scanPayloads(ns, wf.GetWorkflowType().GetName(), wf.GetTaskQueue().GetName(), op, "input", wf.Input)
}

// scanFailure walks a Failure and its cause chain, checking encoded_attributes
// and any failure-info-specific details payloads for unencrypted encodings.
func (i *PlainTextPayloadInterceptor) scanFailure(ns, op string, f *failurepb.Failure) {
	if f == nil {
		return
	}
	// encoded_attributes is the single Payload produced by the failure converter.
	// If it is present and unencrypted, the failure converter is not encrypting.
	i.scanPayload(ns, op, "encoded_attributes", f.EncodedAttributes)

	// Failure-info-specific details payloads — derive location from the proto
	// message descriptor name so it matches the canonical type name exactly.
	switch fi := f.FailureInfo.(type) {
	case *failurepb.Failure_ApplicationFailureInfo:
		info := fi.ApplicationFailureInfo
		loc := string(info.ProtoReflect().Descriptor().Name()) + ".details"
		i.scanPayloads(ns, "", "", op, loc, info.GetDetails())
	case *failurepb.Failure_CanceledFailureInfo:
		info := fi.CanceledFailureInfo
		loc := string(info.ProtoReflect().Descriptor().Name()) + ".details"
		i.scanPayloads(ns, "", "", op, loc, info.GetDetails())
	case *failurepb.Failure_TimeoutFailureInfo:
		info := fi.TimeoutFailureInfo
		loc := string(info.ProtoReflect().Descriptor().Name()) + ".last_heartbeat_details"
		i.scanPayloads(ns, "", "", op, loc, info.GetLastHeartbeatDetails())
	}

	// Walk the cause chain.
	i.scanFailure(ns, op, f.Cause)
}

// scanPayload checks a single Payload (used for Failure.encoded_attributes).
func (i *PlainTextPayloadInterceptor) scanPayload(ns, op, location string, p *commonpb.Payload) {
	if p == nil {
		return
	}
	i.scanPayloads(ns, "", "", op, location, &commonpb.Payloads{Payloads: []*commonpb.Payload{p}})
}

func (i *PlainTextPayloadInterceptor) scanPayloads(
	ns, wfType, taskQueue, op, location string,
	ps *commonpb.Payloads,
) {
	if ps == nil {
		return
	}
	for _, p := range ps.Payloads {
		encoding := string(p.GetMetadata()["encoding"])
		if !unencryptedEncodings[encoding] {
			continue
		}

		// All label keys must be present on every series — Prometheus rejects
		// series that share a metric name but have different label schemas.
		// Use empty string for workflowType/taskqueue when not available.
		// metricWorkflowType/metricTaskQueue use StringTag directly so that an
		// absent value emits "" rather than Temporal's "_unknown_" sentinel.
		metricTags := []metrics.Tag{
			metrics.NamespaceTag(ns),
			metricOperation(op),
			metricPayloadField(location),
			metricEncoding(encoding),
			metricWorkflowType(wfType),
			metricTaskQueue(taskQueue),
		}
		logTags := []tag.Tag{
			tag.WorkflowNamespace(ns),
			tag.Operation(op),
			logPayloadField(location),
			logEncoding(encoding),
		}
		if wfType != "" {
			logTags = append(logTags, tag.WorkflowType(wfType))
		}
		if taskQueue != "" {
			logTags = append(logTags, tag.WorkflowTaskQueueName(taskQueue))
		}

		i.metricsHandler.Counter(metricPlaintextPayload).Record(1, metricTags...)
		i.logger.Warn("unencrypted payload detected", logTags...)
	}
}
