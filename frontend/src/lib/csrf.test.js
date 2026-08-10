import { describe, expect, it } from "vitest";
import { csrfHeaders, readCookie } from "./csrf.js";

describe("CSRF helpers", () => {
  it("reads and decodes the dedicated cookie", () => {
    expect(readCookie("other=x; os_csrf=abc%20123; final=y", "os_csrf")).toBe(
      "abc 123",
    );
  });

  it("adds the token only to unsafe methods", () => {
    expect(csrfHeaders("GET", "os_csrf=secret")).toEqual({});
    expect(csrfHeaders("POST", "os_csrf=secret")).toEqual({
      "X-CSRF-Token": "secret",
    });
  });
});
