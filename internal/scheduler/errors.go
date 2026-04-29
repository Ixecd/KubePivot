// internal/scheduler/errors.go
package scheduler

import "errors"

var (
	errNoNodes         = errors.New("scheduler: no nodes available")
	ErrCapacityExceeded = errors.New("scheduler: capacity exceeded, not all pods could be placed")
)