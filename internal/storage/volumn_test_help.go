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

func runScan(t *testing.T, v *Volume) (end int64, calls int) {
	t.Helper()
	end, err := v.scan(func(int64, NeedleHeader, []byte) error {
		calls++
		return nil
	})
	require.NoError(t, err)
	return end, calls
}

func poke(t *testing.T, f *os.File, off int64, b ...byte) {
	t.Helper()
	_, err := f.WriteAt(b, off)
	require.NoError(t, err)
}

func setupThree(t *testing.T) (*Volume, *os.File, int64, int64) {
	t.Helper()
	v, f := SetupTestVolume(t)
	require.NoError(t, v.Write(NewNeedle(1, 0, 1, []byte("first"))))
	second := v.writeOffset
	require.NoError(t, v.Write(NewNeedle(2, 0, 1, []byte("second"))))
	third := v.writeOffset
	require.NoError(t, v.Write(NewNeedle(3, 0, 1, []byte("third"))))
	return v, f, second, third
}

func reopen(t *testing.T, f *os.File) *Volume {
	t.Helper()
	v, err := NewVolume(f, 1)
	require.NoError(t, err)
	require.NoError(t, v.Reload())
	return v
}
