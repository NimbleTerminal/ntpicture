// Exercise one real WASM program through booba's JS bridge in a fresh process.
// This checks startup/input/resize/exit, not browser canvas rendering.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { setTimeout as delay } from 'node:timers/promises';
const demo = process.argv[2];
assert.ok(['still', 'simple', 'grid'].includes(demo));
vm.runInThisContext(await readFile('web/wasm_exec.js', 'utf8'));
const go = new Go();
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
  bubbletea_resize(44, 18);
  if (demo === 'grid') bubbletea_write('d');
  await delay(150);
  const changed = bubbletea_read();
  assert.ok(changed.length > 0, 'resize or movement produces output');
  bubbletea_write(quit.keys[quit.preferred].key);
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
