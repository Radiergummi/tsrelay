import type { LicenseComponent, LicenseEntry } from "@/api/schemas";

/**
 * licenseLabel is what a license is called everywhere it is shown: the
 * filter matches on it, the dropdown counts it, the badge and the text
 * dialog show it. They have to agree, or the filter silently stops
 * matching what the badge says.
 */
export function licenseLabel({ id, name }: LicenseEntry): string {
  return id || name || "Unknown";
}

/**
 * licensesOf is a component's licenses, or one unknown license when none
 * was detected, so its text can still be found and opened.
 */
export function licensesOf(c: LicenseComponent): LicenseEntry[] {
  return c.licenses.length > 0 ? c.licenses : [{}];
}
