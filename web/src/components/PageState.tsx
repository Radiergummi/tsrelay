import type { ReactNode } from "react";
import { Skeleton } from "@/components/ui/skeleton";

/** errorText is what an error says, whatever was thrown. */
export const errorText = (error: unknown): string =>
  error instanceof Error ? error.message : String(error);

export function PageError({ error }: { error: unknown }) {
  return (
    <p role="alert" className="text-destructive">
      {errorText(error)}
    </p>
  );
}

export function TableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-2" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-8 w-full" />
      ))}
    </div>
  );
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="py-16 text-center">
      <p className="mb-2 font-medium">{title}</p>
      {children && <div className="text-muted-foreground">{children}</div>}
    </div>
  );
}
