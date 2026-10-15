/** Platform picks the install script: install.sh for macOS and Linux, install.ps1 for Windows. */
export type Platform = "unix" | "windows";

/** detectPlatform guesses the viewer's platform from the browser; anything but Windows gets the shell script. */
export function detectPlatform(userAgent: string = navigator.userAgent): Platform {
  return /Windows/i.test(userAgent) ? "windows" : "unix";
}

/** installCommand is the one-liner that installs the CLI from the relay at origin. */
export function installCommand(platform: Platform, origin: string): string {
  return platform === "windows"
    ? `irm ${origin}/install.ps1 | iex`
    : `curl -fsSL ${origin}/install.sh | sh`;
}

/** downloads are the release's builds, as the relay's /download/{os}/{arch} serves them. */
export const downloads = [
  { os: "darwin", arch: "arm64", label: "macOS, Apple silicon" },
  { os: "darwin", arch: "amd64", label: "macOS, Intel" },
  { os: "linux", arch: "amd64", label: "Linux, x86-64" },
  { os: "linux", arch: "arm64", label: "Linux, ARM64" },
  { os: "windows", arch: "amd64", label: "Windows, x86-64" },
  { os: "windows", arch: "arm64", label: "Windows, ARM64" },
];
