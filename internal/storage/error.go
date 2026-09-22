package storage

import "errors"

var (
	ErrNotFound                 = errors.New("error for file not found")
	ErrMagicNumber              = errors.New("error for file magic number")
	ErrCookie                   = errors.New("error for file cookie")
	ErrDataDeleted              = errors.New("error for file data is deleted")
	ErrCrcNotValid              = errors.New("error for file crc not valid")
	ErrInvalidMagicFooter       = errors.New("error for file magic footer")
	ErrBufferTooSmall           = errors.New("error for file buffer too small")
	ErrInvalidNeedle            = errors.New("invalid needle")
	ErrToLarge                  = errors.New("too large")
	ErrInvalidSuperblock        = errors.New("error for invalid superblock")
	ErrVolumeSealed             = errors.New("error for volume is sealed")
	ErrUnsupportedFormatVersion = errors.New("error for unsupported format version")
)
