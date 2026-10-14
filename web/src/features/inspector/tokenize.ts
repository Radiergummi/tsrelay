import type { BodyKind } from "./bytes";

export type TokenKind =
  | "plain"
  | "punct"
  | "key"
  | "string"
  | "number"
  | "literal"
  | "tag"
  | "attr"
  | "value"
  | "comment";
export interface Token {
  kind: TokenKind;
  text: string;
}

/**
 * tokenize splits a body for highlighting. It never fails: whatever it
 * doesn't recognise is plain, and the tokens always join back to text.
 * The viewers render tokens as React text, never as markup.
 */
export function tokenize(text: string, kind: BodyKind): Token[] {
  switch (kind) {
    case "json":
      return json(text);
    case "markup":
      return markup(text);
    case "form":
      return form(text);
    default:
      return [{ kind: "plain", text }];
  }
}

const jsonRE =
  /("(?:[^"\\]|\\.)*"?)|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(true|false|null)\b|([{}[\],:])|(\s+)|(.)/gsy;
/** colonRE looks ahead from a string for the colon that makes it a key, without copying the text. */
const colonRE = /\s*:/y;

function json(text: string): Token[] {
  const out: Token[] = [];
  jsonRE.lastIndex = 0;
  for (let m = jsonRE.exec(text); m; m = jsonRE.exec(text)) {
    const [t, str, num, lit, punct] = m;
    if (str !== undefined) {
      colonRE.lastIndex = jsonRE.lastIndex;
      out.push({ kind: colonRE.test(text) ? "key" : "string", text: t });
    } else if (num !== undefined) out.push({ kind: "number", text: t });
    else if (lit !== undefined) out.push({ kind: "literal", text: t });
    else if (punct !== undefined) out.push({ kind: "punct", text: t });
    else out.push({ kind: "plain", text: t });
  }
  return merge(out);
}

const tagRE = /(<!--[\s\S]*?(?:-->|$))|(<\/?[A-Za-z][^\s/>]*)|([^<]+|<)/gy;
/** The closing alternative (\/?>) comes before the value alternative so "/>" matches as one token, not a stray "/" value followed by ">". */
const attrRE = /(\s+)|([^\s=/>"']+)|(=)|(\/?>)|("[^"]*"?|'[^']*'?|[^\s>"']+)/gy;

function markup(text: string): Token[] {
  const out: Token[] = [];
  tagRE.lastIndex = 0;
  for (let m = tagRE.exec(text); m; m = tagRE.exec(text)) {
    const [t, comment, open] = m;
    if (comment !== undefined) out.push({ kind: "comment", text: t });
    else if (open !== undefined) {
      out.push({ kind: "tag", text: t });
      attrRE.lastIndex = tagRE.lastIndex;
      let end = tagRE.lastIndex; // where the tag's text has been emitted to
      let closed = false;
      let expectValue = false;
      for (let a = attrRE.exec(text); a; a = attrRE.exec(text)) {
        const [at, ws, name, eq, close] = a;
        end = attrRE.lastIndex;
        if (close !== undefined) {
          out.push({ kind: "tag", text: at });
          closed = true;
          break;
        }
        if (ws !== undefined) out.push({ kind: "plain", text: at });
        else if (eq !== undefined) {
          out.push({ kind: "punct", text: at });
          expectValue = true;
          continue;
        } else if (expectValue) out.push({ kind: "value", text: at });
        else if (name !== undefined) out.push({ kind: "attr", text: at });
        else out.push({ kind: "value", text: at });
        expectValue = false;
      }
      // An unterminated tag: whatever the attribute scan couldn't read is plain.
      if (!closed && end < text.length) out.push({ kind: "plain", text: text.slice(end) });
      tagRE.lastIndex = closed ? end : text.length;
    } else out.push({ kind: "plain", text: t });
  }
  return merge(out);
}

function form(text: string): Token[] {
  const out: Token[] = [];
  text.split(/(&)/).forEach((part) => {
    if (part === "&") {
      out.push({ kind: "punct", text: part });
      return;
    }
    const i = part.indexOf("=");
    if (i < 0) {
      if (part) out.push({ kind: "attr", text: part });
      return;
    }
    if (i > 0) out.push({ kind: "attr", text: part.slice(0, i) });
    out.push({ kind: "punct", text: "=" });
    if (i + 1 < part.length) out.push({ kind: "value", text: part.slice(i + 1) });
  });
  return out;
}

/** merge joins neighbouring plain tokens, so a run of unrecognised characters is one span. */
function merge(tokens: Token[]): Token[] {
  const out: Token[] = [];
  for (const t of tokens) {
    const last = out.at(-1);
    if (last && last.kind === "plain" && t.kind === "plain") last.text += t.text;
    else out.push({ ...t });
  }
  return out;
}
