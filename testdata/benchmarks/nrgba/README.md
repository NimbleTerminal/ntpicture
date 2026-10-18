# NRGBA RGB packing measurements

2026-10-18, macOS darwin/arm64, Apple M5 Max, Go 1.27.2.
Baseline: ntpicture `576fc07628a4a8940b548b8b32de51ea651cea16`.
After: same source plus the NRGBA branch in `kittyRGBBytes`. Dependencies and
benchmark fixtures are identical within each pair. Production pins are unchanged.
`sources.json` records source hashes, sibling revisions and working-tree status.
The consumer pair uses fresh copies of local nt3d and ntgpuimage and the archived
ntcharts3d review copy; it is a new matched pair, not a reconstruction of the
historical published-v0.1.0 measurements. The existing MIGRATION.md edit is unrelated.

Each row reports the median (minimum–maximum) of three sequential samples.
The recorded packing pair ran after CI completed, with no other task benchmark
running concurrently. Fixtures are constructed outside the timed loop. Packing
uses 200ms samples; GPU presentation uses 144 operations per sample, after warmup.
Profiles run separately and include initialization.

## Packing

| Fixture | Before µs/op | After µs/op | Before B/op | After B/op | Before allocs/op | After allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 320x160/RGBA | 45.013 (40.199–48.632) | 49.368 (49.252–49.440) | 155,650 (155,650–155,653) | 155,649 (155,649–155,649) | 1 (1–1) | 1 (1–1) |
| 320x160/NRGBA | 452.685 (444.932–484.430) | 46.601 (45.991–47.690) | 360,453 (360,452–360,461) | 155,650 (155,650–155,651) | 51,201 (51,201–51,201) | 1 (1–1) |
| 320x160/NRGBASubimage | 434.725 (433.018–437.854) | 48.731 (45.115–50.671) | 360,452 (360,451–360,453) | 155,651 (155,650–155,653) | 51,201 (51,201–51,201) | 1 (1–1) |
| 320x160/Generic | 796.657 (794.062–802.374) | 858.374 (841.050–867.877) | 360,452 (360,452–360,453) | 360,450 (360,450–360,451) | 51,201 (51,201–51,201) | 51,201 (51,201–51,201) |
| 640x320/RGBA | 145.438 (145.270–148.911) | 157.918 (156.242–168.938) | 614,401 (614,400–614,401) | 614,401 (614,400–614,404) | 1 (1–1) | 1 (1–1) |
| 640x320/NRGBA | 1,734.370 (1,731.213–1,735.065) | 161.311 (161.243–166.947) | 1,433,607 (1,433,605–1,433,609) | 614,401 (614,400–614,401) | 204,801 (204,801–204,801) | 1 (1–1) |
| 640x320/NRGBASubimage | 1,732.085 (1,728.002–1,732.461) | 162.119 (159.856–164.664) | 1,433,608 (1,433,607–1,433,611) | 614,400 (614,400–614,401) | 204,801 (204,801–204,801) | 1 (1–1) |
| 640x320/Generic | 3,179.486 (3,163.716–3,206.867) | 3,464.194 (3,343.882–3,506.596) | 1,433,606 (1,433,605–1,433,609) | 1,433,607 (1,433,604–1,433,607) | 204,801 (204,801–204,801) | 204,801 (204,801–204,801) |
| 1280x640/RGBA | 684.909 (648.962–764.843) | 570.855 (570.508–573.801) | 2,457,604 (2,457,603–2,457,609) | 2,457,600 (2,457,600–2,457,601) | 1 (1–1) | 1 (1–1) |
| 1280x640/NRGBA | 7,160.771 (7,120.148–7,200.158) | 597.531 (576.062–598.132) | 5,734,419 (5,734,409–5,734,426) | 2,457,600 (2,457,600–2,457,602) | 819,201 (819,201–819,201) | 1 (1–1) |
| 1280x640/NRGBASubimage | 7,891.969 (7,092.158–8,363.125) | 605.683 (594.564–612.738) | 5,734,433 (5,734,408–5,734,466) | 2,457,600 (2,457,600–2,457,600) | 819,201 (819,201–819,201) | 1 (1–1) |
| 1280x640/Generic | 13,223.516 (13,185.263–13,626.750) | 13,066.232 (13,025.618–13,690.350) | 5,734,407 (5,734,404–5,734,419) | 5,734,404 (5,734,404–5,734,411) | 819,201 (819,201–819,201) | 819,201 (819,201–819,201) |

NRGBA and its strided subimage now allocate only the independently owned output,
regardless of pixel count. Generic fallback allocation counts remain unchanged.
Small/medium RGBA and generic control timings increased by roughly 8–10% in this
pair; large controls improved. These short samples do not isolate the cause of
control variation, and no timing threshold is asserted. The RGBA loop is unchanged.

## GPU presentation

Native WebGPU/Metal, 640×320, anisotropy 4, direct Kitty zlib. The archived harness
asserts GPU selection and raster dimensions; all samples completed 144 direct
frames with no shared-memory frames or coalescing. Camera rotates in 5-degree
steps; texture replacement holds the camera fixed. The harness is archived here
as `presentation_bench_test.go.txt`.

| Workload / metric | Before | After |
| --- | ---: | ---: |
| camera / ms/op | 3.324 (3.252–3.335) | 1.444 (1.425–1.453) |
| camera / render-ms/frame | 0.676 (0.672–0.695) | 0.409 (0.394–0.409) |
| camera / output-ms/frame | 2.594 (2.545–2.621) | 0.997 (0.993–1.009) |
| camera / B/op | 3,738,046 (3,738,034–3,739,204) | 2,918,045 (2,918,013–2,918,912) |
| camera / allocs/op | 206,004 (206,000–206,004) | 1,203 (1,198–1,203) |
| texture / ms/op | 5.158 (4.987–5.233) | 3.261 (3.232–3.324) |
| texture / render-ms/frame | 2.395 (2.299–2.425) | 2.246 (2.244–2.276) |
| texture / output-ms/frame | 2.496 (2.423–2.527) | 0.795 (0.768–0.819) |
| texture / B/op | 7,459,361 (7,459,356–7,461,075) | 6,639,491 (6,639,406–6,641,024) |
| texture / allocs/op | 206,464 (206,464–206,466) | 1,663 (1,662–1,664) |

Rendering-stage timings also changed; stage medians need not sum to total medians.
Do not attribute all total-time improvement to the isolated packing loop.
`PresentedMsg` measures ordered application output completion to a discard sink,
not terminal display acknowledgement. These numbers exclude terminal painting and
real output backpressure and are not visible FPS. Driver, compression, and output
allocations remain.

The separate baseline allocation profile attributes 85.59% of sampled allocation
objects to `image.(*NRGBA).At`. The post-fix profile has no samples at that
function (also checked with a zero-cutoff focused profile). Profiles include
initialization and sampling effects; benchmark rows supply per-operation counts.

## Reproduction

Create disposable copies of the recorded source trees, put the archived harness
inside the ntcharts3d copy, and select those three consumer modules plus ntpicture
in a Go 1.27.2 workspace. For the baseline, use the recorded ntpicture revision
with the new `kitty_rgb_test.go` copied in; for after, use the fixed tree. Do not
change production module files. This run used `/private/tmp/ntpicture-nrgba/` with
`before.work` and `after.work`; both use the same consumer copies, with only the
ntpicture module directory differing. SHA-256 hashes in `sources.json` identify
both versions of kitty.go and all copied consumer Go/WGSL/module files.

Run sequentially in each ntpicture tree:

```sh
GOWORK=off go test -mod=readonly -run '^$' -bench '^BenchmarkKittyRGBPacking$' -benchmem -benchtime=200ms -count=3
```

For each workspace (substitute `before` and then `after`):

```sh
OSM_REVIEW_GPU=1 GOWORK=/private/tmp/ntpicture-nrgba/before.work go test -tags webgpu /private/tmp/ntpicture-nrgba/ntcharts3d -run '^$' -bench '^BenchmarkPresentation$/^direct$/^(camera|texture)$' -benchtime=144x -count=3 -benchmem
OSM_REVIEW_GPU=1 GOWORK=/private/tmp/ntpicture-nrgba/before.work go test -tags webgpu /private/tmp/ntpicture-nrgba/ntcharts3d -run '^$' -bench '^BenchmarkPresentation$/^direct$/^camera$' -benchtime=144x -count=1 -memprofile=/private/tmp/ntpicture-nrgba/before.mem -o /private/tmp/ntpicture-nrgba/before.test
GOWORK=off go tool pprof -top -alloc_objects /private/tmp/ntpicture-nrgba/before.test /private/tmp/ntpicture-nrgba/before.mem
```

## Validation and remaining integration

- `GOWORK=off go test -mod=readonly -run 'TestKitty(RGBPacking|NRGBAEncoding)' .` passed before the implementation change, establishing output compatibility.
- `task ci` passed after the code/test change; full output is in `validation.txt`.
  This includes native race and shared-memory tests, no-CGO fallback, vet/lint,
  Windows compilation, native builds, WASM builds, Node demo lifecycle smoke
  execution, WASM tests under Node, notices and vulnerability checks.
- Exact decoded RGB/RGBA pixels, alpha (including hidden RGB under zero alpha),
  padded and cropped rows, bounds, internal views, generic fallback, and output
  ownership are covered. No transport selection, public API or dependency changed.
- No interactive browser or real-terminal visual check was performed. Node
  execution establishes protocol/lifecycle coverage, not visual behavior.
- A release and consumer dependency update remain separate integration work.
  No commit, push, release or production pin update was performed.
