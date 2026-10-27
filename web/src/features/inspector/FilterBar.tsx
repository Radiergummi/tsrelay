import { useEffect, useEffectEvent, useRef, useState, type Ref } from "react";
import type { Summary } from "@/api/schemas";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { cn } from "@/lib/utils";
import { countByStatus, methodsOf, sendersOf, statusFilters, type Filters } from "./filters";

const pathWriteDelay = 300;

export function FilterBar({
  calls,
  filters,
  onChange,
  searchRef,
}: {
  calls: Summary[];
  filters: Filters;
  onChange: (f: Filters) => void;
  searchRef: Ref<HTMLInputElement>;
}) {
  const counts = countByStatus(calls);
  // The path text is local state, not controlled straight from the URL:
  // RouterProvider applies a navigation inside startTransition, so a
  // URL-controlled input's value can briefly revert to the pre-keystroke
  // value while that transition is still committing, dropping fast
  // keystrokes. `synced` is the last value we either read from or wrote to
  // the URL, so an external change (back/forward, a filter reset
  // elsewhere) re-seeds this state without fighting our own write below.
  const [path, setPath] = useState(filters.path);
  const synced = useRef(filters.path);
  useEffect(() => {
    if (filters.path !== synced.current) {
      synced.current = filters.path;
      setPath(filters.path);
    }
  }, [filters.path]);
  // The debounced write uses the filters and onChange current when it fires.
  const write = useEffectEvent((p: string) => {
    synced.current = p;
    onChange({ ...filters, path: p });
  });
  useEffect(() => {
    if (path === synced.current) return;
    const t = setTimeout(() => write(path), pathWriteDelay);
    return () => clearTimeout(t);
  }, [path]);
  return (
    <div className="flex flex-col gap-2 pb-4">
      <fieldset
        className="m-0 flex min-w-0 flex-wrap gap-1 border-0 p-0"
        aria-label="Filter by status"
      >
        {statusFilters.map((s) => (
          <button
            key={s}
            type="button"
            aria-pressed={filters.status === s}
            onClick={() => onChange({ ...filters, status: s })}
            className={cn(
              "rounded-md px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground",
              filters.status === s && "bg-accent text-accent-foreground",
            )}
          >
            {s} <span className="tabular-nums">{counts[s]}</span>
          </button>
        ))}
      </fieldset>
      <div className="flex gap-2">
        <Input
          ref={searchRef}
          type="search"
          aria-label="Filter by path"
          placeholder="Path contains…  /"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          className="h-7"
        />
        <NativeSelect
          aria-label="Filter by method"
          value={filters.method}
          onChange={(e) => onChange({ ...filters, method: e.target.value })}
          className="h-7 w-28"
        >
          <option value="">Any method</option>
          {methodsOf(calls).map((m) => (
            <option key={m}>{m}</option>
          ))}
        </NativeSelect>
        <NativeSelect
          aria-label="Filter by sender"
          value={filters.sender}
          onChange={(e) => onChange({ ...filters, sender: e.target.value })}
          className="h-7 w-40"
        >
          <option value="">Any sender</option>
          {sendersOf(calls).map((s) => (
            <option key={s}>{s}</option>
          ))}
        </NativeSelect>
      </div>
    </div>
  );
}
