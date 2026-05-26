package archiver

import (
	"fmt"
	"strings"
	"time"
)

// HistoryKey returns the S3 object key for an archived workflow history.
//
// Format: {uriPath}/{namespaceID}/{workflowID}/{runID}_{closeFailoverVersion}.history.gz
//
// No date partition is used because GetHistoryRequest only carries namespaceID,
// workflowID, runID, and closeFailoverVersion — there is no close timestamp available
// on retrieval. The key is therefore always fully reconstructable from those four fields.
func HistoryKey(uriPath, namespaceID, workflowID, runID string, closeFailoverVersion int64) string {
	prefix := strings.TrimPrefix(uriPath, "/")
	return fmt.Sprintf("%s/%s/%s/%s_%d.history.gz",
		prefix, namespaceID, workflowID, runID, closeFailoverVersion)
}

// VisibilityKey returns the S3 object key for an archived visibility record.
//
// Format: {uriPath}/{namespaceID}/{YYYY}/{MM}/{DD}/{closeTimeUnixNano}_{shortRunID}.visibility.gz
//
// Date-partitioned so listing by prefix gives time-ordered results and MinIO
// lifecycle rules can target specific date ranges with a simple prefix pattern.
// The close timestamp comes from VisibilityRecord.CloseTime which is always set.
func VisibilityKey(uriPath, namespaceID, runID string, closeTime time.Time) string {
	prefix := strings.TrimPrefix(uriPath, "/")
	t := closeTime.UTC()
	shortID := runID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("%s/%s/%04d/%02d/%02d/%d_%s.visibility.gz",
		prefix, namespaceID, t.Year(), int(t.Month()), t.Day(), t.UnixNano(), shortID)
}

// VisibilityPrefix returns the S3 key prefix used to list all visibility records
// for a namespace, optionally narrowed to a specific date.
func VisibilityPrefix(uriPath, namespaceID string) string {
	prefix := strings.TrimPrefix(uriPath, "/")
	return fmt.Sprintf("%s/%s/", prefix, namespaceID)
}
