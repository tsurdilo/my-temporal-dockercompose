package archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	archiverspb "go.temporal.io/server/api/archiver/v1"
	"go.temporal.io/server/common/archiver"
	"go.temporal.io/server/common/codec"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
	"go.temporal.io/server/common/metrics"
	"go.temporal.io/server/common/searchattribute"
)

// executionStatusFilterRe matches:  ExecutionStatus = 'Failed'  (case-insensitive, single or double quotes)
var executionStatusFilterRe = regexp.MustCompile(`(?i)ExecutionStatus\s*=\s*['"](\w+)['"]`)

// parseStatusFilter extracts an ExecutionStatus = 'X' clause from a query string.
//
// Returns (status, true, nil)  if a recognised filter is present.
// Returns (0,      false, nil) if the query is empty or contains no ExecutionStatus clause.
// Returns an error if the status name is not one of the known values.
func parseStatusFilter(query string) (enumspb.WorkflowExecutionStatus, bool, error) {
	if strings.TrimSpace(query) == "" {
		return 0, false, nil
	}
	m := executionStatusFilterRe.FindStringSubmatch(query)
	if m == nil {
		return 0, false, nil
	}
	status, ok := statusNameToEnum[m[1]]
	if !ok {
		return 0, false, fmt.Errorf(
			"unknown ExecutionStatus %q; valid values: Completed, Failed, TimedOut, Canceled, Terminated, ContinuedAsNew",
			m[1],
		)
	}
	return status, true, nil
}

// MinioVisibilityArchiver implements archiver.VisibilityArchiver backed by MinIO.
type MinioVisibilityArchiver struct {
	logger         log.Logger
	metricsHandler metrics.Handler
	s3cli          *s3.Client
	cfg            *Config
}

func newVisibilityArchiver(
	logger log.Logger,
	metricsHandler metrics.Handler,
	cfg *Config,
) (*MinioVisibilityArchiver, error) {
	s3cli, err := NewS3Client(cfg)
	if err != nil {
		return nil, fmt.Errorf("minio visibility archiver: build S3 client: %w", err)
	}
	return &MinioVisibilityArchiver{
		logger:         logger,
		metricsHandler: metricsHandler,
		s3cli:          s3cli,
		cfg:            cfg,
	}, nil
}

// ValidateURI verifies that the URI scheme is "minio" and has a non-empty hostname (bucket name).
func (v *MinioVisibilityArchiver) ValidateURI(uri archiver.URI) error {
	if uri.Scheme() != Scheme {
		return archiver.ErrURISchemeMismatch
	}
	if uri.Hostname() == "" {
		return fmt.Errorf("%w: minio URI must include a bucket name as the hostname", archiver.ErrInvalidURI)
	}
	return nil
}

// Archive checks the workflow's completion status against the configured allowlist and —
// if allowed — serialises, gzip-compresses, and uploads the visibility record to MinIO.
// If the status is not in the allowlist the method returns nil immediately (silent skip).
func (v *MinioVisibilityArchiver) Archive(
	ctx context.Context,
	uri archiver.URI,
	request *archiverspb.VisibilityRecord,
	opts ...archiver.ArchiveOption,
) error {
	featureCatalog := archiver.GetFeatureCatalog(opts...)
	logger := log.With(v.logger,
		tag.WorkflowNamespaceID(request.GetNamespaceId()),
		tag.WorkflowID(request.GetWorkflowId()),
		tag.WorkflowRunID(request.GetRunId()),
	)

	if err := v.ValidateURI(uri); err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason(archiver.ErrReasonInvalidURI), tag.Error(err))
		return err
	}
	if err := archiver.ValidateVisibilityArchivalRequest(request); err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason(archiver.ErrReasonInvalidArchiveRequest), tag.Error(err))
		return err
	}

	// Status filter — cheapest check first, before any I/O.
	if !v.cfg.StatusAllowed(request.GetStatus()) {
		logger.Info("minio visibility archiver: skipping — status not in allowedStatuses",
			tag.NewStringTag("status", request.GetStatus().String()))
		return nil
	}

	enc := codec.NewJSONPBEncoder()
	jsonBytes, err := enc.Encode(request)
	if err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason("encode_visibility"), tag.Error(err))
		if featureCatalog.NonRetryableError != nil {
			return featureCatalog.NonRetryableError()
		}
		return err
	}

	compressed, err := GzipCompress(jsonBytes)
	if err != nil {
		logger.Error(archiver.ArchiveNonRetryableErrorMsg,
			tag.ArchivalArchiveFailReason("compress_visibility"), tag.Error(err))
		if featureCatalog.NonRetryableError != nil {
			return featureCatalog.NonRetryableError()
		}
		return err
	}

	closeTime := request.GetCloseTime().AsTime()
	key := VisibilityKey(uri.Path(), request.GetNamespaceId(), request.GetRunId(), closeTime)

	_, err = v.s3cli.PutObject(ctx, &s3.PutObjectInput{
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

	logger.Info("minio visibility archiver: archived",
		tag.NewStringTag("key", key),
		tag.NewStringTag("status", request.GetStatus().String()),
	)
	return nil
}

// Query lists archived visibility records for a namespace, optionally filtered by
// ExecutionStatus.
//
// Supported query syntax:
//
//	ExecutionStatus = 'Failed'
//	ExecutionStatus = 'Terminated'
//	ExecutionStatus = 'Completed'
//	ExecutionStatus = 'TimedOut'
//	ExecutionStatus = 'Canceled'
//	ExecutionStatus = 'ContinuedAsNew'
//
// The filter is case-insensitive and accepts single or double quotes. Any other
// query clauses are silently ignored — see Known limitations in README.md.
//
// The method loops over S3 batches until it has collected PageSize matching records
// or has exhausted all objects in the namespace prefix.
func (v *MinioVisibilityArchiver) Query(
	ctx context.Context,
	uri archiver.URI,
	request *archiver.QueryVisibilityRequest,
	saTypeMap searchattribute.NameTypeMap,
) (*archiver.QueryVisibilityResponse, error) {
	if err := v.ValidateURI(uri); err != nil {
		return nil, serviceerror.NewInvalidArgument(archiver.ErrInvalidURI.Error())
	}
	if err := archiver.ValidateQueryRequest(request); err != nil {
		return nil, serviceerror.NewInvalidArgument(archiver.ErrInvalidQueryVisibilityRequest.Error())
	}

	statusFilter, hasStatusFilter, err := parseStatusFilter(request.Query)
	if err != nil {
		return nil, serviceerror.NewInvalidArgument(err.Error())
	}

	prefix := VisibilityPrefix(uri.Path(), request.NamespaceID)

	var continuationToken *string
	if len(request.NextPageToken) > 0 {
		t := string(request.NextPageToken)
		continuationToken = &t
	}

	// Over-fetch per S3 batch to compensate for records filtered out by status.
	// Minimum 50; scaled by 5× PageSize when filtering is active.
	batchSize := int32(request.PageSize * 2)
	if hasStatusFilter {
		batchSize = int32(request.PageSize * 5)
	}
	if batchSize < 50 {
		batchSize = 50
	}

	response := &archiver.QueryVisibilityResponse{}
	var lastListOut *s3.ListObjectsV2Output

	for len(response.Executions) < request.PageSize {
		listOut, err := v.s3cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(uri.Hostname()),
			Prefix:            aws.String(prefix),
			MaxKeys:           aws.Int32(batchSize),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			var noSuchBucket *types.NoSuchBucket
			if errors.As(err, &noSuchBucket) {
				return nil, serviceerror.NewInvalidArgument(
					fmt.Sprintf("minio visibility archiver: bucket %q not found", uri.Hostname()))
			}
			return nil, serviceerror.NewUnavailable(err.Error())
		}
		lastListOut = listOut

		for _, item := range listOut.Contents {
			if len(response.Executions) >= request.PageSize {
				break
			}

			record, err := v.downloadRecord(ctx, uri.Hostname(), aws.ToString(item.Key))
			if err != nil {
				v.logger.Warn("minio visibility archiver: failed to download record",
					tag.NewStringTag("key", aws.ToString(item.Key)), tag.Error(err))
				continue
			}

			execInfo, err := convertToExecutionInfo(record, saTypeMap)
			if err != nil {
				v.logger.Warn("minio visibility archiver: failed to convert record",
					tag.NewStringTag("key", aws.ToString(item.Key)), tag.Error(err))
				continue
			}

			if hasStatusFilter && execInfo.Status != statusFilter {
				continue
			}

			response.Executions = append(response.Executions, execInfo)
		}

		if !aws.ToBool(listOut.IsTruncated) {
			break // exhausted all objects in the namespace
		}
		continuationToken = listOut.NextContinuationToken
	}

	// If the last S3 batch was truncated there are more objects. Carry the
	// continuation token forward so the next call resumes from here.
	if lastListOut != nil && aws.ToBool(lastListOut.IsTruncated) {
		response.NextPageToken = []byte(aws.ToString(lastListOut.NextContinuationToken))
	}

	return response, nil
}

// --- helpers ---

func (v *MinioVisibilityArchiver) downloadRecord(ctx context.Context, bucket, key string) (*archiverspb.VisibilityRecord, error) {
	result, err := v.s3cli.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer result.Body.Close()

	compressed, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	jsonBytes, err := GzipDecompress(compressed)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}

	record := &archiverspb.VisibilityRecord{}
	enc := codec.NewJSONPBEncoder()
	if err := enc.Decode(jsonBytes, record); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return record, nil
}

// convertToExecutionInfo converts a VisibilityRecord proto to the WorkflowExecutionInfo
// wire type expected by the Query response.
func convertToExecutionInfo(record *archiverspb.VisibilityRecord, saTypeMap searchattribute.NameTypeMap) (*workflowpb.WorkflowExecutionInfo, error) {
	searchAttributes, err := searchattribute.Parse(record.SearchAttributes, &saTypeMap)
	if err != nil {
		return nil, err
	}
	return &workflowpb.WorkflowExecutionInfo{
		Execution: &commonpb.WorkflowExecution{
			WorkflowId: record.GetWorkflowId(),
			RunId:      record.GetRunId(),
		},
		Type: &commonpb.WorkflowType{
			Name: record.WorkflowTypeName,
		},
		StartTime:         record.StartTime,
		ExecutionTime:     record.ExecutionTime,
		CloseTime:         record.CloseTime,
		ExecutionDuration: record.ExecutionDuration,
		Status:            record.Status,
		HistoryLength:     record.HistoryLength,
		Memo:              record.Memo,
		SearchAttributes:  searchAttributes,
	}, nil
}
