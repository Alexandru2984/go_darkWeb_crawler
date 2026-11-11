import { afterEach, describe, expect, it, vi } from "vitest";
import { apiJSON, onSessionLost } from "./api.js";

afterEach(() => vi.unstubAllGlobals());

describe("API failure handling", () => {
  it("returns a usable error when the network is unavailable", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    );
    await expect(apiJSON("/api/nodes")).resolves.toMatchObject({
      ok: false,
      status: 0,
      data: { error: expect.stringContaining("connection") },
    });
  });

  it("preserves cancellation so an obsolete request can be discarded", async () => {
    const aborted = new DOMException("Aborted", "AbortError");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(aborted));
    await expect(apiJSON("/api/nodes")).rejects.toBe(aborted);
  });

  it("keeps the session after a refused re-authentication", async () => {
    const lost = vi.fn();
    const stop = onSessionLost(lost);
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValue(
          new Response(JSON.stringify({ error: "Invalid credentials" }), {
            status: 403,
          }),
        ),
    );
    try {
      expect((await apiJSON("/api/privacy/purge")).status).toBe(403);
      expect(lost).not.toHaveBeenCalled();
    } finally {
      stop();
    }
  });

  it("announces an expired session", async () => {
    const lost = vi.fn();
    const stop = onSessionLost(lost);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("{}", { status: 401 })),
    );
    try {
      await apiJSON("/api/auth/me");
      expect(lost).toHaveBeenCalledOnce();
    } finally {
      stop();
    }
  });
});
