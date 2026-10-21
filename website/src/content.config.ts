import { glob } from "astro/loaders";
import { z } from "astro/zod";
import { defineCollection } from "astro:content";

const docs = defineCollection({
  loader: glob({
    // DESIGN.md is the design document for contributors, not a page.
    pattern: ["*.md", "!DESIGN.md"],
    base: "../docs",
  }),
  schema: z.object({
    title: z.string(),
    description: z.string(),
    category: z.enum(["overview", "guide", "reference"]),
    tags: z.array(z.string()),
  }),
});

export const collections = { docs };
