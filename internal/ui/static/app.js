// The tsrelay web UI script (DESIGN.md §10.2). Pages are rendered on the
// server. This script writes through the JSON API and keeps the parts of a
// page marked data-live current. It is an ES module so that its pure helpers
// can be tested with node (internal/ui/testdata/app_test.mjs).

export function init(doc) {}

if (typeof document !== "undefined") init(document);
