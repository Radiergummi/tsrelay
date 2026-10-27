import { expect, test } from "vitest";
import { tokenize } from "./tokenize";

const samples: [string, "json" | "markup" | "form" | "text"][] = [
  [`{"a": [1, -2.5e3, true, null], "b": "x\\"y", "c": {}}`, "json"],
  [`{"cut": "aaa`, "json"],
  [`not json at all {`, "json"],
  [`<a href="x" b='y' c=d>text<!-- c --></a><br/>`, "markup"],
  [`<unterminated attr="`, "markup"],
  [`a=1&b=%3Cscript%3E&c&=v`, "form"],
  ["plain\ntext\u202e", "text"],
];

test("tokens always join back to the input", () => {
  for (const [text, kind] of samples)
    expect(
      tokenize(text, kind)
        .map((token) => token.text)
        .join(""),
    ).toBe(text);
});

test("JSON keys, strings, numbers and literals", () => {
  const kinds = tokenize(`{"k": "v", "n": 12345678901234567890, "t": true}`, "json").filter(
    ({ kind }) => kind !== "plain" && kind !== "punct",
  );
  expect(kinds.map(({ kind, text }) => [kind, text])).toEqual([
    ["key", `"k"`],
    ["string", `"v"`],
    ["key", `"n"`],
    ["number", "12345678901234567890"],
    ["key", `"t"`],
    ["literal", "true"],
  ]);
});

test("markup tags, attributes and values", () => {
  const tokens = tokenize(`<a href="x">hi</a>`, "markup");
  expect(tokens.filter((token) => token.kind === "tag").map((token) => token.text)).toEqual([
    "<a",
    ">",
    "</a",
    ">",
  ]);
  expect(tokens.find((token) => token.kind === "attr")?.text).toBe("href");
  expect(tokens.find((token) => token.kind === "value")?.text).toBe(`"x"`);
});

test("a self-closing tag's slash and bracket are one token", () => {
  const tokens = tokenize("<br/>", "markup");
  expect(tokens.filter((token) => token.kind === "tag").map((token) => token.text)).toEqual([
    "<br",
    "/>",
  ]);
});
