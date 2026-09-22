package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

type KeyPair struct {
	Key    uint64
	AltKey uint32
}

type Volume struct {
	dataFile    *os.File
	index       map[KeyPair]NeedleMeta
	bufferPool  *BufferPool
	writeOffset int64
	mu          sync.RWMutex

	deadBytes atomic.Uint64
}

const compactRatio = 0.6

func (v *Volume) ShouldCompact() bool {
	v.mu.RLock()
	total := v.writeOffset
	v.mu.RUnlock()

	if total <= 0 {
		return false
	}

	return float64(v.deadBytes.Load())/float64(total) >= compactRatio
}

type NeedleMeta struct {
	Offset int64
	Size   uint32
}

var (
	bufferPool *BufferPool
	O          sync.Once
)

func NewVolume(dataFile *os.File) *Volume {
	O.Do(func() {
		bufferPool = NewBufferPool()
		bufferPool.WarnUp()
	})

	v := &Volume{
		dataFile:    dataFile,
		index:       make(map[KeyPair]NeedleMeta),
		writeOffset: 0,
		bufferPool:  bufferPool,
	}

	return v
}

func (v *Volume) Write(n *Needle) error {

	if n.Header.Size != uint32(len(n.Data)) {
		return ErrInvalidNeedle
	}

	dataBytes, err := n.Bytes(v.bufferPool)
	if err != nil {
		return fmt.Errorf("write error: %w", err)
	}

	defer v.bufferPool.Put(dataBytes)

	v.mu.Lock()
	defer v.mu.Unlock()

	offset := v.writeOffset

	if n, err := v.dataFile.WriteAt(dataBytes.B, offset); err != nil || n != len(dataBytes.B) {
		return fmt.Errorf("write error: %v", err)
	}

	key := KeyPair{
		Key:    n.Header.Key,
		AltKey: n.Header.AlternateKey,
	}

	if old, ok := v.index[key]; ok {
		v.deadBytes.Add(uint64(onDiskSize(old.Size)))
	}

	v.index[key] = NeedleMeta{
		Offset: offset,
		Size:   n.Header.Size,
	}

	v.writeOffset += int64(len(dataBytes.B))

	return nil
}

func (v *Volume) Read(key KeyPair, cookie uint64) ([]byte, error) {
	v.mu.RLock()
	meta, ok := v.index[key]
	f := v.dataFile
	v.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("read not found the data from key: %w: %v", ErrNotFound, key)
	}

	lastMetaSize := meta.Size

	totalSize := NeedleHeaderSize + lastMetaSize + NeedleFooterSize

	buf, err := v.bufferPool.Get(totalSize)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	buf.B = buf.B[:totalSize]

	defer v.bufferPool.Put(buf)

	n, err := f.ReadAt(buf.B, meta.Offset)

	if errors.Is(err, io.EOF) && n == int(totalSize) {
		_ = err
	} else if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	err = ValidNeedleBlock(buf.B, cookie)
	if err != nil {
		return nil, fmt.Errorf("error in read when valid needle block: %w", err)
	}

	data, err := GetNeedleBlockInfo(totalSize, lastMetaSize, buf.B)
	if err != nil {
		return nil, err
	}

	return append([]byte(nil), data...), nil
}

func (v *Volume) Sync() error {
	return v.dataFile.Sync()
}

func (v *Volume) Reload() {
	// TODO when the system start reload(recover) the data from disk
	panic("implement me")
}

func (v *Volume) Delete(key KeyPair, cookie uint64) error {
	v.mu.Lock()
	meta, ok := v.index[key]

	if !ok {
		v.mu.Unlock()
		return nil
	}
	delete(v.index, key)
	v.mu.Unlock()

	delNeedle := NewNeedle(key.Key, key.AltKey, cookie, nil, DeleteByte)

	buf, err := delNeedle.Bytes(v.bufferPool)
	if err != nil {
		return fmt.Errorf("delete error: %w", err)
	}

	defer v.bufferPool.Put(buf)

	v.mu.Lock()
	n, err := v.dataFile.WriteAt(buf.B, v.writeOffset)
	if err != nil {
		v.mu.Unlock()
		return fmt.Errorf("write error: %v", err)
	}

	if n != len(buf.B) {
		v.mu.Unlock()
		return fmt.Errorf("write error: %v", io.ErrShortWrite)
	}

	v.writeOffset += int64(n)
	v.mu.Unlock()

	v.deadBytes.Add(uint64(onDiskSize(meta.Size)) + uint64(onDiskSize(0)))

	return nil
}

func onDiskSize(payload uint32) uint32 {
	return align8(NeedleHeaderSize + payload + NeedleFooterSize)
}
