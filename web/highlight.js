// A small Go tokenizer for the source panel. It only needs to colour one
// example file, so it knows comments, strings, numbers, keywords and
// predeclared names, and treats everything else as plain text.
const keywords = new Set('break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var'.split(' '));
const types = new Set('any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr true false iota nil'.split(' '));
const functions = new Set('append cap clear close complex copy delete imag len make max min new panic print println real recover'.split(' '));
const token = /(\/\/[^\n]*|\/\*[\s\S]*?\*\/)|("(?:\\.|[^"\\\n])*"|`[^`]*`|'(?:\\.|[^'\\\n])*')|(\b\d[\w.]*)|([A-Za-z_]\w*)/g;

// isBuiltin tells a predeclared name from a field or method that shares it,
// such as tty.close or a method named clear: those follow a dot, and a
// predeclared function is only ever called, never declared.
function isBuiltin(source, index, word) {
  if (source[index - 1] === '.') return false;
  if (types.has(word)) return true;
  if (!functions.has(word) || source[index + word.length] !== '(') return false;
  return source.slice(0, index).trimEnd().at(-1) !== ')';
}

// tokenizeGo splits source into {kind, text} pieces whose text concatenates
// back to the input. kind is comment, string, number, keyword, builtin, or
// undefined for plain text.
export function tokenizeGo(source) {
  const pieces = [];
  let last = 0;
  for (const match of source.matchAll(token)) {
    if (match.index > last) pieces.push({ text: source.slice(last, match.index) });
    const [text, comment, string, number, word] = match;
    const kind = comment ? 'comment' : string ? 'string' : number ? 'number'
      : keywords.has(word) ? 'keyword' : isBuiltin(source, match.index, word) ? 'builtin' : undefined;
    pieces.push(kind ? { kind, text } : { text });
    last = match.index + text.length;
  }
  if (last < source.length) pieces.push({ text: source.slice(last) });
  return pieces;
}

// highlightGo replaces element's content with coloured source. It builds text
// nodes and spans, never markup, so the source cannot inject HTML.
export function highlightGo(element, source) {
  const nodes = tokenizeGo(source).map(({ kind, text }) => {
    if (!kind) return document.createTextNode(text);
    const span = document.createElement('span');
    span.className = `tok-${kind}`;
    span.textContent = text;
    return span;
  });
  element.replaceChildren(...nodes);
}
