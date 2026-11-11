// Exercise the eventloop demo, which has no Bubble Tea program, through the
// same JS bridge the page uses. With "kitty" this script answers the probe as
// a Kitty-capable terminal would; without it nothing answers, as under Node.
// This checks detection/input/resize/exit, not browser canvas rendering.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setTimeout as delay } from 'node:timers/promises';
const kitty = process.argv.includes('kitty');
vm.runInThisContext(await readFile('web/wasm_exec.js', 'utf8'));
const go = new Go();
let exitCode;
go.exit = code => { exitCode = code; };
const { instance } = await WebAssembly.instantiate(await readFile('web/demos/eventloop.wasm'), go.importObject);
const timeout = setTimeout(() => { console.error('eventloop: timed out'); process.exit(1); }, 15000);
const running = go.run(instance);
// Collect output until it satisfies done, or about five seconds pass.
async function until(done) {
  let output = '';
  for (let n = 0; n < 200 && !done(output); n++) {
    await delay(25);
    output += bubbletea_read();
  }
  return output;
}
try {
  for (let n = 0; typeof globalThis.bubbletea_read !== 'function' && n < 100; n++) await delay(20);
  assert.equal(typeof globalThis.bubbletea_read, 'function', 'terminal bridge installed');
  bubbletea_resize(80, 28);
  let output = await until(text => text.includes('[c'));
  const query = output.match(/a=q[^;]*i=(\d+)/);
  assert.ok(query && output.includes('[16t') && output.includes('[c'), 'probe query written');
  if (kitty) {
    // A key typed before the reply, and the reply split mid-sequence.
    const reply = `\x1b_Gi=${query[1]};OK\x1b\\\x1b[6;20;10t\x1b[?62;4c`;
    bubbletea_write('m' + reply.slice(0, 7));
    await delay(30);
    bubbletea_write(reply.slice(7));
  }
  output = await until(text => text.includes('q: quit'));
  assert.ok(output.includes('q: quit') && output.includes('▄'), 'first view is glyphs');
  assert.ok(!output.includes('\x1b_G'), 'no Kitty output yet');
  if (kitty) {
    assert.ok(output.includes('glyphs |'), 'early key switched to glyphs');
    bubbletea_write('m');
    output = await until(text => text.includes('q: quit'));
    assert.ok(output.includes('a=T') && output.includes('Kitty graphics |'), 'Kitty image after switching back');
  } else {
    bubbletea_write('m');
    output = await until(text => text.includes('q: quit'));
    assert.ok(!output.includes('\x1b_G'), 'unanswered probe never yields Kitty output');
  }
  bubbletea_resize(44, 18);
  output = await until(text => text.includes('q: quit'));
  assert.ok(output.includes('q: quit'), 'resize redraws');
  if (kitty) assert.ok(output.includes('a=d'), 'redraw deletes the previous image');
  bubbletea_write('q');
  await running;
  assert.equal(exitCode, 0);
  assert.equal(globalThis.bubbletea_exited, true, 'exit published to adapter');
  const final = bubbletea_final;
  assert.ok(final.includes('\x1b[?1049l'), 'screen restored on exit');
  if (kitty) assert.ok(final.includes('a=d'), 'image deleted on exit');
  console.log(`eventloop${kitty ? ' (kitty)' : ''}: probe, input, resize, and clean exit passed`);
} finally {
  clearTimeout(timeout);
}
process.exit(0);
