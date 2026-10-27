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
  for (let match = jsonRE.exec(text); match; match = jsonRE.exec(text)) {
    const [matched, quoted, numeral, literal, punctuation] = match;
    if (quoted !== undefined) {
      colonRE.lastIndex = jsonRE.lastIndex;
      out.push({ kind: colonRE.test(text) ? "key" : "string", text: matched });
    } else if (numeral !== undefined) out.push({ kind: "number", text: matched });
    else if (literal !== undefined) out.push({ kind: "literal", text: matched });
    else if (punctuation !== undefined) out.push({ kind: "punct", text: matched });
    else out.push({ kind: "plain", text: matched });
  }
  return merge(out);
}

const tagRE = /(<!--[\s\S]*?(?:-->|$))|(<\/?[A-Za-z][^\s/>]*)|([^<]+|<)/gy;
/** The closing alternative (\/?>) comes before the value alternative so "/>" matches as one token, not a stray "/" value followed by ">". */
const attrRE = /(\s+)|([^\s=/>"']+)|(=)|(\/?>)|("[^"]*"?|'[^']*'?|[^\s>"']+)/gy;

function markup(text: string): Token[] {
  const out: Token[] = [];
  tagRE.lastIndex = 0;
  for (let match = tagRE.exec(text); match; match = tagRE.exec(text)) {
    const [matched, comment, openTag] = match;
    if (comment !== undefined) out.push({ kind: "comment", text: matched });
    else if (openTag !== undefined) {
      out.push({ kind: "tag", text: matched });
      attrRE.lastIndex = tagRE.lastIndex;
      let end = tagRE.lastIndex; // where the tag's text has been emitted to
      let closed = false;
      let expectValue = false;
      for (let attribute = attrRE.exec(text); attribute; attribute = attrRE.exec(text)) {
        const [attributeText, space, name, equals, close] = attribute;
        end = attrRE.lastIndex;
        if (close !== undefined) {
          out.push({ kind: "tag", text: attributeText });
          closed = true;
          break;
        }
        if (space !== undefined) out.push({ kind: "plain", text: attributeText });
        else if (equals !== undefined) {
          out.push({ kind: "punct", text: attributeText });
          expectValue = true;
          continue;
        } else if (expectValue) out.push({ kind: "value", text: attributeText });
        else if (name !== undefined) out.push({ kind: "attr", text: attributeText });
        else out.push({ kind: "value", text: attributeText });
        expectValue = false;
      }
      // An unterminated tag: whatever the attribute scan couldn't read is plain.
      if (!closed && end < text.length) out.push({ kind: "plain", text: text.slice(end) });
      tagRE.lastIndex = closed ? end : text.length;
    } else out.push({ kind: "plain", text: matched });
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
    const equals = part.indexOf("=");
    if (equals < 0) {
      if (part) out.push({ kind: "attr", text: part });
      return;
    }
    if (equals > 0) out.push({ kind: "attr", text: part.slice(0, equals) });
    out.push({ kind: "punct", text: "=" });
    if (equals + 1 < part.length) out.push({ kind: "value", text: part.slice(equals + 1) });
  });
  return out;
}

/** merge joins neighbouring plain tokens, so a run of unrecognised characters is one span. */
function merge(tokens: Token[]): Token[] {
  const out: Token[] = [];
  for (const token of tokens) {
    const last = out.at(-1);
    if (last && last.kind === "plain" && token.kind === "plain") last.text += token.text;
    else out.push({ ...token });
  }
  return out;
}
