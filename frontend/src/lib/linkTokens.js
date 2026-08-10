const tokenPattern = /^[A-Za-z0-9_-]{16,128}$/;

// URL fragments are never included in HTTP requests or Referer headers. Email
// credentials live there only long enough for the SPA to read them and replace
// the current history entry with the token-free path.
export const tokenFromFragment = (href, expectedPath) => {
  try {
    const url = new URL(href);
    if (url.pathname !== expectedPath || !url.hash.startsWith("#")) return "";
    const token = new URLSearchParams(url.hash.slice(1)).get("token") || "";
    return tokenPattern.test(token) ? token : "";
  } catch {
    return "";
  }
};

// Routes that receive a credential in the fragment.
const tokenPaths = ["/reset-password", "/verify-account"];

// The token, held in memory only. Nothing writes it back into the URL, into
// storage, or into a request line — it is sent in a POST body and nowhere else.
let capturedToken = "";

/**
 * consumeLinkToken reads a fragment credential exactly once, at startup, and
 * immediately rewrites the address bar without it.
 *
 * Doing this before the router mounts matters: the replacement has to happen in
 * the same tick as the read, so the token never becomes a history entry the
 * back button (or a shared screenshot of the address bar) can return to.
 */
// defaultReplace is the browser implementation. It is injected rather than
// called directly so this module stays usable and testable without a DOM, and
// so a test can prove the address bar really was rewritten.
const defaultReplace = (path) => {
  if (typeof window !== "undefined" && window.history) {
    window.history.replaceState({}, document.title, path);
  }
};

export function consumeLinkToken(
  href = typeof window !== "undefined" ? window.location.href : "",
  replace = defaultReplace,
) {
  for (const path of tokenPaths) {
    const token = tokenFromFragment(href, path);
    if (token) {
      capturedToken = token;
      replace(path);
      return token;
    }
  }
  return "";
}

/** Returns the captured token and forgets it, so it cannot be replayed by a
 *  later component mount. */
export function takeLinkToken() {
  const token = capturedToken;
  capturedToken = "";
  return token;
}
