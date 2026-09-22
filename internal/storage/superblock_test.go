package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestFile(tb testing.TB) *os.File {
	tb.Helper()

	f, err := os.Create(filepath.Join(tb.TempDir(), "test.vol"))
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = f.Close() })

	return f
}

func TestSuperblock_RoundTrip(t *testing.T) {
	f := setupTestFile(t)

	want := NewSuperblock(42)
	require.NoError(t, WriteSuperblock(f, want))

	info, err := f.Stat()
	require.NoError(t, err)
	assert.Equal(t, int64(SuperblockSize), info.Size())

	got, err := ReadSuperblock(f)
	require.NoError(t, err)

	assert.Equal(t, want, got)
	assert.False(t, got.Sealed())
}

func TestSuperblock_Seal(t *testing.T) {
	f := setupTestFile(t)

	require.NoError(t, WriteSuperblock(f, NewSuperblock(1)))
	require.NoError(t, SealSuperblock(f, 1700000000))

	got, err := ReadSuperblock(f)
	require.NoError(t, err)

	assert.Equal(t, int64(1700000000), got.SealedAtUnixNano)
	assert.True(t, got.Sealed())
}

func TestSuperblock_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(buf []byte)
		wantErr error
	}{
		{
			name:    "bad magic",
			corrupt: func(buf []byte) { binary.BigEndian.PutUint32(buf[0:4], 0xDEADBEEF) },
			wantErr: ErrInvalidSuperblock,
		},
		{
			name:    "unsupported version",
			corrupt: func(buf []byte) { binary.BigEndian.PutUint32(buf[4:8], FormatVersion+1) },
			wantErr: ErrUnsupportedFormatVersion,
		},
		{
			name:    "bad needle start offset",
			corrupt: func(buf []byte) { binary.BigEndian.PutUint32(buf[32:36], 4096) },
			wantErr: ErrInvalidSuperblock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := setupTestFile(t)

			buf := NewSuperblock(1).Bytes()
			tt.corrupt(buf)
			_, err := f.WriteAt(buf, 0)
			require.NoError(t, err)

			_, err = ReadSuperblock(f)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestSuperblock_Truncated(t *testing.T) {
	f := setupTestFile(t)

	require.NoError(t, WriteSuperblock(f, NewSuperblock(1)))
	require.NoError(t, f.Truncate(SuperblockSize-1))

	_, err := ReadSuperblock(f)
	assert.Error(t, err)
}
