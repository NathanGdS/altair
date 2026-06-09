package shared

import (
	"runtime"
	"time"
)

const (
	PurgeInterval            = 15 * time.Minute
	RemoveEmptyFilesInterval = 5 * time.Minute
	ConsumerRunningInterval  = 5 * time.Second
	RemoveMakedFilesInterval = 15 * time.Minute
)

var ConsumerWorkingPool = getWorkingPool()

func getWorkingPool() int {
	return runtime.NumCPU() * 2
}
