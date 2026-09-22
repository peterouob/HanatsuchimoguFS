package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferPool_GetPicksSmallestFittingBucket(t *testing.T) {
	bp := NewBufferPool()

	tests := []struct {
		request uint32
		wantCap int
	}{
		{0, xsmallSize},
		{1, xsmallSize},
		{xsmallSize, xsmallSize},
		{xsmallSize + 1, smallSize},
		{smallSize, smallSize},
		{smallSize + 1, mediumSize},
		{mediumSize, mediumSize},
		{mediumSize + 1, largeSize},
		{largeSize, largeSize},
		{largeSize + 1, xlargeSize},
		{xlargeSize, xlargeSize},
	}

	for _, tt := range tests {
		buf, err := bp.Get(tt.request)

		require.NoError(t, err, "Get(%d)", tt.request)
		assert.Equal(t, tt.wantCap, cap(buf.B), "Get(%d) bucket", tt.request)
		assert.Empty(t, buf.B, "Get(%d) must hand back an empty slice", tt.request)
	}
}

func TestBufferPool_GetTooLarge(t *testing.T) {
	bp := NewBufferPool()

	buf, err := bp.Get(xlargeSize + 1)

	assert.Nil(t, buf)
	assert.ErrorIs(t, err, ErrToLarge)
}

func TestBufferPool_GetResetsDirtyBuffer(t *testing.T) {
	bp := NewBufferPool()

	buf, err := bp.Get(8)
	require.NoError(t, err)
	buf.B = append(buf.B, "dirty"...)
	bp.Put(buf)

	reused, err := bp.Get(8)
	require.NoError(t, err)
	assert.Empty(t, reused.B, "a recycled buffer must come back with len 0")
}

func TestBufferPool_PutDropsForeignCapacity(t *testing.T) {
	bp := NewBufferPool()

	bp.Put(&Buffer{B: make([]byte, 0, xsmallSize+1)})

	buf, err := bp.Get(xsmallSize)
	require.NoError(t, err)
	assert.Equal(t, xsmallSize, cap(buf.B), "xsmall bucket must still hand out xsmall buffers")
}

func TestBufferPool_CustomSizes(t *testing.T) {
	bp := NewBufferPool(16, 128)

	buf, err := bp.Get(17)
	require.NoError(t, err)
	assert.Equal(t, 128, cap(buf.B))

	_, err = bp.Get(129)
	assert.ErrorIs(t, err, ErrToLarge)
}

func TestBufferPool_WarnUp(t *testing.T) {
	bp := NewBufferPool()
	bp.WarnUp()

	for _, size := range bp.size {
		buf, err := bp.Get(size)
		require.NoError(t, err)
		assert.Equal(t, int(size), cap(buf.B))
		assert.Empty(t, buf.B)
	}
}

func TestBufferPool_RoundTripKeepsCapacityContract(t *testing.T) {
	bp := NewBufferPool()

	for range 100 {
		buf, err := bp.Get(mediumSize)
		require.NoError(t, err)
		require.Equal(t, mediumSize, cap(buf.B))

		buf.B = buf.B[:mediumSize]
		bp.Put(buf)
	}
}
