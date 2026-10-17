import { BoobaTerminal, installActionBar, installRendererHud, parseRendererFromURL } from './booba/booba.js';

const demos = {
  still: 'Still image',
  simple: 'Embedded image',
  grid: 'Interactive grid',
  animation: 'GIF & WebP playback',
  framebuffer: 'Live framebuffer',
};
const requested = new URLSearchParams(location.search).get('demo');
const demo = Object.hasOwn(demos, requested) ? requested : 'still';
const composite = demo === 'animation' && new URLSearchParams(location.search).get('composite') === '1';
const scroll = demo === 'animation' && !composite && new URLSearchParams(location.search).get('scroll') === '1';
const title = composite ? 'Animated regions' : scroll ? 'Scrolling document' : demos[demo];
const status = document.querySelector('#status');
document.querySelector('#title').textContent = title;
document.title = `${title} · ntpicture`;
const restart = new URL(location.href);
restart.searchParams.set('demo', demo);
document.querySelector('#restart').href = restart.href;
const sourcePath = composite
  ? 'blob/main/examples/animation/composite.go'
  : scroll
    ? 'blob/main/examples/animation/scroll.go'
    : `tree/main/examples/${demo}`;
document.querySelector('#source').href = `https://github.com/NimbleTerminal/ntpicture/${sourcePath}`;
for (const link of document.querySelectorAll('[data-demo]')) {
  if (link.dataset.demo === (composite ? 'composite' : scroll ? 'scroll' : demo)) link.setAttribute('aria-current', 'page');
}
if (demo === 'grid') {
  document.querySelector('#help').textContent = 'Arrows / WASD / HJKL move · g toggles glyph/Kitty · q quits. Click the terminal to focus.';
}
if (demo === 'animation') {
  document.querySelector('#help').textContent = 'Space pauses/plays both clips · comma/period steps · Home returns to frame one · Q keeps the screen and quits · q clears and quits. Buttons above control playback and keep-screen exit; use the keyboard for q.';
}
if (demo === 'framebuffer') {
  document.querySelector('#help').textContent = 'Space pauses/plays · arrows/WASD/HJKL pan the viewport · c recenters · [ and ] change Kitty resolution · g toggles glyph/Kitty · Q keeps the screen and quits · q clears and quits. Pause, then pan: the image is re-addressed, not retransmitted.';
}
function failed(error) {
  status.dataset.error = '';
  status.textContent = `Could not run the demo: ${error.message}. Try Restart.`;
  console.error(error);
}
async function main() {
  const go = new Go();
  if (composite) {
    go.argv = ['animation', '--composite'];
    document.querySelector('#help').textContent = 'One picture containing GIF and WebP regions. Space pauses/plays · Home restarts · Q keeps the screen · q clears and quits.';
  } else if (scroll) {
    go.argv = ['animation', '--scroll'];
    document.querySelector('#help').textContent = 'Mixed still and animated images in a scrollable document. Up/Down/J/K scroll · Space pauses/plays · Home returns to top · g toggles glyph/Kitty · Q keeps the screen · q clears and quits.';
  }
  const response = await fetch(`demos/${demo}.wasm`);
  if (!response.ok) throw new Error(`Download failed (${response.status})`);
  const { instance } = await WebAssembly.instantiateStreaming(response, go.importObject);
  const running = go.run(instance);
  running.then(() => { status.textContent = 'Session ended. Choose Restart to run again.'; }, failed);
  const terminal = new BoobaTerminal('terminal', { renderer: parseRendererFromURL(), allowOSC7501Actions: true });
  await terminal.init();
  terminal.setActionKeyEncoder({
    canEncode: gesture => terminal.encodeKey(gesture) !== null,
    encode: gesture => terminal.encodeKey(gesture),
  });
  installActionBar(terminal.actions, {
    parent: document.querySelector('#controls'),
    openKeyboard: () => terminal.focus(),
    onLayout: () => terminal.fitAddon?.fit(),
  });
  terminal.onActionError = failed;
  window.addEventListener('pagehide', () => terminal.dispose(), { once: true });
  terminal.connectWasm();
  terminal.focus();
  installRendererHud(terminal.term, {
    parent: document.querySelector('.terminal-frame'),
    position: 'absolute',
    bindToggleHotkey: false,
  });
  status.textContent = 'Running locally in your browser. No terminal server required.';
}
main().catch(failed);
