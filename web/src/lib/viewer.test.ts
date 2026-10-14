import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { isAdmin, isReceiver, isUser, owns } from "./viewer";

test("roles and ownership", () => {
  expect(isReceiver(fixtures.me) && isAdmin(fixtures.me)).toBe(true);
  expect(isReceiver(fixtures.meAdmin)).toBe(false);
  expect(isAdmin(fixtures.meAdmin)).toBe(true);
  expect(
    isUser(fixtures.meNobody) && !isReceiver(fixtures.meNobody) && !isAdmin(fixtures.meNobody),
  ).toBe(true);
  expect(owns(fixtures.me, fixtures.hook)).toBe(true);
  expect(owns(fixtures.me, fixtures.theirs)).toBe(false);
  expect(owns({ ...fixtures.me, login: undefined }, fixtures.hook)).toBe(false);
});
