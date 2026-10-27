import { MutationCache, QueryClient } from "@tanstack/react-query";
import { toast } from "@/lib/toast";
import { ApiError } from "./client";

/**
 * makeQueryClient: queries refetch through the event stream rather than on
 * focus, 4xx answers are never retried, and a failed write shows a toast
 * with the problem's detail. A 422 shows under its fields instead.
 */
export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 10_000,
        refetchOnWindowFocus: false,
        retry: (failureCount, error) =>
          failureCount < 2 &&
          !(error instanceof ApiError && error.status >= 400 && error.status < 500),
      },
      mutations: { retry: false },
    },
    mutationCache: new MutationCache({
      onError: (error) => {
        if (error instanceof ApiError && error.status === 422) return;
        toast.error("That didn't work", error.message);
      },
    }),
  });
}
