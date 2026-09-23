package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite(t *testing.T) {
	volume, _ := SetupTestVolume(t)

	needle := NewNeedle(1, 100, 12345, make([]byte, 4096))

	assert.NoError(t, volume.Write(needle))

	key := KeyPair{Key: 1, AltKey: 100}
	metas := volume.index[key]

	assert.Equal(t, int64(NeedleStartOffset), metas.Offset, "First offset should start after the superblock")
	assert.Equal(t, uint32(4096), metas.Size, "Size should be data size only")

	// header + data + footer + padding
	// (29+ 4096 + 8 + n) % 8 = 0;total = 4136
	expectedTotalSize := int64(4136)
	assert.Equal(t, int64(NeedleStartOffset)+expectedTotalSize, volume.writeOffset, "Write offset calculation incorrect")

	assert.NoError(t, volume.Write(needle))
	metas = volume.index[key]

	assert.Equal(t, int64(NeedleStartOffset)+expectedTotalSize, metas.Offset, "Second offset should start after first needle")

	assert.Equal(t, int64(NeedleStartOffset)+expectedTotalSize*2, volume.writeOffset, "Final write offset incorrect")
}

func TestVolume_Read(t *testing.T) {
	payload := []byte("hello world data")
	keyVal := uint64(100)
	cookieVal := uint64(9999)
	altKeyVal := uint32(50)

	keyPair := KeyPair{Key: keyVal, AltKey: altKeyVal}

	needle := NewNeedle(keyVal, altKeyVal, cookieVal, payload)

	t.Run("Success_HappyPath", func(t *testing.T) {
		v, _ := SetupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		gotBytes, err := v.Read(keyPair, cookieVal)
		assert.NoError(t, err)
		assert.Equal(t, gotBytes, payload)
	})

	t.Run("Error_KeyNotFound", func(t *testing.T) {
		v, _ := SetupTestVolume(t)

		wrongKey := KeyPair{Key: 99999, AltKey: 0}
		_, err := v.Read(wrongKey, cookieVal)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Error_InvalidCookie", func(t *testing.T) {
		v, _ := SetupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		wrongCookie := uint64(1111)
		_, err = v.Read(keyPair, wrongCookie)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrCookie)
	})

	t.Run("Error_DataDeleted", func(t *testing.T) {
		v, f := SetupTestVolume(t)

		err := v.Write(needle)
		require.NoError(t, err)

		meta := v.index[keyPair]

		flagOffset := meta.Offset + 24

		_, err = f.WriteAt([]byte{1}, flagOffset)
		require.NoError(t, err)

		_, err = v.Read(keyPair, cookieVal)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDataNotNormal)
	})

	t.Run("Error_MagicHeaderMismatch", func(t *testing.T) {
		v, f := SetupTestVolume(t)

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
		v, f := SetupTestVolume(t)

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
		v, _ := SetupTestVolume(t)

		require.NoError(t, v.Delete(KeyPair{Key: 999}, cookie))
		assert.Equal(t, int64(NeedleStartOffset), v.writeOffset, "a no-op delete must not append a tombstone")
		assert.Empty(t, v.index)
	})

	t.Run("RemovesFromIndex", func(t *testing.T) {
		v, _ := SetupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		require.NoError(t, v.Delete(keyPair, cookie))

		assert.NotContains(t, v.index, keyPair)

		_, err := v.Read(keyPair, cookie)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("AppendsTombstone", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		liveOffset := v.writeOffset
		require.NoError(t, v.Delete(keyPair, cookie))

		assert.Greater(t, v.writeOffset, liveOffset, "tombstone must advance the write offset")

		// header(29) + offset(8) + footer(8) = 45, padded to 48
		assert.Equal(t, liveOffset+48, v.writeOffset)

		tombstone := make([]byte, v.writeOffset-liveOffset)
		_, err := f.ReadAt(tombstone, liveOffset)
		require.NoError(t, err)

		assert.ErrorIs(t, ValidNeedleBlock(tombstone, cookie), ErrDataNotNormal)
	})

	t.Run("DeleteTwiceIsNoOp", func(t *testing.T) {
		v, _ := SetupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))
		require.NoError(t, v.Delete(keyPair, cookie))

		afterFirst := v.writeOffset

		require.NoError(t, v.Delete(keyPair, cookie))
		assert.Equal(t, afterFirst, v.writeOffset, "second delete must not append another tombstone")
	})

	t.Run("RewriteAfterDelete", func(t *testing.T) {
		v, _ := SetupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))
		require.NoError(t, v.Delete(keyPair, cookie))
		require.NoError(t, v.Write(newNeedle()))

		got, err := v.Read(keyPair, cookie)
		require.NoError(t, err)
		assert.Equal(t, []byte("to be deleted"), got)
	})

	t.Run("AppendOnly_NeverReusesFreedSpace", func(t *testing.T) {
		v, _ := SetupTestVolume(t)
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
		v, _ := SetupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		tombstone := v.writeOffset
		require.NoError(t, v.Delete(keyPair, cookie))

		assert.Equal(t, uint64(v.writeOffset-NeedleStartOffset), v.deadBytes.Load(),
			"after deleting the only needle the whole file is dead")
		assert.True(t, v.ShouldCompact())
		assert.Greater(t, v.writeOffset, tombstone)
	})

	t.Run("OverwriteAccountsDeadBytes", func(t *testing.T) {
		v, _ := SetupTestVolume(t)
		require.NoError(t, v.Write(newNeedle()))

		first := v.writeOffset
		require.NoError(t, v.Write(newNeedle()))

		assert.Equal(t, uint64(first-NeedleStartOffset), v.deadBytes.Load(),
			"the superseded needle is dead space")
	})
}

func TestVolume_Sync(t *testing.T) {
	v, _ := SetupTestVolume(t)

	require.NoError(t, v.Write(newRandomNeedle(1, 64)))
	assert.NoError(t, v.Sync())
}

func TestVolume_WriteTooLarge(t *testing.T) {
	v, _ := SetupTestVolume(t)

	err := v.Write(newRandomNeedle(1, xlargeSize+1))

	assert.ErrorIs(t, err, ErrToLarge)
	assert.Equal(t, int64(NeedleStartOffset), v.writeOffset)
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
				v, _ := SetupTestVolume(t)
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
	volume, _ := SetupTestVolume(t)

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

func TestVolume_Superblock(t *testing.T) {
	t.Run("WrittenOnCreate", func(t *testing.T) {
		v, f := SetupTestVolume(t)

		assert.Equal(t, int64(NeedleStartOffset), v.writeOffset)

		header, err := ReadSuperblock(f)
		require.NoError(t, err)

		assert.Equal(t, uint64(1), header.VolumeID)
		assert.Equal(t, uint32(FormatVersion), header.FormatVersion)
		assert.False(t, header.Sealed())
		assert.Equal(t, header, v.header)
	})

	t.Run("ReusedOnReopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "test.vol")

		f, err := os.Create(path)
		require.NoError(t, err)

		v, err := NewVolume(f, 7)
		require.NoError(t, err)
		require.NoError(t, v.Write(newRandomNeedle(1, 64)))
		require.NoError(t, f.Close())

		f, err = os.OpenFile(path, os.O_RDWR, 0o600)
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })

		reopened, err := NewVolume(f, 999)
		require.NoError(t, err)

		assert.Equal(t, uint64(7), reopened.header.VolumeID, "volume id comes from the file, not the caller")
		assert.Equal(t, v.header.CreatedAtUnixNano, reopened.header.CreatedAtUnixNano)
	})

	t.Run("RejectsCorruptSuperblock", func(t *testing.T) {
		f := setupTestFile(t)

		_, err := f.WriteAt(make([]byte, SuperblockSize), 0)
		require.NoError(t, err)

		_, err = NewVolume(f, 1)
		assert.ErrorIs(t, err, ErrInvalidSuperblock)
	})

	t.Run("SealIsWrittenAndPersisted", func(t *testing.T) {
		v, f := SetupTestVolume(t)

		require.NoError(t, v.Seal())

		header, err := ReadSuperblock(f)
		require.NoError(t, err)
		assert.True(t, header.Sealed())
		assert.Equal(t, v.header.SealedAtUnixNano, header.SealedAtUnixNano)

		sealedAt := v.header.SealedAtUnixNano
		require.NoError(t, v.Seal())
		assert.Equal(t, sealedAt, v.header.SealedAtUnixNano, "sealing twice must not move the timestamp")
	})

	t.Run("SealedVolumeIsReadOnly", func(t *testing.T) {
		v, _ := SetupTestVolume(t)

		keyPair := KeyPair{Key: 1}
		require.NoError(t, v.Write(NewNeedle(keyPair.Key, keyPair.AltKey, 42, []byte("live"))))
		require.NoError(t, v.Seal())

		offset := v.writeOffset

		assert.ErrorIs(t, v.Write(newRandomNeedle(2, 64)), ErrVolumeSealed)
		assert.ErrorIs(t, v.Delete(keyPair, 42), ErrVolumeSealed)
		assert.Equal(t, offset, v.writeOffset)

		got, err := v.Read(keyPair, 42)
		require.NoError(t, err)
		assert.Equal(t, []byte("live"), got)
	})
}

func TestVolume_Scan(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		v, _ := SetupTestVolume(t)

		end, calls := runScan(t, v)
		assert.Equal(t, int64(NeedleStartOffset), end)
		assert.Equal(t, 0, calls)
	})

	t.Run("AllNeedles", func(t *testing.T) {
		v, _ := SetupTestVolume(t)

		payloads := [][]byte{[]byte("a"), make([]byte, 4096), make([]byte, 2*1024*1024)}
		var offsets []int64
		for i, p := range payloads {
			require.NoError(t, v.Write(NewNeedle(uint64(i), 0, 1, p)))
			offsets = append(offsets, v.index[KeyPair{Key: uint64(i)}].Offset)
		}

		var got []int64
		end, err := v.scan(func(off int64, _ NeedleHeader, _ []byte) error {
			got = append(got, off)
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, offsets, got)
		assert.Equal(t, v.writeOffset, end)
	})

	t.Run("TruncatedTail", func(t *testing.T) {
		for _, cut := range []int64{1, 8, 16, 18, 19, 20, 30} {
			v, f := SetupTestVolume(t)
			require.NoError(t, v.Write(NewNeedle(1, 0, 1, []byte("first"))))
			first := v.writeOffset
			require.NoError(t, v.Write(NewNeedle(2, 0, 1, []byte("second"))))

			require.NoError(t, f.Truncate(v.writeOffset-cut))

			end, calls := runScan(t, v)
			assert.Equal(t, first, end, "cut %d", cut)
			assert.Equal(t, 1, calls, "cut %d", cut)
		}
	})

	t.Run("CorruptData", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, 1, []byte("first"))))
		second := v.writeOffset
		require.NoError(t, v.Write(NewNeedle(2, 0, 1, []byte("second"))))
		require.NoError(t, v.Write(NewNeedle(3, 0, 1, []byte("third"))))

		_, err := f.WriteAt([]byte{'X'}, second+NeedleHeaderSize)
		require.NoError(t, err)

		end, calls := runScan(t, v)
		assert.Equal(t, 1, calls)
		assert.Equal(t, second, end)
	})

	t.Run("CorruptFirstSize", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, 1, []byte("first"))))

		_, err := f.WriteAt([]byte{0xFF, 0xFF, 0xFF, 0xFF}, NeedleStartOffset+25)
		require.NoError(t, err)

		end, calls := runScan(t, v)
		assert.Equal(t, 0, calls)
		assert.Equal(t, int64(NeedleStartOffset), end)
	})

	t.Run("DeletedNeedle", func(t *testing.T) {
		v, _ := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, 1, []byte("first"))))
		require.NoError(t, v.Delete(KeyPair{Key: 1}, 1))

		var flags []byte
		end, err := v.scan(func(_ int64, h NeedleHeader, _ []byte) error {
			flags = append(flags, h.Flag)
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, v.writeOffset, end)
		assert.Equal(t, []byte{DeleteByte, TombstoneByte}, flags)
	})

	t.Run("CorruptSize", func(t *testing.T) {
		cases := []struct {
			name  string
			write func(size uint32) uint32
		}{
			{"Huge", func(uint32) uint32 { return 0xFFFFFFFF }},
			{"Shrunk", func(s uint32) uint32 { return s - 4 }},
			{"Grown", func(s uint32) uint32 { return s + 4 }},
			{"Zero", func(uint32) uint32 { return 0 }},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				v, f, second, _ := setupThree(t)

				var buf [4]byte
				binary.BigEndian.PutUint32(buf[:], tc.write(uint32(len("second"))))
				poke(t, f, second+25, buf[:]...)

				end, calls := runScan(t, v)
				assert.Equal(t, second, end)
				assert.Equal(t, 1, calls)
			})
		}
	})

	t.Run("BadHeaderMagic", func(t *testing.T) {
		v, f, second, _ := setupThree(t)
		poke(t, f, second+4, 0xDE, 0xAD, 0xBE, 0xEF)

		end, calls := runScan(t, v)
		assert.Equal(t, second, end)
		assert.Equal(t, 1, calls)
	})

	t.Run("ZeroTail", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, 1, []byte("first"))))
		want := v.writeOffset
		require.NoError(t, f.Truncate(want+4096))

		end, calls := runScan(t, v)
		assert.Equal(t, want, end)
		assert.Equal(t, 1, calls)
	})

	t.Run("BadFlag", func(t *testing.T) {
		v, f, second, _ := setupThree(t)
		poke(t, f, second+1, 0x7F)

		end, calls := runScan(t, v)
		assert.Equal(t, second, end)
		assert.Equal(t, 1, calls)
	})

	t.Run("BadPadding", func(t *testing.T) {
		v, f, second, third := setupThree(t)
		poke(t, f, third-1, 0x01)

		end, calls := runScan(t, v)
		assert.Equal(t, second, end)
		assert.Equal(t, 1, calls)
	})
}

func TestVolume_Reload(t *testing.T) {
	const cookie = uint64(1)

	t.Run("RebuildsIndex", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, cookie, []byte("one"))))
		require.NoError(t, v.Write(NewNeedle(2, 0, cookie, []byte("two"))))
		require.NoError(t, v.Write(NewNeedle(2, 0, cookie, []byte("two-v2"))))

		r := reopen(t, f)

		assert.Equal(t, v.index, r.index)
		assert.Equal(t, v.writeOffset, r.writeOffset)
		assert.Equal(t, v.deadBytes.Load(), r.deadBytes.Load())

		got, err := r.Read(KeyPair{Key: 2}, cookie)
		require.NoError(t, err)
		assert.Equal(t, []byte("two-v2"), got)
	})

	t.Run("EmptyVolume", func(t *testing.T) {
		_, f := SetupTestVolume(t)

		r := reopen(t, f)

		assert.Empty(t, r.index)
		assert.Equal(t, int64(NeedleStartOffset), r.writeOffset)
	})

	t.Run("TruncatesTornTail", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, cookie, []byte("one"))))
		end := v.writeOffset
		poke(t, f, end, 0xDE, 0xAD, 0xBE, 0xEF)

		r := reopen(t, f)

		assert.Equal(t, end, r.writeOffset)
		info, err := f.Stat()
		require.NoError(t, err)
		assert.Equal(t, end, info.Size())

		require.NoError(t, r.Write(NewNeedle(2, 0, cookie, []byte("two"))))
		got, err := r.Read(KeyPair{Key: 2}, cookie)
		require.NoError(t, err)
		assert.Equal(t, []byte("two"), got)
	})

	t.Run("SealedTornTailIsCorrupt", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, cookie, []byte("one"))))
		require.NoError(t, v.Seal())
		poke(t, f, v.writeOffset, 0xDE, 0xAD)

		r, err := NewVolume(f, 1)
		require.NoError(t, err)
		assert.ErrorIs(t, r.Reload(), ErrCorruptVolume)
	})
}

func TestVolume_ReloadWithDelete(t *testing.T) {
	const cookie = uint64(1)

	t.Run("DeletedKeyStaysDeleted", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, cookie, []byte("one"))))
		require.NoError(t, v.Write(NewNeedle(2, 0, cookie, []byte("two"))))
		require.NoError(t, v.Delete(KeyPair{Key: 1}, cookie))

		r := reopen(t, f)

		assert.NotContains(t, r.index, KeyPair{Key: 1})
		assert.Equal(t, v.index, r.index)
		assert.Equal(t, v.writeOffset, r.writeOffset)
		assert.Equal(t, v.deadBytes.Load(), r.deadBytes.Load())

		got, err := r.Read(KeyPair{Key: 2}, cookie)
		require.NoError(t, err)
		assert.Equal(t, []byte("two"), got)
	})

	t.Run("CrashBeforeFlagFlip", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, cookie, []byte("one"))))
		victim := v.index[KeyPair{Key: 1}].Offset
		require.NoError(t, v.Delete(KeyPair{Key: 1}, cookie))
		poke(t, f, victim+24, NormalByte)

		r := reopen(t, f)

		assert.NotContains(t, r.index, KeyPair{Key: 1})
		assert.Equal(t, v.deadBytes.Load(), r.deadBytes.Load())

		flag := make([]byte, 1)
		_, err := f.ReadAt(flag, victim+24)
		require.NoError(t, err)
		assert.Equal(t, DeleteByte, flag[0])

		r2 := reopen(t, f)
		assert.Equal(t, r.index, r2.index)
	})

	t.Run("ReopenUseOldKey", func(t *testing.T) {
		v, f := SetupTestVolume(t)
		require.NoError(t, v.Write(NewNeedle(1, 0, cookie, []byte("one"))))
		r := reopen(t, f)
		require.NoError(t, r.Write(NewNeedle(2, 0, cookie, []byte("two"))))
		got, err := r.Read(KeyPair{Key: 1}, cookie)
		require.NoError(t, err)
		assert.Equal(t, []byte("one"), got)
	})
}
