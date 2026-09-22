package storage

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setUp(payload []byte) *Needle {

	return NewNeedle(1001, 2, 0x1234567890ABCDEF, payload)
}

func TestNeedle_Bytes(t *testing.T) {
	dataPayload := []byte("12345")
	needle := setUp(dataPayload)
	pool := NewBufferPool()
	buf, err := needle.Bytes(pool)
	assert.NoError(t, err)
	getBytes := buf.B
	assert.Equal(t, len(getBytes)%8, 0) // check align 8 byte

	// before the padding size is 29+5+8 = 42
	// we want be aligned to 8 byte so actually will be 48
	assert.Equal(t, len(getBytes), 48)

	magic := binary.BigEndian.Uint32(getBytes[0:4])
	assert.Equal(t, magic, needle.Header.MagicHeader)

	cookie := binary.BigEndian.Uint64(getBytes[4:12])
	assert.Equal(t, cookie, needle.Header.Cookie)

	key := binary.BigEndian.Uint64(getBytes[12:20])
	assert.Equal(t, key, needle.Header.Key)

	altKey := binary.BigEndian.Uint32(getBytes[20:24])
	assert.Equal(t, altKey, needle.Header.AlternateKey)

	flag := getBytes[24]
	assert.Equal(t, flag, needle.Header.Flag)

	size := binary.BigEndian.Uint32(getBytes[25:29])
	assert.Equal(t, size, needle.Header.Size)

	data := getBytes[29 : 29+len(dataPayload)]
	assert.Equal(t, data, dataPayload)

	crc := binary.BigEndian.Uint32(getBytes[29+len(dataPayload) : 29+len(dataPayload)+4])
	expectCRC := NewCRC(getBytes[:29+len(dataPayload)]).Value()
	assert.Equal(t, expectCRC, crc, "footer checksum must cover header+data")
	assert.Equal(t, expectCRC, needle.Footer.Checksum, "Bytes() must publish the checksum it wrote")

	magic = binary.BigEndian.Uint32(getBytes[29+len(dataPayload)+4 : 29+len(dataPayload)+8])
	assert.Equal(t, magic, needle.Footer.MagicFooter)

	ps := 29 + len(dataPayload) + 8
	paddingArea := getBytes[ps:]
	expectedPaddingLen := (8 - (42 % 8)) % 8
	assert.Equal(t, expectedPaddingLen, len(paddingArea))

	for _, b := range paddingArea {
		assert.Equal(t, byte(0), b, "Padding byte should be zero")
	}
}

func TestNeedle_Bytes_TooLarge(t *testing.T) {
	needle := setUp(make([]byte, xlargeSize+1))

	buf, err := needle.Bytes(NewBufferPool())

	assert.Nil(t, buf)
	assert.ErrorIs(t, err, ErrToLarge)
}

func TestValidNeedleBlock(t *testing.T) {
	const cookie = uint64(0x1234567890ABCDEF)

	valid, err := setUp([]byte("payload")).Bytes(NewBufferPool())
	assert.NoError(t, err)

	tests := []struct {
		name    string
		mutate  func(b []byte)
		cookie  uint64
		wantErr error
	}{
		{"Valid", func([]byte) {}, cookie, nil},
		{"BadMagicHeader", func(b []byte) { binary.BigEndian.PutUint32(b[0:4], 0) }, cookie, ErrMagicNumber},
		{"BadCookie", func([]byte) {}, cookie + 1, ErrCookie},
		{"Deleted", func(b []byte) { b[24] = DeleteByte }, cookie, ErrDataDeleted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := append([]byte(nil), valid.B...)
			tt.mutate(b)

			err := ValidNeedleBlock(b, tt.cookie)

			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}

	t.Run("TooShort", func(t *testing.T) {
		assert.ErrorIs(t, ValidNeedleBlock(make([]byte, 24), cookie), ErrBufferTooSmall)
	})
}

func TestGetNeedleBlockInfo_BadMagicFooter(t *testing.T) {
	payload := []byte("truncated tail")
	needle := NewNeedle(1, 2, 3, payload)

	buf, err := needle.Bytes(NewBufferPool())
	require.NoError(t, err)

	totalSize := uint32(NeedleHeaderSize + len(payload) + NeedleFooterSize)
	buf.B[totalSize-1] ^= 0xFF

	_, err = GetNeedleBlockInfo(totalSize, uint32(len(payload)), buf.B)
	assert.ErrorIs(t, err, ErrInvalidMagicFooter)
}
