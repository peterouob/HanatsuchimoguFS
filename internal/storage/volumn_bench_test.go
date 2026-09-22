package storage

import (
	"context"
	"fmt"
	rand2 "math/rand/v2"
	"os"
	"path/filepath"
	"runtime/trace"
	"sync/atomic"
	"testing"
)

const (
	BenchPayloadSize = 4096
	BenchFileCount   = 10000
)

var benchModes = []struct {
	name string
	sync bool
}{
	{"Cached", false},
	{"Durable", true},
}

func writeFile(path string, data []byte, sync bool) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}

	if sync {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
	}

	return f.Close()
}

func BenchmarkHaystack_Write(b *testing.B) {
	ctx, task := trace.NewTask(context.Background(), "TASK_Haystack_Write")
	defer task.End()

	for _, mode := range benchModes {
		b.Run(mode.name+"/Serial", func(b *testing.B) {
			region := trace.StartRegion(ctx, "REGION_Haystack_Write_"+mode.name+"_Serial")
			defer region.End()

			v, _ := setupTestVolume(b)
			needle := newRandomNeedle(1, BenchPayloadSize)

			v.index = make(map[KeyPair]NeedleMeta, b.N)

			b.SetBytes(BenchPayloadSize)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				needle.Header.Key = uint64(i)

				if err := v.Write(needle); err != nil {
					b.Fatal(err)
				}

				if mode.sync {
					if err := v.Sync(); err != nil {
						b.Fatal(err)
					}
				}
			}
		})

		b.Run(mode.name+"/Parallel", func(b *testing.B) {
			region := trace.StartRegion(ctx, "REGION_Haystack_Write_"+mode.name+"_Parallel")
			defer region.End()

			v, _ := setupTestVolume(b)
			v.index = make(map[KeyPair]NeedleMeta, b.N)

			var nextKey atomic.Uint64

			b.SetBytes(BenchPayloadSize)
			b.ReportAllocs()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				needle := newRandomNeedle(0, BenchPayloadSize)

				for pb.Next() {
					needle.Header.Key = nextKey.Add(1)

					if err := v.Write(needle); err != nil {
						b.Fatal(err)
					}

					if mode.sync {
						if err := v.Sync(); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		})
	}
}

func BenchmarkOS_Write(b *testing.B) {
	ctx, task := trace.NewTask(context.Background(), "TASK_OS_Write")
	defer task.End()

	payload := make([]byte, BenchPayloadSize)
	payload[0] = 1
	payload[BenchPayloadSize-1] = 255

	for _, mode := range benchModes {
		b.Run(mode.name+"/Serial", func(b *testing.B) {
			region := trace.StartRegion(ctx, "REGION_OS_Write_"+mode.name+"_Serial")
			defer region.End()

			tmpDir := b.TempDir()

			b.SetBytes(BenchPayloadSize)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				path := filepath.Join(tmpDir, fmt.Sprintf("%d.dat", i))

				if err := writeFile(path, payload, mode.sync); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(mode.name+"/Parallel", func(b *testing.B) {
			region := trace.StartRegion(ctx, "REGION_OS_Write_"+mode.name+"_Parallel")
			defer region.End()

			tmpDir := b.TempDir()

			var counter atomic.Uint64

			b.SetBytes(BenchPayloadSize)
			b.ReportAllocs()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					path := filepath.Join(tmpDir, fmt.Sprintf("%d.dat", counter.Add(1)))

					if err := writeFile(path, payload, mode.sync); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

type benchItem struct {
	key    uint64
	cookie uint64
}

func setupHaystackRead(b *testing.B) (*Volume, []benchItem) {
	b.Helper()

	v, _ := setupTestVolume(b)
	v.index = make(map[KeyPair]NeedleMeta, BenchFileCount)

	items := make([]benchItem, BenchFileCount)

	for i := range items {
		items[i] = benchItem{key: uint64(i), cookie: uint64(i * 10)}

		n := newRandomNeedle(items[i].key, BenchPayloadSize)
		n.Header.Cookie = items[i].cookie

		if err := v.Write(n); err != nil {
			b.Fatal(err)
		}
	}

	if err := v.Sync(); err != nil {
		b.Fatal(err)
	}

	for _, it := range items {
		if _, err := v.Read(KeyPair{Key: it.key}, it.cookie); err != nil {
			b.Fatal(err)
		}
	}

	return v, items
}

func BenchmarkHaystack_Read(b *testing.B) {
	ctx, task := trace.NewTask(context.Background(), "TASK_Haystack_Read")
	defer task.End()

	v, items := setupHaystackRead(b)

	b.Run("Serial", func(b *testing.B) {
		region := trace.StartRegion(ctx, "REGION_Haystack_Read_Serial")
		defer region.End()

		rng := rand2.New(rand2.NewPCG(1, 2))

		b.SetBytes(BenchPayloadSize)
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			target := items[rng.IntN(BenchFileCount)]

			if _, err := v.Read(KeyPair{Key: target.key}, target.cookie); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Parallel", func(b *testing.B) {
		region := trace.StartRegion(ctx, "REGION_Haystack_Read_Parallel")
		defer region.End()

		b.SetBytes(BenchPayloadSize)
		b.ReportAllocs()
		b.ResetTimer()

		b.RunParallel(func(pb *testing.PB) {
			rng := rand2.New(rand2.NewPCG(1, 2))

			for pb.Next() {
				target := items[rng.IntN(BenchFileCount)]

				if _, err := v.Read(KeyPair{Key: target.key}, target.cookie); err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}

func setupOSRead(b *testing.B) []string {
	b.Helper()

	tmpDir := b.TempDir()
	payload := make([]byte, BenchPayloadSize)
	paths := make([]string, BenchFileCount)

	for i := range paths {
		paths[i] = filepath.Join(tmpDir, fmt.Sprintf("%d.dat", i))

		if err := writeFile(paths[i], payload, true); err != nil {
			b.Fatal(err)
		}
	}

	for _, path := range paths {
		if _, err := os.ReadFile(path); err != nil {
			b.Fatal(err)
		}
	}

	return paths
}

func BenchmarkOS_Read(b *testing.B) {
	ctx, task := trace.NewTask(context.Background(), "TASK_OS_Read")
	defer task.End()

	paths := setupOSRead(b)

	b.Run("Serial", func(b *testing.B) {
		region := trace.StartRegion(ctx, "REGION_OS_Read_Serial")
		defer region.End()

		rng := rand2.New(rand2.NewPCG(1, 2))

		b.SetBytes(BenchPayloadSize)
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			if _, err := os.ReadFile(paths[rng.IntN(BenchFileCount)]); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Parallel", func(b *testing.B) {
		region := trace.StartRegion(ctx, "REGION_OS_Read_Parallel")
		defer region.End()

		b.SetBytes(BenchPayloadSize)
		b.ReportAllocs()
		b.ResetTimer()

		b.RunParallel(func(pb *testing.PB) {
			rng := rand2.New(rand2.NewPCG(1, 2))

			for pb.Next() {
				if _, err := os.ReadFile(paths[rng.IntN(BenchFileCount)]); err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}
