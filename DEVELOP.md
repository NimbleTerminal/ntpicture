# Development

Use Go 1.27.2 for development and CI (the module minimum is Go 1.26.8). Native dependencies use upstream Bubble Tea; `wasm.work`
pins the existing browser fork. Generated native binaries belong in `bin/`, WASM output in `web/demos/`, and
installed QA tools in `.task/bin/`; all are ignored. Do not commit browser build output.

```sh
task ci                  # same gate as GitHub Actions; includes network scans
task test                # native tests
task test-no-cgo          # direct transport fallback
task build-ex-still       # bin/ntpicture-still
task run-still
task build-wasm test-wasm # web/demos/*.wasm and tests under Node
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
replacements in the root go.mod. The separate examples module uses a local
replacement for this checkout so it can exercise unreleased library changes. Run each consumer's documented full CI before its actual
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
release packaging. The Pages workflow separately builds and deploys the demo
site after full QA. Do not commit generated WASM or copied browser assets.

Before the first release, configure the `nimbleterminal.dev/ntpicture`
`go-import` metadata to resolve to `github.com/NimbleTerminal/ntpicture`.
Confirm module discovery from a fresh consumer without workspace or local
replacements. Update CHANGELOG.md, run `task ci`, the local consumer checks,
and the manual acceptance checks above. Review dependency licenses and retain
upstream copyright notices when distributing copied source or browser assets.
Then commit the release preparation, tag the intended version, wait for tag CI
to pass, and publish the GitHub release with its changelog. Do not advertise
animation/playback packages until they are implemented and validated.


## Browser demo site

`task build-examples` builds still, simple, and grid into `bin/`. To run one
without building all three, use `GOWORK=off go -C examples run ./grid` (or
`./simple`, `./still`). These examples use booba.Run for both native and browser
execution and `Picture` for ownership, command routing, and cleanup.

`task build-wasm-site` compiles all three into `web/demos/`, exports the booba
and ghostty-web runtime using the pinned `go tool booba-assets` in the examples
module, and copies license notices. The exporter uses the active toolchain's
wasm_exec.js. Source HTML/CSS/JS and browser license texts are tracked; generated
runtime assets and binaries are ignored and removed by `task clean`.

`task serve-wasm-site` serves the repository at http://localhost:8000/web/.
All site URLs are relative so the same artifact works under GitHub Pages'
repository prefix. Example navigation reloads the page, giving each Go WASM
program and image registry a fresh lifetime. `?demo=still`, `?demo=simple`, and
`?demo=grid` select the example; unknown names select still.

CI validates the site files and WASM binaries, then starts each real demo under
Node, exercises its booba input/resize bridge, and verifies a clean exit.
These are runtime smoke checks, not a visual browser check. Verify the rendered
site in a real browser for Canvas2D/WebGL, focus, mobile layout, and Kitty/glyph
toggling before release. The local sandbox blocks listening sockets, so a
browser preview could not be verified during this port.

The Pages workflow runs `task ci` on the exact revision before uploading `web/`.
Configure the repository's Pages source to **GitHub Actions**. A push to main or
a manual workflow run then deploys the site; this change does not itself publish.

Run `task notices` after dependency updates, then review and commit
THIRD_PARTY_NOTICES.md. `task notices-check` enforces that generated text in CI.
The generator includes both upstream/native and forked/WASM dependencies,
the Go runtime, and the vendored browser runtime license texts. Review
`web/licenses/` when updating booba; its module does not ship those license
files alongside the exported ghostty-web assets.


The examples publish help through booba's `bubblehelp.NewModel` wrapper. Shared
key bindings drive input handling, native help, and OSC 7501 action snapshots.
The browser opts in with `allowOSC7501Actions` and uses `installActionBar`;
buttons send normal encoded key events, with the first alias preferred. The
grid's rendering toggle is advertised only after Kitty support is confirmed.
Smoke tests acknowledge the status query, decode the real action snapshot, and
quit via its advertised gesture. Browser click/touch verification remains part
of manual acceptance.

Dependency refresh: the fork revision `v2.0.0-20261010153813-5097f2f73229`
returned by `@latest` fails to compile for js/wasm (missing platform methods).
Keep `c397c32a5074` until a newer WASM-capable revision passes the browser checks.
