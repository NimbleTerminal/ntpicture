// Validate the generated Pages artifact without requiring a local HTTP server.
import assert from 'node:assert/strict';
import { readFile, access } from 'node:fs/promises';
for (const file of [
  'index.html', 'site.css', 'site.js', 'highlight.js', 'wasm_exec.js', 'booba/booba.js',
  'ntwebterm/ntwebterm.js', 'ntwebterm/ghostty-vt.wasm',
  'LICENSE', 'NOTICE.md', 'THIRD_PARTY_NOTICES.md',
]) await access(`web/${file}`);
for (const demo of ['still', 'simple', 'grid', 'animation', 'framebuffer', 'eventloop']) {
  const bytes = await readFile(`web/demos/${demo}.wasm`);
  assert.deepEqual([...bytes.subarray(0, 4)], [0, 97, 115, 109], `${demo}: WASM header`);
  assert.ok(WebAssembly.validate(bytes), `${demo}: valid WASM`);
}
assert.equal(await readFile('web/demos/eventloop.go.txt', 'utf8'), await readFile('examples/eventloop/main.go', 'utf8'), 'eventloop: displayed source is current');
// The source panel's highlighter must reproduce the file exactly and classify it.
const { tokenizeGo } = await import('../web/highlight.js');
const shown = await readFile('web/demos/eventloop.go.txt', 'utf8');
const pieces = tokenizeGo(shown);
assert.equal(pieces.map(piece => piece.text).join(''), shown, 'highlighter preserves the source text');
const has = (kind, text) => pieces.some(piece => piece.kind === kind && piece.text === text);
assert.ok(has('keyword', 'func') && has('keyword', 'select') && has('builtin', 'nil'), 'keywords and predeclared names');
assert.ok(has('string', '"\\x1b[?1049h\\x1b[?25l"') && has('number', '0x03'), 'strings and numbers');
assert.ok(pieces.some(piece => piece.kind === 'comment' && piece.text.startsWith('// 1. Write the probe')), 'comments');
assert.ok(!pieces.some(piece => piece.kind && piece.kind !== 'comment' && piece.text === 'Probe'), 'identifiers stay plain');
assert.ok(!has('builtin', 'close') && !has('builtin', 'clear'), 'fields named like builtins stay plain');
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
  'examples/eventloop',
]) await access(examplePath);
console.log('Site links, runtime assets, and six WASM binaries checked.');
