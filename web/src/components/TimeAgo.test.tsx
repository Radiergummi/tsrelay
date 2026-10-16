import { act, render, screen } from "@testing-library/react";
import { Profiler } from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { TimeAgo } from "./TimeAgo";

beforeEach(() => void vi.useFakeTimers({ now: Date.parse("2026-10-14T12:00:05Z") }));
afterEach(() => void vi.useRealTimers());

test("counts seconds live, then stops ticking every second once nothing is young", () => {
  const onRender = vi.fn<() => void>();
  const { unmount } = render(
    <Profiler id="t" onRender={onRender}>
      <TimeAgo iso="2026-10-14T12:00:00Z" />
    </Profiler>,
  );
  expect(screen.getByText("5 s ago")).toBeInTheDocument();
  act(() => void vi.advanceTimersByTime(1000));
  expect(screen.getByText("6 s ago")).toBeInTheDocument();

  act(() => void vi.advanceTimersByTime(24_000));
  expect(screen.getByText("30 s ago")).toBeInTheDocument();
  act(() => void vi.advanceTimersByTime(1000));
  expect(screen.getByText("less than a minute ago")).toBeInTheDocument();

  // 31 s old: nothing is young, so the next tick is when it turns a minute.
  onRender.mockClear();
  act(() => void vi.advanceTimersByTime(28_999));
  expect(onRender).not.toHaveBeenCalled();
  act(() => void vi.advanceTimersByTime(1));
  expect(onRender).toHaveBeenCalledTimes(1);
  expect(screen.getByText("1 min ago")).toBeInTheDocument();

  unmount();
  expect(vi.getTimerCount()).toBe(0);
});

test("one young timestamp keeps the shared clock ticking every second", () => {
  render(
    <>
      <TimeAgo iso="2026-10-14T11:00:00Z" />
      <TimeAgo iso="2026-10-14T12:00:00Z" />
    </>,
  );
  expect(screen.getByText("1 h ago")).toBeInTheDocument();
  act(() => void vi.advanceTimersByTime(1000));
  expect(screen.getByText("6 s ago")).toBeInTheDocument();
});
