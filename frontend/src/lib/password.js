// Client-side password validation, mirroring the backend's ValidatePassword
// (internal/api/validate.go): 10–72 chars and at least 3 of the 4 character
// classes {lowercase, uppercase, digit, symbol}. Plus a confirm-match check.
// This is UX only — the server re-validates authoritatively.

export function passwordStrengthError(password) {
  if (password.length < 10 || password.length > 72) {
    return 'Password must be between 10 and 72 characters.'
  }
  let classes = 0
  if (/[a-z]/.test(password)) classes++
  if (/[A-Z]/.test(password)) classes++
  if (/[0-9]/.test(password)) classes++
  if (/[^a-zA-Z0-9]/.test(password)) classes++
  if (classes < 3) {
    return 'Password must combine at least 3 of: lowercase, uppercase, digits, symbols.'
  }
  return ''
}

// validateNewPassword returns an error string, or '' if the password is valid
// and matches its confirmation.
export function validateNewPassword(password, confirm) {
  const strengthErr = passwordStrengthError(password)
  if (strengthErr) return strengthErr
  if (password !== confirm) return 'Passwords do not match.'
  return ''
}
