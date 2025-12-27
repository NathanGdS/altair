package shared

import (
	"log"
	"runtime"
	"time"
)

const (
	PurgeInterval            = 15 * time.Minute
	RemoveEmptyFilesInterval = 7 * time.Second
	ConsumerRunningInterval  = 5 * time.Second
)

var ConsumerWorkingPool = getWorkingPool()

func getWorkingPool() int {
	wkPool := runtime.NumCPU() * 2
	log.Printf("[CONFIG] - Current working pool: %d", wkPool)
	return wkPool
}
