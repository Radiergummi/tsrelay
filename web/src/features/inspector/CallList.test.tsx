import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, expect, test, vi } from "vitest";
import { fixtures } from "@/test/fixtures";
import { CallList } from "./CallList";

afterEach(() => {
  vi.restoreAllMocks();
});

function list(selected: string) {
  return (
    <MemoryRouter>
      <CallList calls={fixtures.calls} hookId={fixtures.hook.id} selected={selected} search="" />
    </MemoryRouter>
  );
}

test("re-selecting or unmounting doesn't throw when scrollIntoView returns a promise", () => {
  // Newer browsers resolve scrollIntoView() with a promise once scrolling
  // settles; the scroll-into-view effect must not return that promise as
  // its own cleanup function, or React throws when it tries to call it.
  vi.spyOn(Element.prototype, "scrollIntoView").mockReturnValue(
    Promise.resolve() as unknown as undefined,
  );
  const [first, second] = fixtures.calls;
  const { rerender, unmount } = render(list(first!.id));
  expect(() => rerender(list(second!.id))).not.toThrow();
  expect(() => unmount()).not.toThrow();
});

test("a refused call is marked", () => {
  const [delivered] = fixtures.calls;
  const refused = { ...delivered!, id: "c_refused", refused: true };
  render(
    <MemoryRouter>
      <CallList
        calls={[refused, delivered!]}
        hookId={fixtures.hook.id}
        selected={undefined}
        search=""
      />
    </MemoryRouter>,
  );
  expect(screen.getAllByText("refused")).toHaveLength(1);
});
