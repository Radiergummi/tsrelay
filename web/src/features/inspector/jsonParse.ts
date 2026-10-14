export type JNode =
  | { type: "object"; entries: [string, JNode][] }
  | { type: "array"; items: JNode[] }
  | { type: "string"; value: string }
  | { type: "number"; raw: string }
  | { type: "literal"; raw: string };

/** Deeper documents are shown raw rather than risking the stack. */
const maxDepth = 256;

/**
 * parseJSON parses strict JSON, keeping what JSON.parse would change: the
 * exact text of numbers (so 12345678901234567890 isn't rounded) and every
 * duplicate key in order. It returns null for anything that isn't one
 * complete JSON value, including a body cut at the capture limit.
 */
export function parseJSON(text: string): JNode | null {
  let i = 0;
  const ws = () => {
    while (i < text.length && " \t\n\r".includes(text[i]!)) i++;
  };
  const fail = (): never => {
    throw new SyntaxError();
  };
  const str = (): string => {
    // eslint-disable-next-line no-control-regex -- JSON strings forbid literal control characters.
    const m = /"(?:[^"\\\u0000-\u001f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"/y;
    m.lastIndex = i;
    const r = m.exec(text) ?? fail();
    i = m.lastIndex;
    return JSON.parse(r[0]) as string;
  };
  const value = (depth: number): JNode => {
    if (depth > maxDepth) fail();
    ws();
    const c = text[i];
    if (c === "{") {
      i++;
      const entries: [string, JNode][] = [];
      ws();
      if (text[i] === "}") {
        i++;
        return { type: "object", entries };
      }
      for (;;) {
        ws();
        if (text[i] !== '"') fail();
        const k = str();
        ws();
        if (text[i++] !== ":") fail();
        entries.push([k, value(depth + 1)]);
        ws();
        if (text[i] === ",") i++;
        else if (text[i] === "}") {
          i++;
          return { type: "object", entries };
        } else fail();
      }
    }
    if (c === "[") {
      i++;
      const items: JNode[] = [];
      ws();
      if (text[i] === "]") {
        i++;
        return { type: "array", items };
      }
      for (;;) {
        items.push(value(depth + 1));
        ws();
        if (text[i] === ",") i++;
        else if (text[i] === "]") {
          i++;
          return { type: "array", items };
        } else fail();
      }
    }
    if (c === '"') return { type: "string", value: str() };
    const m = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null/y;
    m.lastIndex = i;
    const r = m.exec(text) ?? fail();
    i = m.lastIndex;
    return /^[tfn]/.test(r[0]) ? { type: "literal", raw: r[0] } : { type: "number", raw: r[0] };
  };
  try {
    const v = value(0);
    ws();
    return i === text.length ? v : null;
  } catch {
    return null;
  }
}
