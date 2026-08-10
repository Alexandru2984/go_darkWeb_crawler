import { describe, expect, it } from "vitest";
import { tokenFromFragment } from "./linkTokens.js";

const token = "0123456789abcdef0123456789abcdef";

describe("tokenFromFragment", () => {
  it("reads a safe token from the expected fragment", () => {
    expect(
      tokenFromFragment(
        `https://example.test/reset-password#token=${token}`,
        "/reset-password",
      ),
    ).toBe(token);
  });

  it("never falls back to a query-string credential", () => {
    expect(
      tokenFromFragment(
        `https://example.test/reset-password?token=${token}`,
        "/reset-password",
      ),
    ).toBe("");
  });

  it("rejects the wrong route and unsafe token characters", () => {
    expect(
      tokenFromFragment(
        `https://example.test/other#token=${token}`,
        "/reset-password",
      ),
    ).toBe("");
    expect(
      tokenFromFragment(
        "https://example.test/reset-password#token=%3Cscript%3Ebad",
        "/reset-password",
      ),
    ).toBe("");
  });
});
