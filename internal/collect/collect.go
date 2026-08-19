// Package collect defines the contract every data source implements.
package collect

import "context"

// A Collector owns its own cadence: some tick, some stream from an event
// source. Run blocks until ctx is cancelled and should recover from transient
// failures rather than returning on the first error.
type Collector interface {
	Name() string
	Run(ctx context.Context) error
}
