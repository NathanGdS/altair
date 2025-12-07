package shared

import (
	"log"
	"runtime"
	"time"
)

const (
	PurgeInterval            = 10 * time.Minute
	RemoveEmptyFilesInterval = 15 * time.Minute
	ConsumerRunningInterval  = 1 * time.Second
)

var ConsumerWorkingPool = getWorkingPool()

func getWorkingPool() int {
	wkPool := runtime.NumCPU() * 2
	log.Printf("[CONFIG] - Current working pool: %d", wkPool)
	return wkPool
}
