import type { UseQueryResult } from "@tanstack/react-query";
import type { ComponentType, ReactNode } from "react";
import { ApiError } from "@/api/client";
import { PageError, TableSkeleton } from "./PageState";

/** isGone: the relay answered 404, so what the query asked for no longer exists (or isn't the viewer's). */
export function isGone(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

/**
 * QueryView renders a query's state the one way the UI does it: pending
 * first, then gone (a 404, where the caller has a gone view), then the error
 * if there is no data, then the data. A failed refetch keeps showing the
 * data it already has.
 */
export function QueryView<T>({
  query,
  pending = <TableSkeleton />,
  gone,
  error: Failed = PageError,
  children,
}: {
  query: UseQueryResult<T>;
  pending?: ReactNode;
  gone?: ReactNode;
  error?: ComponentType<{ error: unknown }>;
  children: (data: T) => ReactNode;
}): ReactNode {
  if (query.isPending) return pending;
  if (gone !== undefined && isGone(query.error)) return gone;
  if (query.data === undefined) return <Failed error={query.error} />;
  return children(query.data);
}
