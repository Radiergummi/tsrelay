import { render } from "@testing-library/react";
import { expect, test } from "vitest";
import { buttonVariants } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { loadingRowClassName, reloadButtonClassName } from "./Layout";

/**
 * Layout.tsx inlines the markup of <Skeleton/> and <Button/> (default/
 * default) to keep cn()/tailwind-merge out of the entry bundle (the 120 KiB
 * gzip budget). These tests fail if the inlined classes drift from what the
 * real shared components actually render/produce.
 */

// buttonVariants' base classes include a few `[&_svg]...` child-selectors
// that only style an icon inside the button; Layout's Reload button has no
// icon, so those never apply and are excluded from the comparison below.
const iconOnlySelectors = new Set([
  "[&_svg]:pointer-events-none",
  "[&_svg]:shrink-0",
  "[&_svg:not([class*='size-'])]:size-4",
]);

test("the inline Reload button carries every non-icon class buttonVariants(default/default) produces", () => {
  const want = buttonVariants({ variant: "default", size: "default" })
    .split(/\s+/)
    .filter((c) => c && !iconOnlySelectors.has(c));
  const have = new Set(reloadButtonClassName.split(/\s+/).filter(Boolean));
  for (const cls of want) expect(have.has(cls)).toBe(true);
});

test('the inline loading row matches <Skeleton className="h-8 w-full" />\'s rendered classes', () => {
  const { container } = render(<Skeleton className="h-8 w-full" />);
  const rendered = new Set(container.firstElementChild!.className.split(/\s+/).filter(Boolean));
  const inline = new Set(loadingRowClassName.split(/\s+/).filter(Boolean));
  expect(inline).toEqual(rendered);
});
