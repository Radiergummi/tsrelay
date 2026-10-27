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
  const skipSpace = () => {
    while (i < text.length && " \t\n\r".includes(text[i]!)) i++;
  };
  const fail = (): never => {
    throw new SyntaxError();
  };
  const readString = (): string => {
    // eslint-disable-next-line no-control-regex -- JSON strings forbid literal control characters.
    const pattern = /"(?:[^"\\\u0000-\u001f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"/y;
    pattern.lastIndex = i;
    const match = pattern.exec(text) ?? fail();
    i = pattern.lastIndex;
    return JSON.parse(match[0]) as string;
  };
  const value = (depth: number): JNode => {
    if (depth > maxDepth) fail();
    skipSpace();
    const character = text[i];
    if (character === "{") {
      i++;
      const entries: [string, JNode][] = [];
      skipSpace();
      if (text[i] === "}") {
        i++;
        return { type: "object", entries };
      }
      for (;;) {
        skipSpace();
        if (text[i] !== '"') fail();
        const key = readString();
        skipSpace();
        if (text[i++] !== ":") fail();
        entries.push([key, value(depth + 1)]);
        skipSpace();
        if (text[i] === ",") i++;
        else if (text[i] === "}") {
          i++;
          return { type: "object", entries };
        } else fail();
      }
    }
    if (character === "[") {
      i++;
      const items: JNode[] = [];
      skipSpace();
      if (text[i] === "]") {
        i++;
        return { type: "array", items };
      }
      for (;;) {
        items.push(value(depth + 1));
        skipSpace();
        if (text[i] === ",") i++;
        else if (text[i] === "]") {
          i++;
          return { type: "array", items };
        } else fail();
      }
    }
    if (character === '"') return { type: "string", value: readString() };
    const pattern = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null/y;
    pattern.lastIndex = i;
    const [raw] = pattern.exec(text) ?? fail();
    i = pattern.lastIndex;
    return /^[tfn]/.test(raw) ? { type: "literal", raw } : { type: "number", raw };
  };
  try {
    const root = value(0);
    skipSpace();
    return i === text.length ? root : null;
  } catch {
    return null;
  }
}
