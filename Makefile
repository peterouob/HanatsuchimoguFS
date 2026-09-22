.PHONY: test race cover bench bench-cold bench-compare align-show align-fix gosec

test:
	go test ./...

race:
	go test -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

bench:
	go test -run '^$$' -bench . -benchmem -benchtime=3s -count=10 -timeout=30m ./... | tee bench.txt
	benchstat bench.txt

bench-cold:
	sync
	if [ "$$(uname)" = "Darwin" ]; then sudo purge; else echo 3 | sudo tee /proc/sys/vm/drop_caches >/dev/null; fi
	go test -run '^$$' -bench . -benchmem -benchtime=3s -count=10 -timeout=30m ./... | tee bench-cold.txt
	benchstat bench-cold.txt

bench-compare:
	benchstat $(BASE) bench.txt

align-show:
	fieldalignment ./...
align-fix:
	fieldalignment -fix ./...
gosec:
	gosec -fmt=html -out=results.html ./...
