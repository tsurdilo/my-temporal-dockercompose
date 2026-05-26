package archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	archiverspb "go.temporal.io/server/api/archiver/v1"
	"go.temporal.io/server/common/archiver"
	"go.temporal.io/server/common/codec"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
	"go.temporal.io/server/common/metrics"
	"go.temporal.io/server/common/persistence"
)

// eventTypeToStatus maps a terminal workflow event type to its corresponding
// WorkflowExecutionStatus. Returns -1 if the event type is not a terminal event.
var eventTypeToStatus = map[enumspb.EventType]enumspb.WorkflowExecutionStatus{
	enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:      enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
	enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:         enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
	enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:      enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
	enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:       enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED,
	enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:     enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED,
	enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW: enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW,
}

// MinioHistoryArchiver implements archiver.HistoryArchiver backed by MinIO.
type MinioHistoryArchiver struct {
	executionManager persistence.ExecutionManager
	logger           log.Logger
	metricsHandler   metrics.Handler
	s3cli            *s3.Client
	cfg              *Config
}

func newHistoryArchiver(
	executionManager persistence.ExecutionManager,
	logger log.Logger,
	metricsHandler metrics.Handler,
	cfg *Config,
) (*MinioHistoryArchiver, error) {
	s3cli, err := NewS3Client(cfg)
	if err != nil {
		return nil, fmt.Errorf("minio history archiver: build S3 client: %w", err)
	}
	return &MinioHistoryArchiver{
		executionManager: executionManager,
		logger:           logger,
		metricsHandler:   metricsHandler,
		s3cli:            s3cli,
		cfg:              cfg,
	}, nil
}

// ValidateURI verifies that the URI scheme is "minio" and has a non-empty hostname (bucket name).
func (h *MinioHistoryArchiver) ValidateURI(uri archiver.URI) error {
	if uri.Scheme() != Scheme {
		return archiver.ErrURISchemeMismatch
	}
	if uri.Hostname() == "" {
		return fmt.Errorf("%w: minio URI must include a bucket name as the hostname", archiver.ErrInvalidURI)
	}
	return nil
}

// Archive fetches the workflow's full history via the HistoryIterator, derives the
// workflow's terminal status from the last event, and — if the status is in the
// configured allowlist — encodes, gzip-compresses, and uploads the history as a
// single object to MinIO. If the status is not allowed, the method returns nil
// immediately (silent skip, nothing written).
func (h *MinioHistoryArchiver) Archive(
	ctx context.Context,
	uri archiver.URI,
	request *archiver.ArchiveHistoryRequest,
	opts ...archiver.ArchiveOption,
) error {
	featureCatalog := archiver.GetFeatureCatalog(opts...)
	logger := log.With(h.logger,
		tag.WorkflowNamespaceID(request.NamespaceID),
		tag.WorkflowID(request.WorkflowID),
		tag.WorkflowRunID(request.RunID),
	)

	if err := h.ValidateURI(uri); err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason(archiver.ErrReasonInvalidURI), tag.Error(err))
		return err
	}
	if err := archiver.ValidateHistoryArchiveRequest(request); err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason(archiver.ErrReasonInvalidArchiveRequest), tag.Error(err))
		return err
	}

	// Build the history iterator. If the activity has heartbeat state from a
	// previous attempt, try to restore it; fall back to a fresh iterator.
	iter := buildHistoryIterator(ctx, request, h.executionManager, featureCatalog)

	// Accumulate all history blobs. We must see the last blob before we know
	// the terminal status, so we cannot upload incrementally.
	var allHistories []*historypb.History

	for iter.HasNext() {
		blob, err := iter.Next(ctx)
		if err != nil {
			var notFound *serviceerror.NotFound
			if errors.As(err, &notFound) {
				// History already deleted (duplicate archival signal). Nothing to do.
				logger.Info(archiver.ArchiveSkippedInfoMsg)
				return nil
			}
			logger.Error(archiver.ArchiveTransientErrorMsg,
				tag.ArchivalArchiveFailReason(archiver.ErrReasonReadHistory), tag.Error(err))
			return err
		}

		if historyMutated(request, blob.Body, blob.Header.IsLast) {
			logger.Error(archiver.ArchiveNonRetryableErrorMsg,
				tag.ArchivalArchiveFailReason(archiver.ErrReasonHistoryMutated))
			return archiver.ErrHistoryMutated
		}

		allHistories = append(allHistories, blob.Body...)

		// Heartbeat so the activity doesn't time out during long iteration.
		saveIteratorState(ctx, featureCatalog, iter)

		if blob.Header.IsLast {
			// Derive terminal status from the last event.
			status, ok := terminalStatus(blob)
			if !ok {
				// IsLast=true but last event is not a recognised terminal event.
				// Archive defensively rather than silently dropping.
				logger.Warn("minio history archiver: could not derive status from last event; archiving anyway")
			} else if !h.cfg.StatusAllowed(status) {
				logger.Info("minio history archiver: skipping — status not in allowedStatuses",
					tag.NewStringTag("status", status.String()))
				return nil
			}

			// Status is allowed (or unknown). Encode, compress, and upload.
			return h.upload(ctx, uri, request, allHistories, logger, featureCatalog)
		}
	}

	// If the iterator is empty (no blobs at all), nothing to archive.
	return nil
}

func (h *MinioHistoryArchiver) upload(
	ctx context.Context,
	uri archiver.URI,
	request *archiver.ArchiveHistoryRequest,
	histories []*historypb.History,
	logger log.Logger,
	featureCatalog *archiver.ArchiveFeatureCatalog,
) error {
	enc := codec.NewJSONPBEncoder()
	jsonBytes, err := enc.EncodeHistories(histories)
	if err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason("encode_history"), tag.Error(err))
		if featureCatalog.NonRetryableError != nil {
			return featureCatalog.NonRetryableError()
		}
		return err
	}

	compressed, err := GzipCompress(jsonBytes)
	if err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason("compress_history"), tag.Error(err))
		if featureCatalog.NonRetryableError != nil {
			return featureCatalog.NonRetryableError()
		}
		return err
	}

	key := HistoryKey(uri.Path(), request.NamespaceID, request.WorkflowID, request.RunID, request.CloseFailoverVersion)
	_, err = h.s3cli.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(uri.Hostname()),
		Key:           aws.String(key),
		Body:          bytes.NewReader(compressed),
		ContentLength: aws.Int64(int64(len(compressed))),
	})
	if err != nil {
		logger.Error(archiver.ArchiveTransientErrorMsg,
			tag.ArchivalArchiveFailReason("s3_put"), tag.Error(err))
		return err
	}

	logger.Info("minio history archiver: archived",
		tag.NewStringTag("key", key),
		tag.NewInt64("compressed_bytes", int64(len(compressed))),
	)
	return nil
}

// Get downloads an archived history, decompresses it, decodes it, and applies
// pagination according to request.PageSize and request.NextPageToken.
func (h *MinioHistoryArchiver) Get(
	ctx context.Context,
	uri archiver.URI,
	request *archiver.GetHistoryRequest,
) (*archiver.GetHistoryResponse, error) {
	if err := h.ValidateURI(uri); err != nil {
		return nil, serviceerror.NewInvalidArgument(archiver.ErrInvalidURI.Error())
	}
	if err := archiver.ValidateGetRequest(request); err != nil {
		return nil, serviceerror.NewInvalidArgument(archiver.ErrInvalidGetHistoryRequest.Error())
	}

	var closeFailoverVersion int64
	if request.CloseFailoverVersion != nil {
		closeFailoverVersion = *request.CloseFailoverVersion
	}

	key := HistoryKey(uri.Path(), request.NamespaceID, request.WorkflowID, request.RunID, closeFailoverVersion)

	result, err := h.s3cli.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(uri.Hostname()),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, serviceerror.NewNotFound(archiver.ErrHistoryNotExist.Error())
		}
		return nil, serviceerror.NewUnavailable(err.Error())
	}
	defer result.Body.Close()

	compressed, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, serviceerror.NewUnavailable(fmt.Sprintf("minio history archiver: read body: %v", err))
	}

	jsonBytes, err := GzipDecompress(compressed)
	if err != nil {
		return nil, serviceerror.NewInternal(fmt.Sprintf("minio history archiver: decompress: %v", err))
	}

	enc := codec.NewJSONPBEncoder()
	histories, err := enc.DecodeHistories(jsonBytes)
	if err != nil {
		return nil, serviceerror.NewInternal(fmt.Sprintf("minio history archiver: decode: %v", err))
	}

	// Paginate over the decoded history batches.
	return paginateHistories(histories, request), nil
}

// --- helpers ---

// buildHistoryIterator constructs a HistoryIterator, restoring heartbeat state
// from a previous attempt if available.
func buildHistoryIterator(
	ctx context.Context,
	request *archiver.ArchiveHistoryRequest,
	executionManager persistence.ExecutionManager,
	featureCatalog *archiver.ArchiveFeatureCatalog,
) archiver.HistoryIterator {
	const targetBlobSize = 2 * 1024 * 1024 // 2 MiB per blob

	if featureCatalog.ProgressManager != nil && featureCatalog.ProgressManager.HasProgress(ctx) {
		var state []byte
		if err := featureCatalog.ProgressManager.LoadProgress(ctx, &state); err == nil && len(state) > 0 {
			iter, err := archiver.NewHistoryIteratorFromState(request, executionManager, targetBlobSize, state)
			if err == nil {
				return iter
			}
		}
	}
	return archiver.NewHistoryIterator(request, executionManager, targetBlobSize)
}

// saveIteratorState heartbeats the current iterator position so a retry can resume.
func saveIteratorState(
	ctx context.Context,
	featureCatalog *archiver.ArchiveFeatureCatalog,
	iter archiver.HistoryIterator,
) {
	if featureCatalog.ProgressManager == nil {
		return
	}
	state, err := iter.GetState()
	if err != nil {
		return
	}
	_ = featureCatalog.ProgressManager.RecordProgress(ctx, state)
}

// terminalStatus returns the workflow execution status derived from the last event
// in the last history blob. ok=false means the last event type is not recognised
// as a terminal event (should not happen for a well-formed history).
func terminalStatus(blob *archiverspb.HistoryBlob) (enumspb.WorkflowExecutionStatus, bool) {
	if len(blob.Body) == 0 {
		return 0, false
	}
	lastHistory := blob.Body[len(blob.Body)-1]
	if len(lastHistory.Events) == 0 {
		return 0, false
	}
	lastEvent := lastHistory.Events[len(lastHistory.Events)-1]
	st, ok := eventTypeToStatus[lastEvent.GetEventType()]
	return st, ok
}

// historyMutated returns true if the history contents do not match the expected
// failover version and event ID derived from the archive request.
func historyMutated(
	request *archiver.ArchiveHistoryRequest,
	historyBatches []*historypb.History,
	isLast bool,
) bool {
	if len(historyBatches) == 0 {
		return false
	}
	lastBatch := historyBatches[len(historyBatches)-1].Events
	if len(lastBatch) == 0 {
		return false
	}
	lastEvent := lastBatch[len(lastBatch)-1]
	lastFailoverVersion := lastEvent.GetVersion()
	if lastFailoverVersion > request.CloseFailoverVersion {
		return true
	}
	if !isLast {
		return false
	}
	lastEventID := lastEvent.GetEventId()
	return lastFailoverVersion != request.CloseFailoverVersion || lastEventID+1 != request.NextEventID
}

// paginateHistories applies PageSize and NextPageToken pagination to a slice of
// decoded history batches. NextPageToken is a single byte encoding the start index.
func paginateHistories(histories []*historypb.History, request *archiver.GetHistoryRequest) *archiver.GetHistoryResponse {
	startIdx := 0
	if len(request.NextPageToken) == 1 {
		startIdx = int(request.NextPageToken[0])
	}
	if startIdx >= len(histories) {
		return &archiver.GetHistoryResponse{}
	}

	endIdx := startIdx + request.PageSize
	var nextToken []byte
	if endIdx < len(histories) {
		nextToken = []byte{byte(endIdx)}
	} else {
		endIdx = len(histories)
	}

	return &archiver.GetHistoryResponse{
		HistoryBatches: histories[startIdx:endIdx],
		NextPageToken:  nextToken,
	}
}
