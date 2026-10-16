import { screen, within } from "@testing-library/react";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { ChannelPage } from "./ChannelPage";

function open(route: string, me = fixtures.me) {
  return renderWithProviders(<ChannelPage />, { route, path: "/channels/:name", context: me });
}

test("shows the viewer's hooks in the channel and what they may do on it", async () => {
  open("/channels/default");
  expect(await screen.findByRole("link", { name: "payments/done" })).toHaveAttribute(
    "href",
    `/hooks/${fixtures.hook.id}`,
  );
  expect(screen.getByRole("link", { name: "busy" })).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "theirs" })).not.toBeInTheDocument();
  expect(screen.getByText(/You may receive on this channel, and may not send on it/)).toBeVisible();
});

test("admins with ?all=true see every user's hooks with their owners", async () => {
  open("/channels/default?all=true");
  const row = (await screen.findByRole("link", { name: "theirs" })).closest("tr")!;
  expect(within(row).getByText("other@example.com")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Only your hooks" })).toHaveAttribute(
    "href",
    "/channels/default",
  );
});

test("a channel without hooks is still a channel", async () => {
  open("/channels/staging.eu");
  expect(await screen.findByText("You have no hooks here")).toBeInTheDocument();
  expect(screen.getByText(/You may receive on this channel/)).toBeInTheDocument();
});

test("an invalid name is the relay's 404", async () => {
  open("/channels/Not_A_Channel");
  expect(await screen.findByRole("alert")).toHaveTextContent("no such channel");
});
