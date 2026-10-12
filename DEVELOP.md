# Development

Use Go 1.27.2 for development and CI (the module minimum is Go 1.26.8). Native dependencies use upstream Bubble Tea; `wasm.work`
pins the existing browser fork. Generated native binaries belong in `bin/`, WASM output in `dist/`, and
installed QA tools in `.task/bin/`; all are ignored. Do not commit browser build output.

```sh
task ci                  # same gate as GitHub Actions; includes network scans
task test                # native tests
task test-no-cgo          # direct transport fallback
task build-ex-still       # bin/ntpicture-still
task run-still
task build-wasm test-wasm # dist/still.wasm and tests under Node
task consumers           # optional; requires local sibling repositories
task clean               # remove generated artifacts and installed QA tools
```

Install [Go Task](https://taskfile.dev) and Node (CI uses Node 24), plus a C
compiler for native shared memory and the race detector. `task --list-all`
lists maintenance tasks. CI runs on Linux and macOS for their distinct shared
memory implementations and cross-compiles Windows. Native tasks explicitly
use `GOWORK=off`; browser tasks select `wasm.work`.

`task vulncheck` installs the same pinned govulncheck version as ntqrcode,
then scans native tests and the WASM dependency graph. It requires network
access to the Go vulnerability database; failures stop CI. Dependency updates
are proposed weekly for Go modules and Actions. Review the browser fork pin
and `wasm.work.sum` alongside module updates; do not assume a Go module bot
updates workspace replacements.

Node is needed for the WASM tests. These test the Go producer and browser
registry lifecycle, not visual output in a browser. Native POSIX shared-memory
tests need an environment allowing `shm_open`; they intentionally fail when
that facility is blocked. The real-terminal consumption test additionally
requires `NTCHARTS_SHM_TERMINAL_TEST=1` inside a local Kitty-capable terminal.

For restricted environments, `CGO_ENABLED=0` on macOS tests the direct fallback.
A race run may explicitly exclude OS-blocked shared-memory tests; report those
exclusions and do not describe it as full native transport validation.

## Consumer compatibility checks

```sh
python3 scripts/check-consumers.py --projects /path/to/projects
```

This copies current tracked/unignored files from NTCharts, NTCharts3d, ntqrcode
and Gloss into a temporary directory, replaces picture with the alias fixture,
and tests heatpicture, chartpicture, QR, NTCharts3d, Gloss animation/app and URL
adapters. Private picture implementation tests live here after extraction.
`--restricted` compiles the URL adapter tests without running their HTTP servers
when the sandbox prohibits listening sockets. Consumer changes made in the
copies are discarded; no dependency pin in a real checkout is changed.

Keep `GOWORK=off` for native/release dependency checks. Do not put local sibling
replacements in go.mod. Run each consumer's documented full CI before its actual
migration, including its separate example/browser modules and generated notice
or documentation checks.

## Bundled browser protocol check

```sh
task browser-check BOOBA_DIR=/path/to/go-booba
```

This writes ignored `dist/browser-frames.json` and checks Go-generated PNG/RGBA/zlib payloads and registry consumption against
booba's actual bundled JavaScript and WASM. It is a protocol/pixel check under
Node, not a visual Canvas2D/WebGL test or a terminal acknowledgement check.


## Manual acceptance checks

Automated protocol checks do not establish visual terminal behavior. Before a
release or transport change, record the OS, terminal/browser version, command,
and result for these checks:

- Native Kitty and tmux: show a still, repeatedly replace it, resize, and quit;
  verify retained content during transmission and removal of old placements.
- Glyph fallback: repeat replacement and resize in a terminal without Kitty.
- Shared memory: test native and browser paths, direct fallback when unavailable,
  delayed consumption, and cleanup after cancellation or shutdown.
- Browser host: check the actual booba/ghostty-web Canvas2D/WebGL output,
  registry consumption, resizing, and teardown with multiple pictures.
- Lifecycle: exercise rapid updates and stale messages while closing/reopening
  pictures; ensure messages route only to their owning instance.

Unit tests cover stale results, geometry, encoding, and cleanup. Manual results
are not yet recorded for this extracted module. Animation acceptance will be
added with the playback migration.

## Releases

This is a Go library: CI runs for branches, pull requests, and tags. Like the
small companion libraries, release publication is manual. There is no binary
packaging or Pages deployment yet; `dist/still.wasm` is a build check, not a
standalone website. Do not commit it or copied browser assets.

Before the first release, configure the `nimbleterminal.dev/ntpicture`
`go-import` metadata to resolve to `github.com/NimbleTerminal/ntpicture`.
Confirm module discovery from a fresh consumer without workspace or local
replacements. Update CHANGELOG.md, run `task ci`, the local consumer checks,
and the manual acceptance checks above. Review dependency licenses and retain
upstream copyright notices when distributing copied source or browser assets.
Then commit the release preparation, tag the intended version, wait for tag CI
to pass, and publish the GitHub release with its changelog. Do not advertise
animation/playback packages until they are implemented and validated.
