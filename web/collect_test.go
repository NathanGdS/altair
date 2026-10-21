package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	assert.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func TestOriginFromReadyFile(t *testing.T) {
	t.Run("extracts the origin, including origins that contain dashes", func(t *testing.T) {
		// Act
		simple, okSimple := originFromReadyFile("orders-2026-10-20.json")
		dashed, okDashed := originFromReadyFile("stress-test-2026-10-20.json")

		// Assert
		assert.True(t, okSimple)
		assert.Equal(t, "orders", simple)
		assert.True(t, okDashed)
		assert.Equal(t, "stress-test", dashed)
	})

	t.Run("rejects names that are not ready files", func(t *testing.T) {
		for _, name := range []string{"orders.json", "20261020_14.json", "orders-2026-10-20.json.reading.1", "-2026-10-20.json"} {
			// Act
			_, ok := originFromReadyFile(name)

			// Assert
			assert.False(t, ok, name)
		}
	})
}

func TestCountLines(t *testing.T) {
	t.Run("counts a last line without a trailing newline", func(t *testing.T) {
		// Arrange
		path := filepath.Join(t.TempDir(), "ready.json")
		writeFile(t, path, "{\"a\":1}\n{\"a\":2}\n{\"a\":3}")

		// Act
		lines, err := countLines(path)

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, 3, lines)
	})

	t.Run("does not double count a trailing newline", func(t *testing.T) {
		// Arrange
		path := filepath.Join(t.TempDir(), "processed.json")
		writeFile(t, path, "{\"a\":1}\n{\"a\":2}\n")

		// Act
		lines, err := countLines(path)

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, 2, lines)
	})

	t.Run("counts zero for an empty file", func(t *testing.T) {
		// Arrange
		path := filepath.Join(t.TempDir(), "empty.json")
		writeFile(t, path, "")

		// Act
		lines, err := countLines(path)

		// Assert
		assert.NoError(t, err)
		assert.Equal(t, 0, lines)
	})
}

func TestLinesByOrigin(t *testing.T) {
	t.Run("sums every day file of the same origin and ignores unrelated files", func(t *testing.T) {
		// Arrange
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "orders-2026-10-19.json"), "1\n2")
		writeFile(t, filepath.Join(dir, "orders-2026-10-20.json"), "3")
		writeFile(t, filepath.Join(dir, "payments-2026-10-20.json"), "1\n2\n3")
		writeFile(t, filepath.Join(dir, "notes.txt"), "1\n2\n3\n4")

		// Act
		byOrigin := linesByOrigin(dir)

		// Assert
		assert.Equal(t, map[string]int{"orders": 3, "payments": 3}, byOrigin)
	})

	t.Run("returns an empty map when the directory does not exist", func(t *testing.T) {
		// Act
		byOrigin := linesByOrigin(filepath.Join(t.TempDir(), "missing"))

		// Assert
		assert.Empty(t, byOrigin)
	})
}

func TestSumLines(t *testing.T) {
	t.Run("includes in-flight reading files", func(t *testing.T) {
		// Arrange
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "orders-2026-10-20.json"), "a\nb\n")
		writeFile(t, filepath.Join(dir, "orders-2026-10-20.json.reading.123"), "c\n")
		writeFile(t, filepath.Join(dir, "README"), "x\n")

		// Act
		total := sumLines(dir)

		// Assert
		assert.Equal(t, 3, total)
	})
}
