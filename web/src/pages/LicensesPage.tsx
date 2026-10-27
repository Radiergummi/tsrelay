import { ExternalLink } from "lucide-react";
import { useMemo, useState } from "react";
import { paths } from "@/api/client";
import { useLicenses } from "@/api/queries";
import type { LicenseComponent } from "@/api/schemas";
import { EmptyState } from "@/components/PageState";
import { QueryView } from "@/components/QueryView";
import { buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { LicenseTextDialog } from "@/features/licenses/LicenseTextDialog";
import { licenseLabel, licensesOf } from "@/lib/licenseLabel";
import { cn } from "@/lib/utils";

type Ecosystem = "all" | LicenseComponent["ecosystem"];

const ecosystemLabels: Record<string, string> = {
  all: "All",
  go: "Go",
  npm: "npm",
  other: "Other",
};

const badgeClass = "inline-flex h-5 items-center rounded-md px-1.5 text-xs font-medium";

const matchesSearch = (component: LicenseComponent, needle: string) =>
  !needle || component.name.toLowerCase().includes(needle);
const matchesEcosystem = (component: LicenseComponent, ecosystem: Ecosystem) =>
  ecosystem === "all" || component.ecosystem === ecosystem;
const matchesLicense = (component: LicenseComponent, license: string) =>
  license === "all" || licensesOf(component).some((entry) => licenseLabel(entry) === license);

/**
 * browserURL turns a VCS URL ("git+https://…", "git://…") into one a
 * browser opens. SBOM URLs are untrusted input, so anything that isn't
 * http(s) afterwards, such as "javascript:", is null and not linked.
 */
function browserURL(url: string | undefined): string | null {
  if (!url) return null;
  const normalized = url.replace(/^git(\+https)?:\/\//, "https://").replace(/\.git$/, "");
  return /^https?:\/\//i.test(normalized) ? normalized : null;
}

/** LicensesPage lists the open-source components built into the relay (GET /licenses). */
export function LicensesPage() {
  const licenses = useLicenses();
  return (
    <QueryView query={licenses}>
      {({ components }) => <Licenses components={components} />}
    </QueryView>
  );
}

function Licenses({ components }: { components: LicenseComponent[] }) {
  const [query, setQuery] = useState("");
  const [ecosystem, setEcosystem] = useState<Ecosystem>("all");
  const [license, setLicense] = useState("all");
  const needle = query.trim().toLowerCase();

  // Each control counts against every filter but its own, so it shows
  // what picking it would leave rather than counting itself out.
  const byLicenseAndSearch = useMemo(
    () =>
      components.filter(
        (component) => matchesLicense(component, license) && matchesSearch(component, needle),
      ),
    [components, license, needle],
  );
  const byEcosystemAndSearch = useMemo(
    () =>
      components.filter(
        (component) => matchesEcosystem(component, ecosystem) && matchesSearch(component, needle),
      ),
    [components, ecosystem, needle],
  );
  const counts = useMemo(() => {
    const result: Record<string, number> = { all: byLicenseAndSearch.length };
    for (const component of byLicenseAndSearch)
      result[component.ecosystem] = (result[component.ecosystem] ?? 0) + 1;
    return result;
  }, [byLicenseAndSearch]);
  // Only licenses something in scope carries are offered; "All licenses"
  // always is, so a filter that narrows to nothing is one step to undo.
  const licenseOptions = useMemo(() => {
    const perLicense = new Map<string, number>();
    for (const component of byEcosystemAndSearch)
      for (const label of new Set(licensesOf(component).map(licenseLabel)))
        perLicense.set(label, (perLicense.get(label) ?? 0) + 1);
    return [...perLicense.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [byEcosystemAndSearch]);
  const shown = useMemo(
    () =>
      byLicenseAndSearch
        .filter((component) => matchesEcosystem(component, ecosystem))
        .sort((a, b) => a.name.localeCompare(b.name)),
    [byLicenseAndSearch, ecosystem],
  );

  // Which chips exist depends on the inventory, not the filters: a chip
  // that vanished while typing would take its selection with it.
  const ecosystems: Ecosystem[] = ["all", "go", "npm"];
  if (components.some((component) => component.ecosystem === "other")) ecosystems.push("other");

  return (
    <section className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="mb-1 text-lg font-medium">Open-source licenses</h1>
          <p className="text-muted-foreground">The open-source projects built into tsrelay.</p>
        </div>
        <a
          href={paths.notices}
          download="tsrelay-notices.txt"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Download all notices
        </a>
      </div>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Input
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search by name…"
          aria-label="Search by name"
          className="sm:max-w-xs"
        />
        <div className="flex items-center gap-2 overflow-x-auto">
          <NativeSelect
            aria-label="License"
            value={license}
            onChange={(event) => setLicense(event.target.value)}
            className="h-7 w-48 shrink-0 text-xs"
          >
            <option value="all">All licenses ({byEcosystemAndSearch.length})</option>
            {licenseOptions.map(([label, count]) => (
              <option key={label} value={label}>
                {label} ({count})
              </option>
            ))}
          </NativeSelect>
          {ecosystems.map((value) => (
            <button
              key={value}
              type="button"
              aria-pressed={value === ecosystem}
              onClick={() => setEcosystem(value)}
              className={cn(
                "inline-flex h-7 shrink-0 items-center rounded-md px-3 text-xs",
                value === ecosystem
                  ? "bg-primary font-medium text-primary-foreground"
                  : "border text-muted-foreground transition hover:text-foreground",
              )}
            >
              {ecosystemLabels[value]}
              <span className="ml-1.5 opacity-60">{counts[value] ?? 0}</span>
            </button>
          ))}
        </div>
      </div>
      {shown.length === 0 ? (
        <EmptyState title="No matching components." />
      ) : (
        <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {shown.map((component) => (
            <LicenseCard
              key={`${component.ecosystem}:${component.name}@${component.version ?? ""}`}
              component={component}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function LicenseCard({ component }: { component: LicenseComponent }) {
  const { name, version, description, ecosystem, textId } = component;
  const [open, setOpen] = useState(false);
  const homepage = browserURL(component.homepage);
  const repository = browserURL(component.repository);
  return (
    <li className="flex flex-col gap-2 rounded-lg border p-4">
      <div className="flex items-start justify-between gap-2">
        {homepage ? (
          <a
            href={homepage}
            target="_blank"
            rel="noopener noreferrer"
            className="truncate font-medium transition hover:text-primary"
          >
            {name}
          </a>
        ) : (
          <span className="truncate font-medium">{name}</span>
        )}
        <span className={cn(badgeClass, "border")}>{ecosystemLabels[ecosystem] ?? ecosystem}</span>
      </div>
      {version && <span className="font-mono text-xs text-muted-foreground">{version}</span>}
      {description && <p className="line-clamp-2 text-xs text-muted-foreground">{description}</p>}
      <div className="mt-auto flex flex-wrap items-center gap-1.5 pt-1">
        {/* Every license on the card opens the same text, so the badges are
            buttons only when there is one. */}
        {licensesOf(component).map((entry, i) =>
          textId ? (
            <button
              key={entry.id || entry.name || i}
              type="button"
              onClick={() => setOpen(true)}
              className={cn(badgeClass, "cursor-pointer bg-secondary text-secondary-foreground")}
            >
              {licenseLabel(entry)}
            </button>
          ) : (
            <span
              key={entry.id || entry.name || i}
              className={cn(badgeClass, "bg-secondary text-secondary-foreground")}
            >
              {licenseLabel(entry)}
            </span>
          ),
        )}
        {repository && (
          <a
            href={repository}
            target="_blank"
            rel="noopener noreferrer"
            className="ml-auto inline-flex items-center gap-1 text-xs text-muted-foreground transition hover:text-foreground"
          >
            <ExternalLink className="size-3" />
            Source
          </a>
        )}
      </div>
      {textId && <LicenseTextDialog component={component} open={open} onOpenChange={setOpen} />}
    </li>
  );
}
