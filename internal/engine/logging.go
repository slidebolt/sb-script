package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	logging "github.com/slidebolt/sb-logging-sdk"
)

var engineLogSequence uint64

func (e *Engine) appendLog(kind, level, message, traceID string, data map[string]any) {
	if e == nil || e.logger == nil {
		return
	}
	event := logging.Event{
		ID:      fmt.Sprintf("sb-script-%d", atomic.AddUint64(&engineLogSequence, 1)),
		TS:      time.Now().UTC(),
		Source:  "sb-script",
		Kind:    kind,
		Level:   level,
		Message: message,
		TraceID: traceID,
		Data:    data,
	}
	_ = e.logger.Append(context.Background(), event)
}

func (rt *activationRuntime) log(kind, level, message, traceID string, data map[string]any) {
	if rt == nil || rt.engine == nil {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["name"] = rt.name
	data["query_ref"] = rt.queryRef
	data["instance_hash"] = rt.instanceHash()
	rt.engine.appendLog(kind, level, message, traceID, data)
}
