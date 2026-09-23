package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func SetupTestVolume(tb testing.TB) (*Volume, *os.File) {
	tb.Helper()
	vPath := filepath.Join(tb.TempDir(), "test.vol")

	f, err := os.Create(vPath)
	require.NoError(tb, err)

	tb.Cleanup(func() { _ = f.Close() })

	v, err := NewVolume(f, 1)
	require.NoError(tb, err)

	return v, f
}
