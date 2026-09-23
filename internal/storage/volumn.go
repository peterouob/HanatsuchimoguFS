package storage

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/peterouob/HanatsuchimoguFS/utils"
)

type KeyPair struct {
	Key    uint64
	AltKey uint32
}

type Volume struct {
	dataFile    *os.File
	header      Superblock
	index       map[KeyPair]NeedleMeta
	bufferPool  *BufferPool
	writeOffset int64
	mu          sync.RWMutex

	deadBytes atomic.Uint64
}

const compactRatio = 0.6

func (v *Volume) ShouldCompact() bool {
	v.mu.RLock()
	total := v.writeOffset - NeedleStartOffset
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

func NewVolume(dataFile *os.File, volumeID uint64) (*Volume, error) {
	O.Do(func() {
		bufferPool = NewBufferPool()
		bufferPool.WarnUp()
	})

	info, err := dataFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("new volume: %w", err)
	}

	var header Superblock

	switch info.Size() {
	case 0:
		header = NewSuperblock(volumeID)
		if err := WriteSuperblock(dataFile, header); err != nil {
			return nil, err
		}
	default:
		header, err = ReadSuperblock(dataFile)
		if err != nil {
			return nil, err
		}
	}

	v := &Volume{
		dataFile:    dataFile,
		header:      header,
		index:       make(map[KeyPair]NeedleMeta),
		writeOffset: NeedleStartOffset,
		bufferPool:  bufferPool,
	}

	return v, nil
}

func (v *Volume) Seal() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.header.Sealed() {
		return nil
	}

	sealedAt := time.Now().UnixNano()

	if err := SealSuperblock(v.dataFile, sealedAt); err != nil {
		return err
	}

	if err := v.dataFile.Sync(); err != nil {
		return fmt.Errorf("seal volume: %w", err)
	}

	v.header.SealedAtUnixNano = sealedAt

	return nil
}

func (v *Volume) Write(n *Needle) error {
	dataSize, err := utils.CIU[int, uint32](len(n.Data))
	if err != nil {
		return err
	}

	if n.Header.Size != dataSize {
		return ErrInvalidNeedle
	}

	dataBytes, err := n.Bytes(v.bufferPool)
	if err != nil {
		return fmt.Errorf("write error: %w", err)
	}

	defer v.bufferPool.Put(dataBytes)

	v.mu.Lock()
	defer v.mu.Unlock()

	if v.header.Sealed() {
		return ErrVolumeSealed
	}

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

func (v *Volume) Reload() error {
	var pending []int64

	reloadScan := func(off int64, h NeedleHeader, data []byte) error {
		k := KeyPair{Key: h.Key, AltKey: h.AlternateKey}
		switch h.Flag {
		case NormalByte:
			if old, ok := v.index[k]; ok {
				v.deadBytes.Add(uint64(onDiskSize(old.Size)))
			}
			v.index[k] = NeedleMeta{
				Offset: off,
				Size:   h.Size,
			}
		case DeleteByte:
			v.deadBytes.Add(uint64(onDiskSize(h.Size)))
		case TombstoneByte:
			victim, err := utils.CUI[uint64, int64](binary.BigEndian.Uint64(data))
			if err != nil {
				return err
			}
			if m, ok := v.index[k]; ok && m.Offset == victim {
				delete(v.index, k)
				v.deadBytes.Add(uint64(onDiskSize(m.Size)))
				pending = append(pending, victim)
			}
			v.deadBytes.Add(uint64(onDiskSize(8)))
		}
		return nil
	}

	end, err := v.scan(reloadScan)
	if err != nil {
		return err
	}

	info, err := v.dataFile.Stat()
	if err != nil {
		return err
	}

	if size := info.Size(); end < size {
		if v.header.Sealed() {
			return fmt.Errorf("%w: valid data ends at %d, file size %d", ErrCorruptVolume, end, info.Size())
		}

		if err := v.dataFile.Truncate(end); err != nil {
			return fmt.Errorf("truncate error: %w", err)
		}
	}

	v.writeOffset = end

	for _, offset := range pending {
		if _, err := v.dataFile.WriteAt([]byte{DeleteByte}, offset+24); err != nil {
			return fmt.Errorf("write error: %w", err)
		}
	}

	return nil
}

func (v *Volume) Delete(key KeyPair, cookie uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.header.Sealed() {
		return ErrVolumeSealed
	}

	meta, ok := v.index[key]

	if !ok {
		return nil
	}

	offset, err := utils.CIU[int64, uint64](meta.Offset)
	if err != nil {
		return err
	}

	data := binary.BigEndian.AppendUint64(nil, offset)
	delNeedle := NewNeedle(key.Key, key.AltKey, cookie, data, TombstoneByte)

	buf, err := delNeedle.Bytes(v.bufferPool)
	if err != nil {
		return fmt.Errorf("delete error: %w", err)
	}

	defer v.bufferPool.Put(buf)

	n, err := v.dataFile.WriteAt(buf.B, v.writeOffset)
	if err != nil {
		return fmt.Errorf("write error: %v", err)
	}

	if n != len(buf.B) {
		return fmt.Errorf("write error: %v", io.ErrShortWrite)
	}

	v.writeOffset += int64(n)

	if _, err := v.dataFile.WriteAt([]byte{DeleteByte}, meta.Offset+24); err != nil {
		return fmt.Errorf("write error: %v", err)
	}

	delete(v.index, key)
	v.deadBytes.Add(uint64(onDiskSize(meta.Size)) + uint64(onDiskSize(8)))

	return nil
}

func (v *Volume) scan(fn func(off int64, h NeedleHeader, data []byte) error) (end int64, err error) {
	info, err := v.dataFile.Stat()
	if err != nil {
		return 0, fmt.Errorf("get file state error: %w", err)
	}

	size := info.Size()
	section := io.NewSectionReader(v.dataFile, NeedleStartOffset, size-NeedleStartOffset)
	r := bufio.NewReaderSize(section, 1<<20)
	offset := int64(NeedleStartOffset)
	buf := make([]byte, largeSize)

	for {
		if _, err := io.ReadFull(r, buf[:NeedleHeaderSize]); err != nil {
			switch {
			case err == io.EOF, errors.Is(err, io.ErrUnexpectedEOF):
				return offset, nil
			default:
				return offset, fmt.Errorf("read header error: %w", err)
			}
		}

		if binary.BigEndian.Uint32(buf[:4]) != MagicHeader {
			return offset, nil
		}

		if flag := buf[24]; flag != NormalByte && flag != DeleteByte && flag != TombstoneByte {
			return offset, nil
		}

		if binary.BigEndian.Uint32(buf[25:29]) > maxPayload {
			return offset, nil
		}

		total, err := utils.CUI[uint32, int](onDiskSize(binary.BigEndian.Uint32(buf[25:29])))
		if err != nil {
			return offset, err
		}

		if cap(buf) < total {
			buf = append(buf[:NeedleHeaderSize], make([]byte, total-NeedleHeaderSize)...)
		}
		buf = buf[:total]

		if _, err := io.ReadFull(r, buf[NeedleHeaderSize:]); err != nil {
			switch {
			case err == io.EOF, errors.Is(err, io.ErrUnexpectedEOF):
				return offset, nil
			default:
				return offset, fmt.Errorf("read data error: %w", err)
			}
		}

		n := ParseNeedle(buf)
		dataEnd := NeedleHeaderSize + n.Header.Size

		if _, err := GetNeedleBlockInfo(dataEnd+NeedleFooterSize, n.Header.Size, buf); err != nil {
			return offset, nil
		}

		for _, b := range buf[dataEnd+NeedleFooterSize:] {
			if b != 0 {
				return offset, nil
			}
		}

		if err := fn(offset, n.Header, n.Data); err != nil {
			return offset, err
		}

		offset += int64(total)
	}
}

func onDiskSize(payload uint32) uint32 {
	return (NeedleHeaderSize + payload + NeedleFooterSize + 7) &^ 7
}
