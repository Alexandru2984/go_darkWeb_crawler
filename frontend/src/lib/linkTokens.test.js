import { describe, it, expect, vi, afterEach } from "vitest";
import {
  tokenFromFragment,
  consumeLinkToken,
  takeLinkToken,
} from "./linkTokens.js";

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

describe("consumeLinkToken", () => {
  afterEach(() => {
    takeLinkToken(); // drain, so one test cannot leak into the next
  });

  it("captures a reset token and strips it from the address bar", () => {
    const token = "a".repeat(32);
    const replace = vi.fn();
    expect(
      consumeLinkToken(
        `https://go.example.com/reset-password#token=${token}`,
        replace,
      ),
    ).toBe(token);
    // The replacement is what stops the credential entering history.
    expect(replace).toHaveBeenCalledWith("/reset-password");
    expect(takeLinkToken()).toBe(token);
  });

  it("captures a verification token on its own path", () => {
    const token = "b".repeat(24);
    const replace = vi.fn();
    expect(
      consumeLinkToken(
        `https://go.example.com/verify-account#token=${token}`,
        replace,
      ),
    ).toBe(token);
    expect(replace).toHaveBeenCalledWith("/verify-account");
    expect(takeLinkToken()).toBe(token);
  });

  it("ignores a token offered on an unrelated path", () => {
    const replace = vi.fn();
    expect(
      consumeLinkToken(
        `https://go.example.com/dashboard#token=${"c".repeat(32)}`,
        replace,
      ),
    ).toBe("");
    expect(replace).not.toHaveBeenCalled();
    expect(takeLinkToken()).toBe("");
  });

  it("ignores a malformed token rather than forwarding it to the API", () => {
    const replace = vi.fn();
    expect(
      consumeLinkToken(
        "https://go.example.com/reset-password#token=too-short",
        replace,
      ),
    ).toBe("");
    expect(replace).not.toHaveBeenCalled();
    expect(takeLinkToken()).toBe("");
  });

  it("yields the token only once, so a later mount cannot replay it", () => {
    const token = "d".repeat(32);
    consumeLinkToken(
      `https://go.example.com/reset-password#token=${token}`,
      vi.fn(),
    );
    expect(takeLinkToken()).toBe(token);
    expect(takeLinkToken()).toBe("");
  });
});
