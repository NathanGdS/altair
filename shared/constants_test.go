package shared

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetWorkingPool(t *testing.T) {
	t.Run("sizes the pool from GOMAXPROCS so container CPU limits are honored", func(t *testing.T) {
		// Arrange
		previous := runtime.GOMAXPROCS(3)
		defer runtime.GOMAXPROCS(previous)

		// Act
		pool := getWorkingPool()

		// Assert
		assert.Equal(t, 6, pool)
	})
}
