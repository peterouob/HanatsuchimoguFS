package utils

import (
	"errors"
	"math"
)

var (
	ErrOverflow     = errors.New("convert overflow")
	ErrNoneNegative = errors.New("convert none negative")
)

type NNumber interface {
	int | int8 | int16 | int32 | int64
}

func CIU32[T NNumber](i T) (uint32, error) {
	if int64(i) > int64(math.MaxUint32) {
		return 0, ErrOverflow
	}

	if i < 0 {
		return 0, ErrNoneNegative
	}

	return uint32(i), nil
}
