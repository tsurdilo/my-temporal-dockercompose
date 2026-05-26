package archiver

import (
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
)

const Scheme = "minio"

// Config holds the parsed configuration for the MinIO archiver, sourced from
// the customStores YAML block.
type Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	AllowedStatuses map[enumspb.WorkflowExecutionStatus]struct{}
}

// statusNameToEnum maps the YAML-friendly status names to protobuf enum values.
var statusNameToEnum = map[string]enumspb.WorkflowExecutionStatus{
	"Completed":     enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
	"Failed":        enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
	"TimedOut":      enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
	"Canceled":      enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED,
	"Terminated":    enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED,
	"ContinuedAsNew": enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW,
}

// ParseConfig parses a map[string]any (delivered by ArchiverProvider from the
// customStores YAML) into a typed Config struct.
func ParseConfig(configs map[string]any) (*Config, error) {
	if configs == nil {
		return nil, fmt.Errorf("minio archiver: nil config map")
	}

	getString := func(key string) (string, error) {
		v, ok := configs[key]
		if !ok {
			return "", fmt.Errorf("minio archiver: missing required config key %q", key)
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("minio archiver: config key %q must be a string, got %T", key, v)
		}
		return s, nil
	}

	endpoint, err := getString("endpoint")
	if err != nil {
		return nil, err
	}
	region, err := getString("region")
	if err != nil {
		return nil, err
	}
	accessKeyID, err := getString("accessKeyID")
	if err != nil {
		return nil, err
	}
	secretAccessKey, err := getString("secretAccessKey")
	if err != nil {
		return nil, err
	}

	var allowedStatuses map[enumspb.WorkflowExecutionStatus]struct{}
	if raw, ok := configs["allowedStatuses"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("minio archiver: allowedStatuses must be a list of strings")
		}
		if len(list) > 0 {
			allowedStatuses = make(map[enumspb.WorkflowExecutionStatus]struct{}, len(list))
			for _, item := range list {
				name, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("minio archiver: each allowedStatuses entry must be a string, got %T", item)
				}
				st, ok := statusNameToEnum[name]
				if !ok {
					return nil, fmt.Errorf("minio archiver: unknown status %q in allowedStatuses; valid values: Completed, Failed, TimedOut, Canceled, Terminated, ContinuedAsNew", name)
				}
				allowedStatuses[st] = struct{}{}
			}
		}
	}

	return &Config{
		Endpoint:        endpoint,
		Region:          region,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		AllowedStatuses: allowedStatuses,
	}, nil
}

// StatusAllowed returns true if the given workflow execution status should be archived.
// If AllowedStatuses is nil or empty, all statuses are archived.
func (c *Config) StatusAllowed(status enumspb.WorkflowExecutionStatus) bool {
	if len(c.AllowedStatuses) == 0 {
		return true
	}
	_, ok := c.AllowedStatuses[status]
	return ok
}
