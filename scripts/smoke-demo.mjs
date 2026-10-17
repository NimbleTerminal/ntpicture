// Exercise one real WASM program through booba's JS bridge in a fresh process.
// This checks startup/input/resize/exit, not browser canvas rendering.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setTimeout as delay } from 'node:timers/promises';
const demo = process.argv[2];
assert.ok(['still', 'simple', 'grid', 'animation', 'framebuffer'].includes(demo));
vm.runInThisContext(await readFile('web/wasm_exec.js', 'utf8'));
const go = new Go();
const composite = process.argv.includes('composite');
const scroll = process.argv.includes('scroll');
if (composite) go.argv = ['animation', '--composite'];
if (scroll) go.argv = ['animation', '--scroll'];
let exitCode;
go.exit = code => { exitCode = code; };
const { instance } = await WebAssembly.instantiate(await readFile(`web/demos/${demo}.wasm`), go.importObject);
const timeout = setTimeout(() => { console.error(`${demo}: timed out`); process.exit(1); }, 15000);
const running = go.run(instance);
try {
  for (let n = 0; typeof globalThis.bubbletea_read !== 'function' && n < 100; n++) await delay(20);
  assert.equal(typeof globalThis.bubbletea_read, 'function', 'booba bridge installed');
  bubbletea_resize(80, 28);
  let output = '';
  for (let n = 0; n < 100 && !output.toLowerCase().includes('quit'); n++) {
    await delay(25);
    output += bubbletea_read();
  }
  assert.ok(output.toLowerCase().includes('quit'), 'initial terminal view rendered');
  assert.ok(output.includes('\x1b]7501;?'), 'application queries action support');
  bubbletea_write('\x1b]7501;?\x1b\\');
  let report;
  for (let n = 0; n < 100 && !report; n++) {
    await delay(25);
    output += bubbletea_read();
    report = output.match(/\x1b\]7501;state=idle:[^\x1b]*:msg=([A-Za-z0-9+/=]+)\x1b\\/);
  }
  assert.ok(report, 'application publishes OSC action snapshot');
  const payload = Buffer.from(report[1], 'base64').toString('utf8');
  const snapshot = JSON.parse(payload.split(' | booba-actions/v1 ')[1]);
  const quit = snapshot.actions.find(action => action.label === 'Quit');
  assert.ok(quit?.enabled, 'quit action enabled');
  if (demo === 'grid') {
    for (const label of ['Up', 'Down', 'Left', 'Right']) {
      assert.ok(snapshot.actions.some(action => action.label === label && action.enabled), `${label} action`);
    }
    const right = snapshot.actions.find(action => action.label === 'Right');
    assert.equal(right.keys[right.preferred].key, 'right');
  }
  if (demo === 'animation') {
    const expectedActions = composite
      ? ['Pause', 'First frame', 'Keep screen & quit']
      : scroll
        ? ['Pause', 'Up', 'Down', 'Top', 'Keep screen & quit']
        : ['Pause', 'Previous frame', 'Next frame', 'First frame', 'Keep screen & quit'];
    for (const label of expectedActions) {
      assert.ok(snapshot.actions.some(action => action.label === label && action.enabled), `${label} action`);
    }
    bubbletea_write(' ');
    let paused = '';
    for (let n = 0; n < 100 && !paused.includes('aused'); n++) {
      await delay(25);
      paused += bubbletea_read();
    }
    assert.ok(paused.includes('aused'), 'animation pauses through input bridge');
    bubbletea_write(composite ? '\x1b[H' : scroll ? '\x1b[B' : '.');
    let stepped = '';
    for (let n = 0; n < 100 && (!composite ? stepped.length === 0 : n < 4); n++) {
      await delay(25);
      stepped += bubbletea_read();
    }
    // Restart may leave the screen identical when already on frame one.
    if (!composite) assert.ok(stepped.length > 0, scroll ? 'scroll document moves down' : 'paused animation can step frames');
    bubbletea_write(' ');
  }
  if (demo === 'framebuffer') {
    for (const label of ['Pause', 'Up', 'Down', 'Left', 'Right', 'Center view', 'Keep screen & quit']) {
      assert.ok(snapshot.actions.some(action => action.label === label && action.enabled), `${label} action`);
    }
    // Node answers no Kitty query, so glyph output is active and the
    // resolution controls must not be advertised.
    assert.ok(!snapshot.actions.some(action => action.label === 'Higher resolution' && action.enabled), 'resolution hidden without Kitty');
    // Fresh glyph rows after startup show that rendered frames keep arriving.
    let frames = '';
    for (let n = 0; n < 100 && !frames.includes('▄'); n++) {
      await delay(25);
      frames += bubbletea_read();
    }
    assert.ok(frames.includes('▄'), 'producer delivered frames');
    bubbletea_write(' ');
    let paused = '';
    for (let n = 0; n < 100 && !paused.includes('aused'); n++) {
      await delay(25);
      paused += bubbletea_read();
    }
    assert.ok(paused.includes('aused'), 'framebuffer pauses through input bridge');
    // While paused, only the viewport changes; the redraw proves the pan.
    bubbletea_read();
    bubbletea_write('\x1b[C');
    let panned = '';
    for (let n = 0; n < 100 && panned.length === 0; n++) {
      await delay(25);
      panned += bubbletea_read();
    }
    assert.ok(panned.length > 0, 'paused viewport pans');
    bubbletea_write(' ');
  }
  bubbletea_resize(44, 18);
  if (demo === 'grid') bubbletea_write('d');
  let changed = '';
  for (let n = 0; n < 100 && changed.length === 0; n++) {
    await delay(25);
    changed += bubbletea_read();
  }
  assert.ok(changed.length > 0, 'resize or movement produces output');
  bubbletea_write(process.argv.includes('keep') ? 'Q' : quit.keys[quit.preferred].key);
  await running;
  assert.equal(exitCode, 0);
  assert.equal(globalThis.bubbletea_exited, true, 'exit published to adapter');
  console.log(`${demo}: startup, action publication, resize/input, and clean exit passed`);
} finally {
  clearTimeout(timeout);
}
// Like go_js_wasm_exec, terminate the host process after Go exits. The Go
// runtime may still have scheduled timer callbacks in this Node process.
process.exit(0);
