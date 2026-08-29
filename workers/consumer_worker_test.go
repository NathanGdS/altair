package workers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func withTempWorkDir(t *testing.T) {
	t.Helper()
	originalDir, err := os.Getwd()
	assert.NoError(t, err)

	tempDir := t.TempDir()
	assert.NoError(t, os.Chdir(tempDir))

	t.Cleanup(func() {
		assert.NoError(t, os.Chdir(originalDir))
	})
}

func TestProcessSingleMessage(t *testing.T) {
	t.Run("enqueues one delivery line per active consumer on the message origin", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDirectories()
		initDeliveryDirectories()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, shared.Consumers.Close()) })

		_, err = shared.Consumers.Register("orders", "https://example.com/hook-a")
		assert.NoError(t, err)
		_, err = shared.Consumers.Register("orders", "https://example.com/hook-b")
		assert.NoError(t, err)

		line := `{"origin":"orders","id":"msg-1","data":{"key":"value"}}`

		// Act
		processSingleMessage(message{FileName: "orders-2026-08-16.json", Line: line})

		// Assert
		files, err := os.ReadDir(DeliveryPendingDir)
		assert.NoError(t, err)
		assert.Len(t, files, 1)

		content, err := os.ReadFile(filepath.Join(DeliveryPendingDir, files[0].Name()))
		assert.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		assert.Len(t, lines, 2)
	})

	t.Run("does not create a pending file when the origin has no consumers", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDirectories()
		initDeliveryDirectories()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, shared.Consumers.Close()) })

		line := `{"origin":"no-subscribers","id":"msg-2","data":{}}`

		// Act
		processSingleMessage(message{FileName: "no-subscribers-2026-08-16.json", Line: line})

		// Assert
		files, err := os.ReadDir(DeliveryPendingDir)
		assert.NoError(t, err)
		assert.Empty(t, files)
	})
}
