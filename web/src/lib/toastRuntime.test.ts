import { expect, test } from "vitest";
import { toastManager } from "./toastRuntime";

test("a toast added before any subscriber is queued and replayed once one subscribes", () => {
  const id = toastManager.add({ title: "Queued" });
  const received: unknown[] = [];
  const unsubscribe = toastManager[" subscribe"]((event) => received.push(event));
  expect(received).toEqual([
    { action: "add", options: expect.objectContaining({ id, title: "Queued" }) },
  ]);
  unsubscribe();
});
