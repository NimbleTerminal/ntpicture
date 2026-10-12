# Notices

Source code is MIT licensed; see LICENSE. The simple and grid examples are
adapted from NTCharts, Copyright (c) 2024–2026 Neomantra Corp.

`examples/simple/Fuji-01.png` is copied unchanged from NTCharts. It is by
hypertalking, from
https://www.hypertalking.com/2023/05/08/1-bit-pixel-art-of-hokusais-the-great-wave-off-kanagawa/
and distributed under Creative Commons Attribution-NonCommercial-NoDerivatives
4.0 International: https://creativecommons.org/licenses/by-nc-nd/4.0/.
The artwork is embedded in the simple demo binary, including its WASM build.

The generated demo site includes booba and ghostty-web runtime assets.
Go dependency and browser runtime license texts are collected in
THIRD_PARTY_NOTICES.md, generated with `task notices` and copied into the site.
The browser licenses in `web/licenses/` were copied from go-booba's local
vendored ghostty-web, Ghostty, and pako sources; review them when updating the
pinned booba runtime. `wasm_exec.js` retains its Go BSD license header.

`examples/animation/motion.gif` and `motion.webp` are original orbit fixtures
copied unchanged from Gloss (Neomantra Corp, MIT). Their generator is
Gloss's `scripts/gen-assets/gif.go`; its `task gen-assets` builds the GIF and
converts it to animated WebP with `gif2webp`. No external artwork is used in
these two fixtures. Animation decoder/player source and baseline tests are
also adapted from Gloss under the same MIT license.
