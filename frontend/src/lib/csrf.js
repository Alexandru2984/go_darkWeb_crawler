export const readCookie = (cookieString, name) => {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = cookieString.match(new RegExp(`(?:^|; )${escaped}=([^;]*)`));
  if (!match) return "";
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return "";
  }
};

export const csrfHeaders = (
  method,
  cookieString = globalThis.document?.cookie || "",
) => {
  if (!method || method.toUpperCase() === "GET") return {};
  const token = readCookie(cookieString, "os_csrf");
  return token ? { "X-CSRF-Token": token } : {};
};
