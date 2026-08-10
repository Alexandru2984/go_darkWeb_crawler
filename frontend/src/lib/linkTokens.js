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
