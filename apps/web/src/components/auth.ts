// Tiny session-scoped admin token store. Phase 5 hardening means every
// admin API call needs an Authorization header; this is where the
// browser holds the value between page loads.
//
// We use sessionStorage (not localStorage) on purpose: an admin token
// is the same security level as a long-lived cookie, and we want it to
// disappear when the tab closes.

const KEY = "tfm_admin_token";

export function getAdminToken(): string {
  return sessionStorage.getItem(KEY) ?? "";
}

export function setAdminToken(t: string): void {
  if (t) {
    sessionStorage.setItem(KEY, t);
  } else {
    sessionStorage.removeItem(KEY);
  }
}

export function clearAdminToken(): void {
  sessionStorage.removeItem(KEY);
}
