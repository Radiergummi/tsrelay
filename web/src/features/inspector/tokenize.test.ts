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
        .map((t) => t.text)
        .join(""),
    ).toBe(text);
});

test("JSON keys, strings, numbers and literals", () => {
  const kinds = tokenize(`{"k": "v", "n": 12345678901234567890, "t": true}`, "json").filter(
    (t) => t.kind !== "plain" && t.kind !== "punct",
  );
  expect(kinds.map((t) => [t.kind, t.text])).toEqual([
    ["key", `"k"`],
    ["string", `"v"`],
    ["key", `"n"`],
    ["number", "12345678901234567890"],
    ["key", `"t"`],
    ["literal", "true"],
  ]);
});

test("markup tags, attributes and values", () => {
  const t = tokenize(`<a href="x">hi</a>`, "markup");
  expect(t.filter((x) => x.kind === "tag").map((x) => x.text)).toEqual(["<a", ">", "</a", ">"]);
  expect(t.find((x) => x.kind === "attr")?.text).toBe("href");
  expect(t.find((x) => x.kind === "value")?.text).toBe(`"x"`);
});

test("a self-closing tag's slash and bracket are one token", () => {
  const t = tokenize("<br/>", "markup");
  expect(t.filter((x) => x.kind === "tag").map((x) => x.text)).toEqual(["<br", "/>"]);
});
