import type { UseQueryResult } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { ApiError } from "@/api/client";
import { QueryView } from "./QueryView";

const apiError = (status: number) =>
  new ApiError({ status, code: "x", title: "", detail: `answered ${status}`, fieldErrors: [] });

// Only the fields QueryView reads; the rest of a query result doesn't matter here.
const query = (q: { isPending?: boolean; data?: string; error?: unknown }) =>
  ({ isPending: false, data: undefined, error: null, ...q }) as UseQueryResult<string>;

const show = (data: string) => <p>data: {data}</p>;

test("shows pending while the first fetch runs", () => {
  render(
    <QueryView query={query({ isPending: true })} pending={<p>waiting</p>}>
      {show}
    </QueryView>,
  );
  expect(screen.getByText("waiting")).toBeInTheDocument();
  expect(screen.queryByText(/data:/)).not.toBeInTheDocument();
});

test("shows the default skeleton when no pending view is given", () => {
  render(<QueryView query={query({ isPending: true })}>{show}</QueryView>);
  expect(screen.getByLabelText("Loading")).toBeInTheDocument();
});

test("shows gone on a 404 even with cached data", () => {
  render(
    <QueryView query={query({ data: "old", error: apiError(404) })} gone={<p>gone</p>}>
      {show}
    </QueryView>,
  );
  expect(screen.getByText("gone")).toBeInTheDocument();
  expect(screen.queryByText(/data:/)).not.toBeInTheDocument();
});

test("without a gone view, a 404 with data keeps the data", () => {
  render(<QueryView query={query({ data: "old", error: apiError(404) })}>{show}</QueryView>);
  expect(screen.getByText("data: old")).toBeInTheDocument();
});

test("keeps the data when a refetch fails", () => {
  render(
    <QueryView query={query({ data: "old", error: apiError(500) })} gone={<p>gone</p>}>
      {show}
    </QueryView>,
  );
  expect(screen.getByText("data: old")).toBeInTheDocument();
});

test("shows the error when there is no data", () => {
  render(<QueryView query={query({ error: apiError(500) })}>{show}</QueryView>);
  expect(screen.getByRole("alert")).toHaveTextContent("answered 500");
});

test("a custom error view gets the error", () => {
  render(
    <QueryView query={query({ error: apiError(503) })} error={(e) => <p>custom {String(e)}</p>}>
      {show}
    </QueryView>,
  );
  expect(screen.getByText(/custom .*answered 503/)).toBeInTheDocument();
});
