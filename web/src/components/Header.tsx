import { Link, NavLink } from "react-router";
import type { Me } from "@/api/schemas";
import { isAdmin, isReceiver } from "@/lib/viewer";
import { ThemeToggle } from "./ThemeToggle";

const navClass = (isActive: boolean) =>
  isActive ? "font-medium text-foreground" : "text-muted-foreground hover:text-foreground";

export function Header({ me }: { me: Me }) {
  return (
    <header className="border-b">
      <div className="mx-auto flex h-12 max-w-[1400px] items-center gap-6 px-4 sm:px-6">
        <Link to={isReceiver(me) ? "/hooks" : "/admin"} className="font-medium tracking-tight">
          tsrelay
        </Link>
        <nav className="flex gap-4">
          {isReceiver(me) && (
            <NavLink to="/hooks" end className={({ isActive }) => navClass(isActive)}>
              Hooks
            </NavLink>
          )}
          {(isReceiver(me) || isAdmin(me)) && (
            <NavLink to="/channels" className={({ isActive }) => navClass(isActive)}>
              Channels
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
          <ThemeToggle />
          <span className="truncate">{me.login ?? me.node}</span>
        </div>
      </div>
    </header>
  );
}
