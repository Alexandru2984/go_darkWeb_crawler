// Client-side password validation, mirroring the backend's ValidatePassword
// (internal/api/validate.go): 10–256 chars and at least 3 of the 4 character
// classes {lowercase, uppercase, digit, symbol}. Plus a confirm-match check.
// This is UX only — the server re-validates authoritatively.
//
// The upper bound was 72 while passwords were hashed with bcrypt, which
// truncates there. Argon2id does not, so passphrases are no longer rejected for
// being long. Keep this number in step with MaxPasswordChars on the server.

export const MAX_PASSWORD_CHARS = 256;

export function passwordStrengthError(password) {
  if (password.length < 10 || password.length > MAX_PASSWORD_CHARS) {
    return `Password must be between 10 and ${MAX_PASSWORD_CHARS} characters.`;
  }
  let classes = 0;
  if (/[a-z]/.test(password)) classes++;
  if (/[A-Z]/.test(password)) classes++;
  if (/[0-9]/.test(password)) classes++;
  if (/[^a-zA-Z0-9]/.test(password)) classes++;
  if (classes < 3) {
    return "Password must combine at least 3 of: lowercase, uppercase, digits, symbols.";
  }
  return "";
}

// validateNewPassword returns an error string, or '' if the password is valid
// and matches its confirmation.
export function validateNewPassword(password, confirm) {
  const strengthErr = passwordStrengthError(password);
  if (strengthErr) return strengthErr;
  if (password !== confirm) return "Passwords do not match.";
  return "";
}
