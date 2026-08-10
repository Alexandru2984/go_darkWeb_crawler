import { describe, it, expect } from "vitest";
import {
  passwordStrengthError,
  validateNewPassword,
  MAX_PASSWORD_CHARS,
} from "./password.js";

describe("passwordStrengthError", () => {
  it("rejects too-short passwords", () => {
    expect(passwordStrengthError("Ab1!")).toMatch(/10 and 256/);
  });

  it("rejects passwords with fewer than 3 character classes", () => {
    expect(passwordStrengthError("alllowercaseonly")).toMatch(/at least 3/);
    // lowercase + uppercase only = 2 classes → still rejected
    expect(passwordStrengthError("lowerUPPERnodigit")).toMatch(/at least 3/);
  });

  it("accepts a strong password", () => {
    expect(passwordStrengthError("Str0ng-Pass!")).toBe("");
  });

  it("accepts a passphrase past the old 72-character bcrypt limit", () => {
    // 80 characters: rejected before Argon2id, valid now.
    expect(passwordStrengthError("Aa1!".repeat(20))).toBe("");
  });

  it("still rejects a password past the current bound", () => {
    const tooLong = "Aa1!".repeat(MAX_PASSWORD_CHARS / 4 + 1);
    expect(passwordStrengthError(tooLong)).toMatch(/10 and 256/);
  });
});

describe("validateNewPassword", () => {
  it("flags mismatched confirmation", () => {
    expect(validateNewPassword("Str0ng-Pass!", "different")).toBe(
      "Passwords do not match.",
    );
  });

  it("passes when strong and matching", () => {
    expect(validateNewPassword("Str0ng-Pass!", "Str0ng-Pass!")).toBe("");
  });

  it("surfaces strength error before match check", () => {
    expect(validateNewPassword("weak", "weak")).toMatch(/10 and 256/);
  });
});
