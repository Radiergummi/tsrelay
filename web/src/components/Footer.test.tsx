import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { expect, test } from "vitest";
import { Footer } from "./Footer";

function renderFooter(props: { version?: string; commit?: string }) {
  const router = createMemoryRouter([{ path: "/", element: <Footer {...props} /> }]);
  render(<RouterProvider router={router} />);
}

test("names the version and links the commit it was built from", () => {
  const commit = "0123456789abcdef0123456789abcdef01234567";
  renderFooter({ version: "v0.3.0", commit });
  expect(screen.getByText("v0.3.0")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "(0123456)" })).toHaveAttribute(
    "href",
    `https://github.com/Radiergummi/tsrelay/commit/${commit}`,
  );
});

test("leaves the commit out when it is unknown", () => {
  renderFooter({ version: "dev" });
  expect(screen.getByText("dev")).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: /^\(/ })).not.toBeInTheDocument();
});

test("links the licenses and the source, with the copyright", () => {
  renderFooter({});
  expect(screen.getByRole("link", { name: "Licenses" })).toHaveAttribute("href", "/licenses");
  expect(screen.getByRole("link", { name: "GitHub" })).toHaveAttribute(
    "href",
    "https://github.com/Radiergummi/tsrelay",
  );
  expect(screen.getByText("© 2026 Moritz Mazetti · Apache-2.0")).toBeInTheDocument();
});
