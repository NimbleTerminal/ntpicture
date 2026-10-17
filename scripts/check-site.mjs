// Validate the generated Pages artifact without requiring a local HTTP server.
import assert from 'node:assert/strict';
import { readFile, access } from 'node:fs/promises';
for (const file of [
  'index.html', 'site.css', 'site.js', 'wasm_exec.js', 'booba/booba.js',
  'ghostty-web/ghostty-web.js', 'ghostty-web/ghostty-vt.wasm',
  'LICENSE', 'NOTICE.md', 'THIRD_PARTY_NOTICES.md',
]) await access(`web/${file}`);
for (const demo of ['still', 'simple', 'grid', 'animation', 'framebuffer']) {
  const bytes = await readFile(`web/demos/${demo}.wasm`);
  assert.deepEqual([...bytes.subarray(0, 4)], [0, 97, 115, 109], `${demo}: WASM header`);
  assert.ok(WebAssembly.validate(bytes), `${demo}: valid WASM`);
}
const html = await readFile('web/index.html', 'utf8');
assert.ok(html.includes('id="source"'), 'demo source link present');
for (const [, path] of html.matchAll(/(?:src|href)="([^"?#]+)"/g)) {
  if (/^(https?:|\.\/)/.test(path)) continue;
  await access(`web/${path}`);
}
for (const examplePath of [
  'examples/still', 'examples/simple', 'examples/grid',
  'examples/animation', 'examples/animation/composite.go',
  'examples/animation/scroll.go', 'examples/framebuffer',
]) await access(examplePath);
console.log('Site links, runtime assets, and five WASM binaries checked.');
