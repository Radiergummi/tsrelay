import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { ChannelsPage } from "./ChannelsPage";

function renderPage(route = "/channels", me = fixtures.me) {
  return renderWithProviders(<ChannelsPage />, { route, path: "/channels", context: me });
}

test("shows the viewer's patterns and their hooks' channels with the patterns covering them", async () => {
  renderPage();
  const row = (await screen.findByRole("link", { name: "prod" })).closest("tr")!;
  expect(within(row).getByRole("link", { name: "prod" })).toHaveAttribute("href", "/channels/prod");
  expect(within(row).getByRole("link", { name: "idle" })).toHaveAttribute(
    "href",
    `/hooks/${fixtures.idle.id}`,
  );
  expect(within(row).getByText("prod", { selector: "code" })).toBeInTheDocument();
  const def = screen.getByRole("link", { name: "default" }).closest("tr")!;
  expect(within(def).getAllByRole("link")).toHaveLength(4); // the chip and three hooks
  expect(screen.getByText("You receive on").nextSibling).toHaveTextContent("defaultprodstaging.*");
  expect(screen.getByText("You send on").nextSibling).toHaveTextContent("none");
});

test("admins switch to every channel in use, with hook counts", async () => {
  const { router } = renderPage();
  await userEvent.click(await screen.findByRole("link", { name: "Every user's channels" }));
  expect(router.state.location.search).toBe("?all=true");
  const row = (await screen.findByRole("link", { name: "default" })).closest("tr")!;
  expect(within(row).getByText("4")).toBeInTheDocument();
  expect(screen.queryByText("Received through")).not.toBeInTheDocument();
});

test("non-admins get no switch, and ?all=true is ignored", async () => {
  renderPage("/channels?all=true", { ...fixtures.me, roles: { receiver: ["*"] } });
  const row = (await screen.findByRole("link", { name: "default" })).closest("tr")!;
  expect(within(row).getByRole("link", { name: "busy" })).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "Every user's channels" })).not.toBeInTheDocument();
});
