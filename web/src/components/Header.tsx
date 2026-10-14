import { Link, NavLink } from "react-router";
import type { Me } from "@/api/schemas";
import { cn } from "@/lib/utils";
import { isAdmin, isReceiver } from "@/lib/viewer";
import { ThemeToggle } from "./ThemeToggle";

const nav = ({ isActive }: { isActive: boolean }) =>
  cn("text-muted-foreground hover:text-foreground", isActive && "font-medium text-foreground");

export function Header({ me }: { me: Me }) {
  const roles = Object.keys(me.roles).sort();
  return (
    <header className="border-b">
      <div className="mx-auto flex h-12 max-w-[1400px] items-center gap-6 px-4 sm:px-6">
        <Link to={isReceiver(me) ? "/" : "/admin"} className="font-medium tracking-tight">
          tsrelay
        </Link>
        <nav className="flex gap-4">
          {isReceiver(me) && (
            <NavLink to="/" end className={nav}>
              Hooks
            </NavLink>
          )}
          {isAdmin(me) && (
            <NavLink to="/admin" className={nav}>
              Admin
            </NavLink>
          )}
        </nav>
        <div className="ml-auto flex min-w-0 items-center gap-3">
          <ThemeToggle />
          <span className="truncate">{me.login ?? me.node}</span>
          {roles.length > 0 && (
            <span className="hidden text-xs text-muted-foreground sm:inline">
              {roles.join(", ")}
            </span>
          )}
        </div>
      </div>
    </header>
  );
}
