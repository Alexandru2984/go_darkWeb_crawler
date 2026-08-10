import { csrfHeaders } from "./csrf.js";

/**
 * apiFetch is the single door to the API.
 *
 * It always sends same-origin credentials (the session lives in an HttpOnly
 * cookie this code cannot read) and attaches the double-submit CSRF header on
 * unsafe methods. Centralising it means a new call site cannot forget either.
 */
export async function apiFetch(url, options = {}) {
  const { json, ...rest } = options;
  const headers = {
    ...(options.headers || {}),
    ...csrfHeaders(options.method),
  };
  let body = rest.body;

  if (json !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(json);
  }

  const response = await fetch(url, {
    ...rest,
    body,
    credentials: "same-origin",
    headers,
  });

  // A 401 means the session ended — expired, revoked from another device, or
  // signed out elsewhere. Tell the app so it can drop back to the login form
  // instead of leaving a dashboard on screen that no longer works.
  if (response.status === 401) {
    onUnauthorized.forEach((fn) => fn());
  }
  return response;
}

const onUnauthorized = new Set();

/** Register a callback for "the server says this session is over". */
export function onSessionLost(fn) {
  onUnauthorized.add(fn);
  return () => onUnauthorized.delete(fn);
}

/**
 * readJSON parses a response body, tolerating an empty or non-JSON body so a
 * proxy error page cannot turn into an unhandled exception mid-render.
 */
export async function readJSON(response) {
  try {
    return await response.json();
  } catch {
    return {};
  }
}

/**
 * apiJSON performs a request and returns { ok, status, data }. Callers that
 * only care about the payload and whether it worked use this instead of
 * repeating the same three lines.
 */
export async function apiJSON(url, options = {}) {
  const response = await apiFetch(url, options);
  return {
    ok: response.ok,
    status: response.status,
    data: await readJSON(response),
  };
}
