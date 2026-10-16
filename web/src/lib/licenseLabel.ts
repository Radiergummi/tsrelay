import type { LicenseEntry } from "@/api/schemas";

/**
 * licenseLabel is what a license is called everywhere it is shown: the
 * filter matches on it, the dropdown counts it, the badge and the text
 * dialog show it. They have to agree, or the filter silently stops
 * matching what the badge says.
 */
export function licenseLabel({ id, name }: LicenseEntry): string {
  return id || name || "Unknown";
}
