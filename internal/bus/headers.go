package bus

import (
	"fmt"
	"strconv"

	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	HeaderEventID           = "event_id"
	HeaderEventType         = "event_type"
	HeaderTraceID           = "trace_id"
	HeaderSchemaVersion     = "schema_version"
	HeaderAttempt           = "x-attempt"
	HeaderNotBefore         = "x-not-before"
	HeaderOriginalTopic     = "x-original-topic"
	HeaderOriginalPartition = "x-original-partition"
	HeaderOriginalOffset    = "x-original-offset"
	HeaderLastError         = "x-last-error"
	HeaderError             = "x-error"
	HeaderErrorKind         = "x-error-kind"
	HeaderAttempts          = "x-attempts"
	HeaderFailedAt          = "x-failed-at"
	HeaderConsumer          = "x-consumer"
	HeaderReplayedBy        = "x-replayed-by"
	HeaderReplayedAt        = "x-replayed-at"
)

// HeaderString extracts a string value for the given header key.
func HeaderString(rec *kgo.Record, key string) string {
	for _, h := range rec.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// HeaderInt extracts an integer value from a header key.
func HeaderInt(rec *kgo.Record, key string) int {
	val := HeaderString(rec, key)
	if val == "" {
		return 0
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return n
}

// HeaderInt64 extracts an int64 value from a header key.
func HeaderInt64(rec *kgo.Record, key string) int64 {
	val := HeaderString(rec, key)
	if val == "" {
		return 0
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// CopyHeaders returns a copy of existing headers with overrides applied.
func CopyHeaders(rec *kgo.Record, overrides map[string]string) []kgo.RecordHeader {
	seen := make(map[string]bool)
	var result []kgo.RecordHeader

	for _, h := range rec.Headers {
		if val, ok := overrides[h.Key]; ok {
			result = append(result, kgo.RecordHeader{Key: h.Key, Value: []byte(val)})
			seen[h.Key] = true
		} else {
			result = append(result, kgo.RecordHeader{Key: h.Key, Value: h.Value})
		}
	}

	for k, v := range overrides {
		if !seen[k] {
			result = append(result, kgo.RecordHeader{Key: k, Value: []byte(v)})
		}
	}

	return result
}

// OriginalTopic returns the original topic where the message was first consumed.
func OriginalTopic(rec *kgo.Record) string {
	if orig := HeaderString(rec, HeaderOriginalTopic); orig != "" {
		return orig
	}
	return rec.Topic
}

// OriginalPartition returns original partition string.
func OriginalPartition(rec *kgo.Record) string {
	if orig := HeaderString(rec, HeaderOriginalPartition); orig != "" {
		return orig
	}
	return fmt.Sprintf("%d", rec.Partition)
}

// OriginalOffset returns original offset string.
func OriginalOffset(rec *kgo.Record) string {
	if orig := HeaderString(rec, HeaderOriginalOffset); orig != "" {
		return orig
	}
	return fmt.Sprintf("%d", rec.Offset)
}

// Truncate safely cuts a string to maxLen characters.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
