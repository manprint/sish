# Perf / regression baseline — branch perf-bugfix-deps-202605

Captured before Fase 1/2/3 changes. Machine: linux/amd64, go1.26.2, 16 logical CPUs.

## Correctness baseline
- `go build ./...` — OK
- `go test ./... -count=1` — PASS (httpmuxer, sshmuxer, utils)
- `go test -race ./... -count=1` — PASS (existing tests do not exercise the B4 racy path; targeted tests added in Fase 1)
- `go vet ./...` — only pre-existing warning B7 (`requests_forceconnect_test.go:37,38` t.Fatalf in goroutine)

## Bandwidth microbench — read-wrapper chain, 16 MiB/op, default 32KB io.Copy buffer
(`BenchmarkReadChain_*`, -benchtime=2s -count=3, CPU-bound, isolates copy loop)

| variant                         | ns/op (~) | B/op  | allocs/op |
|---------------------------------|-----------|-------|-----------|
| Bare (no wrapping)              | 17000     | 32792 | 2         |
| Counting only                  | 21300     | 32824 | 3         |
| Static profile (nil limiter)   | 22500     | 32856 | 4         |
| Dynamic getter (RLock per read)| 51700     | 32888 | 6         |

Key finding: the **dynamic getter path (production tunnel, requests.go:525) is ~3x the bare cost**;
RLock + closure resolution per 32KB chunk roughly doubles per-chunk cost vs the static profile.
Targets: O2 (cache profile, drop per-read RLock) and O1 (larger pooled buffer → fewer chunks).

## Bandwidth end-to-end — CopyBoth over loopback TCP, 64KB chunks
(`BenchmarkCopyBoth_*`)

| variant         | MB/s (~) |
|-----------------|----------|
| NoProfile       | 1815     |
| StaticProfile   | 1820     |
| DynamicGetter   | 1822     |

Loopback is network/syscall-bound (~1.8 GB/s) so it masks the CPU copy-loop cost; the microbench
above is the authoritative signal for O1/O2. Real deployments (TLS, many conns, slower per-core)
sit closer to the microbench profile.

## AFTER Fase 2 (O1 pooled 256KB CopyBuffer + O2 lock-free profile load)

Microbench (read-wrapper chain, 16 MiB/op, 32KB buffer — isolates O2):

| variant                         | before ns/op | after ns/op | change   |
|---------------------------------|--------------|-------------|----------|
| Bare                            | 17000        | ~16000      | ~same    |
| Counting                        | 21300        | ~23000      | ~same    |
| Static profile (nil limiter)    | 22500        | ~23000      | ~same    |
| Dynamic getter (was RLock/read) | 51700        | ~24300      | **~2.1x faster** |

The dynamic (production tunnel) path dropped from ~3x the bare cost to parity
with the static path: the per-chunk RWMutex.RLock was the dominant cost and is
now a lock-free atomic load.

CopyBoth over loopback TCP (end-to-end, captures O1+O2). NOTE: loopback is
syscall/scheduler-bound and very noisy at high GOMAXPROCS; measured stable at
GOMAXPROCS=4, -benchtime=3s -count=6 (median MB/s):

| variant         | baseline (~) | after (~) | note                          |
|-----------------|--------------|-----------|-------------------------------|
| NoProfile       | 1820         | ~3000     | raw conns → splice via CopyBuffer |
| StaticProfile   | 1820         | ~2000     | wrapped; 256KB buffer, fewer syscalls |
| DynamicGetter   | 1820         | ~1950     | now ≈ static (RLock removed)   |

No regression. Also folded in: closeBoth no longer logs the benign
net.ErrClosed from the second (expected) close on every teardown.

## Re-run commands
```
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go test ./utils/ -run '^$' -bench 'BenchmarkCopyBoth|BenchmarkReadChain' -benchmem -benchtime=2s -count=3 2>/dev/null | grep -E 'op|PASS|ok'
```
