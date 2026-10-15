import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { InstallCommand } from "@/features/cli/InstallCommand";
import { detectPlatform, downloads, type Platform } from "@/lib/install";
import { useViewer } from "@/lib/useViewer";

export function CliPage() {
  const me = useViewer();
  const [platform, setPlatform] = useState<Platform>(() => detectPlatform());
  const origin = window.location.origin;
  return (
    <section className="max-w-3xl space-y-6">
      <div>
        <h1 className="mb-1 text-lg font-medium">Get the CLI</h1>
        <p className="text-muted-foreground">
          {me.version === "dev"
            ? "This relay is a development build, so you'll get the latest release; it may not match this relay."
            : `You'll get tsrelay ${me.version}, the version this relay runs.`}
        </p>
      </div>
      <Tabs value={platform} onValueChange={(v) => setPlatform(v as Platform)}>
        <TabsList>
          <TabsTrigger value="unix">macOS / Linux</TabsTrigger>
          <TabsTrigger value="windows">Windows</TabsTrigger>
        </TabsList>
        <TabsContent value="unix">
          <InstallCommand platform="unix" origin={origin} />
        </TabsContent>
        <TabsContent value="windows">
          <InstallCommand platform="windows" origin={origin} />
        </TabsContent>
      </Tabs>
      <p className="text-muted-foreground">
        Then run <code>tsrelay http 8080</code> to get a hook whose calls reach port 8080.
      </p>
      <div>
        <h2 className="mb-2 font-medium">Direct downloads</h2>
        <ul className="grid gap-1 sm:grid-cols-2">
          {downloads.map((d) => (
            <li key={`${d.os}/${d.arch}`}>
              <a href={`/download/${d.os}/${d.arch}`} className="text-primary hover:underline">
                {d.label}
              </a>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}
