// Exercise the actual booba bundle/WASM against Go-generated Kitty payloads.
// Usage: node scripts/check-browser.mjs /path/to/go-booba /path/to/frames.json
import {readFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';

const [booba, framesPath] = process.argv.slice(2);
if (!booba || !framesPath) throw new Error('usage: check-browser.mjs BOOBA FRAMES_JSON');
const assets = resolve(booba, 'serve/static/ghostty-web');
const {Ghostty} = await import(pathToFileURL(resolve(assets, 'ghostty-web.js')).href);
const wasm = await readFile(resolve(assets, 'ghostty-vt.wasm'));
const ghostty = await Ghostty.load(`data:application/wasm;base64,${wasm.toString('base64')}`);
const term = ghostty.createTerminal(80, 24);
const name = '/ntpicture-contract';
try {
  const frames = JSON.parse(await readFile(framesPath, 'utf8'));
  assert.equal(frames.length, 3);
  for (const frame of frames) {
    term.write(frame.APC);
    const pixels = term.getKittyImagePixels(term.getKittyGraphics(), frame.ID);
    assert.equal(pixels.width, 2);
    assert.equal(pixels.height, 1);
    // Zlib uses RGB for opaque input; PNG/raw RGBA may retain alpha.
    const expected = pixels.data.length === 6
      ? [255, 0, 0, 0, 0, 255]
      : [255, 0, 0, 255, 0, 0, 255, 255];
    assert.deepEqual(Array.from(pixels.data), expected);
  }
  const registry = globalThis.ghosttyKittySharedMemory;
  assert.ok(registry instanceof Map);
  registry.set(name, new Uint8Array([255, 0, 0, 255]));
  term.write(`\x1b_Ga=T,t=s,f=32,s=1,v=1,S=4,i=104,c=1,r=1,q=2;${btoa(name)}\x1b\\`);
  assert.equal(registry.has(name), false);
  assert.deepEqual(
    Array.from(term.getKittyImagePixels(term.getKittyGraphics(), 104).data),
    [255, 0, 0, 255],
  );
  console.log('Actual booba bundle: Go PNG/RGBA/zlib payloads and shared-memory registry passed.');
} finally {
  globalThis.ghosttyKittySharedMemory?.delete(name);
  term.free();
}
