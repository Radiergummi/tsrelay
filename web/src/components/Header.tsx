import { lazy, Suspense } from "react";
import { Link, NavLink } from "react-router";
import type { Me } from "@/api/schemas";
import { isAdmin, isReceiver } from "@/lib/viewer";

// Lazy: @base-ui/react/button and its lucide-react icons aren't needed for
// the first paint, and deferring them keeps the initial chunk under the
// 120 KiB gzip budget. The toggle pops in right after.
const ThemeToggle = lazy(() => import("./ThemeToggle").then((m) => ({ default: m.ThemeToggle })));

// No cn()/tailwind-merge here: the two states are fully disjoint strings
// (never both applied at once), so there's nothing to merge or resolve —
// and avoiding it keeps tailwind-merge out of the initial bundle too.
const navClass = (isActive: boolean) =>
  isActive ? "font-medium text-foreground" : "text-muted-foreground hover:text-foreground";

export function Header({ me }: { me: Me }) {
  return (
    <header className="border-b">
      <div className="mx-auto flex h-12 max-w-[1400px] items-center gap-6 px-4 sm:px-6">
        <Link to={isReceiver(me) ? "/" : "/admin"} className="font-medium tracking-tight">
          tsrelay
        </Link>
        <nav className="flex gap-4">
          {isReceiver(me) && (
            <NavLink to="/" end className={({ isActive }) => navClass(isActive)}>
              Hooks
            </NavLink>
          )}
          {isReceiver(me) && (
            <NavLink to="/cli" className={({ isActive }) => navClass(isActive)}>
              Get the CLI
            </NavLink>
          )}
          {isAdmin(me) && (
            <NavLink to="/admin" className={({ isActive }) => navClass(isActive)}>
              Admin
            </NavLink>
          )}
        </nav>
        <div className="ml-auto flex min-w-0 items-center gap-3">
          <Suspense fallback={<span className="size-7" />}>
            <ThemeToggle />
          </Suspense>
          <span className="truncate">{me.login ?? me.node}</span>
        </div>
      </div>
    </header>
  );
}
