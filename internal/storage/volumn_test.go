package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	volume, _ := setupTestVolume(t)

	needle := NewNeedle(1, 100, 12345, make([]byte, 4096))

	assert.NoError(t, volume.Write(needle))

	key := KeyPair{Key: 1, AltKey: 100}
	metas := volume.index[key]

	assert.Equal(t, int64(0), metas.Offset, "First offset should be 0")
	assert.Equal(t, uint32(4096), metas.Size, "Size should be data size only")

	// header + data + footer + padding
	// (29+ 4096 + 8 + n) % 8 = 0;total = 4136
	expectedTotalSize := int64(4136)
	assert.Equal(t, expectedTotalSize, volume.writeOffset, "Write offset calculation incorrect")

	assert.NoError(t, volume.Write(needle))
	metas = volume.index[key]

	assert.Equal(t, expectedTotalSize, metas.Offset, "Second offset should start after first needle")

	assert.Equal(t, expectedTotalSize*2, volume.writeOffset, "Final write offset incorrect")
}

func setupTestVolume(tb testing.TB) (*Volume, *os.File) {
	tb.Helper()
	vPath := filepath.Join(tb.TempDir(), "test.vol")

	f, err := os.Create(vPath)
	require.NoError(tb, err)

	tb.Cleanup(func() { _ = f.Close() })

	return NewVolume(f), f
}

func TestVolume_Read(t *testing.T) {
	payload := []byte("hello world data")
	keyVal := uint64(100)
	cookieVal := uint64(9999)
	altKeyVal := uint32(50)

	keyPair := KeyPair{Key: keyVal, AltKey: altKeyVal}

	needle := NewNeedle(keyVal, altKeyVal, cookieVal, payload)

	t.Run("Success_HappyPath", func(t *testing.T) {
		v, _ := setupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		gotBytes, err := v.Read(keyPair, cookieVal)
		assert.NoError(t, err)
		assert.Equal(t, gotBytes, payload)
	})

	t.Run("Error_KeyNotFound", func(t *testing.T) {
		v, _ := setupTestVolume(t)

		wrongKey := KeyPair{Key: 99999, AltKey: 0}
		_, err := v.Read(wrongKey, cookieVal)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Error_InvalidCookie", func(t *testing.T) {
		v, _ := setupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		wrongCookie := uint64(1111)
		_, err = v.Read(keyPair, wrongCookie)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrCookie)
	})

	t.Run("Error_DataDeleted", func(t *testing.T) {
		v, f := setupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		meta := v.index[keyPair]

		flagOffset := meta.Offset + 24

		_, err = f.WriteAt([]byte{1}, flagOffset)
		require.NoError(t, err)

		_, err = v.Read(keyPair, cookieVal)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDataDeleted)
	})

	t.Run("Error_MagicHeaderMismatch", func(t *testing.T) {
		v, f := setupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		meta := v.index[keyPair]

		badMagic := []byte{0, 0, 0, 0}
		_, err = f.WriteAt(badMagic, meta.Offset)
		require.NoError(t, err)

		_, err = v.Read(keyPair, cookieVal)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMagicNumber)
	})

	t.Run("Error_CRC_Mismatch", func(t *testing.T) {
		v, f := setupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		meta := v.index[keyPair]

		dataStartOffset := meta.Offset + 29

		_, err = f.WriteAt([]byte{'X'}, dataStartOffset)
		require.NoError(t, err)

		_, err = v.Read(keyPair, cookieVal)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrCrcNotValid)
	})
}

func newRandomNeedle(key uint64, size int) *Needle {
	payload := make([]byte, size)
	payload[0] = byte(key)
	payload[size-1] = byte(key >> 8)

	return NewNeedle(key, 0, 0, payload)
}

func TestVolume_Delete(t *testing.T) {
	const cookie = uint64(4242)

	keyPair := KeyPair{Key: 7, AltKey: 3}

	newNeedle := func() *Needle {
		payload := []byte("to be deleted")
		return NewNeedle(keyPair.Key, keyPair.AltKey, cookie, payload)
	}

	t.Run("MissingKeyIsNoOp", func(t *testing.T) {
		v, _ := setupTestVolume(t)

		require.NoError(t, v.Delete(KeyPair{Key: 999}, cookie))
		assert.Zero(t, v.writeOffset, "a no-op delete must not append a tombstone")
		assert.Empty(t, v.index)
	})

	t.Run("RemovesFromIndex", func(t *testing.T) {
		v, _ := setupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		require.NoError(t, v.Delete(keyPair, cookie))

		assert.NotContains(t, v.index, keyPair)

		_, err := v.Read(keyPair, cookie)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("AppendsTombstone", func(t *testing.T) {
		v, f := setupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		liveOffset := v.writeOffset
		require.NoError(t, v.Delete(keyPair, cookie))

		assert.Greater(t, v.writeOffset, liveOffset, "tombstone must advance the write offset")

		// header(29) + no data + footer(8) = 37, padded to 40
		assert.Equal(t, liveOffset+40, v.writeOffset)

		tombstone := make([]byte, v.writeOffset-liveOffset)
		_, err := f.ReadAt(tombstone, liveOffset)
		require.NoError(t, err)

		assert.ErrorIs(t, ValidNeedleBlock(tombstone, cookie), ErrDataDeleted)
	})

	t.Run("DeleteTwiceIsNoOp", func(t *testing.T) {
		v, _ := setupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))
		require.NoError(t, v.Delete(keyPair, cookie))

		afterFirst := v.writeOffset

		require.NoError(t, v.Delete(keyPair, cookie))
		assert.Equal(t, afterFirst, v.writeOffset, "second delete must not append another tombstone")
	})

	t.Run("RewriteAfterDelete", func(t *testing.T) {
		v, _ := setupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))
		require.NoError(t, v.Delete(keyPair, cookie))
		require.NoError(t, v.Write(newNeedle()))

		got, err := v.Read(keyPair, cookie)
		require.NoError(t, err)
		assert.Equal(t, []byte("to be deleted"), got)
	})

	t.Run("AppendOnly_NeverReusesFreedSpace", func(t *testing.T) {
		v, _ := setupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		freedOffset := v.index[keyPair].Offset

		require.NoError(t, v.Delete(keyPair, cookie))
		require.NoError(t, v.Write(newNeedle()))

		assert.Greater(t, v.index[keyPair].Offset, freedOffset,
			"rewrite must append past the tombstone, never reuse the freed span")

		got, err := v.Read(keyPair, cookie)
		require.NoError(t, err)
		assert.Equal(t, []byte("to be deleted"), got)
	})

	t.Run("DeleteAccountsDeadBytes", func(t *testing.T) {
		v, _ := setupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		tombstone := v.writeOffset
		require.NoError(t, v.Delete(keyPair, cookie))

		assert.Equal(t, uint64(v.writeOffset), v.deadBytes.Load(),
			"after deleting the only needle the whole file is dead")
		assert.True(t, v.ShouldCompact())
		assert.Greater(t, v.writeOffset, tombstone)
	})

	t.Run("OverwriteAccountsDeadBytes", func(t *testing.T) {
		v, _ := setupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		first := v.writeOffset
		require.NoError(t, v.Write(newNeedle()))

		assert.Equal(t, uint64(first), v.deadBytes.Load(),
			"the superseded needle is dead space")
	})
}

func TestVolume_Sync(t *testing.T) {
	v, _ := setupTestVolume(t)

	require.NoError(t, v.Write(newRandomNeedle(1, 64)))
	assert.NoError(t, v.Sync())
}

func TestVolume_WriteTooLarge(t *testing.T) {
	v, _ := setupTestVolume(t)

	err := v.Write(newRandomNeedle(1, xlargeSize+1))

	assert.ErrorIs(t, err, ErrToLarge)
	assert.Zero(t, v.writeOffset)
	assert.Empty(t, v.index)
}

func makeNeedle(key uint64, headerSize uint32, dataLen int) *Needle {
	n := NewNeedle(key, 100, 12345, bytes.Repeat([]byte{0xAB}, dataLen))
	n.Header.Size = headerSize
	return n
}

func TestSizeConsistency(t *testing.T) {
	t.Run("HeaderSizeMismatchData", func(t *testing.T) {
		cases := []struct {
			name       string
			headerSize uint32
			dataLen    int
		}{
			{"header=0,data=4096", 0, 4096},
			{"header=4096,data=0", 4096, 0},
			{"header=100,data=101", 100, 101},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				v, _ := setupTestVolume(t)
				before := v.writeOffset

				err := v.Write(makeNeedle(1, tc.headerSize, tc.dataLen))

				require.Equal(t, err, ErrInvalidNeedle)
				assert.Equal(t, before, v.writeOffset, "rejected write must not advance offset")
				_, ok := v.index[KeyPair{Key: 1, AltKey: 100}]
				assert.False(t, ok, "rejected write must not touch index")
			})
		}
	})
}

func TestVolume_ReadDoesNotAliasPool(t *testing.T) {
	volume, _ := setupTestVolume(t)

	first := bytes.Repeat([]byte("A"), 512)
	second := bytes.Repeat([]byte("B"), 512)

	require.NoError(t, volume.Write(NewNeedle(1, 0, 7, first)))
	require.NoError(t, volume.Write(NewNeedle(2, 0, 7, second)))

	got1, err := volume.Read(KeyPair{Key: 1}, 7)
	require.NoError(t, err)

	got2, err := volume.Read(KeyPair{Key: 2}, 7)
	require.NoError(t, err)

	assert.Equal(t, first, got1, "first read corrupted by second read")
	assert.Equal(t, second, got2)
}
