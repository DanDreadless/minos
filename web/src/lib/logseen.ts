// logSeen remembers the newest application-log warning/error the user has
// looked at, so the Logs nav item can show a dot for newer ones. Sequence
// numbers start again at 1 whenever Minos restarts, so the mark is tied to
// the server's boot (start time to the minute); a new boot with any alert
// counts as unseen. Kept per browser; when storage is unavailable (private
// windows) it simply lasts for the session.
import { writable } from 'svelte/store';

export interface Seen {
  seq: number;
  boot: number;
}

const KEY = 'minos.logSeen';

function load(): Seen {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '') as Seen;
    if (typeof v?.seq === 'number' && typeof v?.boot === 'number') return v;
  } catch {
    // Missing, malformed, or storage unavailable
  }
  return { seq: 0, boot: 0 };
}

export const logSeen = writable<Seen>(load());

logSeen.subscribe((v) => {
  try {
    localStorage.setItem(KEY, JSON.stringify(v));
  } catch {
    // Storage unavailable — the in-memory value still works
  }
});

// bootOf identifies a server run from its uptime: start time to the minute.
export function bootOf(uptimeSeconds: number): number {
  return Math.round((Date.now() / 1000 - uptimeSeconds) / 60);
}

// hasUnseen: an alert newer than the mark in this run, or any alert in a
// run the mark doesn't belong to. A minute of slack absorbs rounding.
export function hasUnseen(alertSeq: number, boot: number, seen: Seen): boolean {
  if (alertSeq === 0) return false;
  if (Math.abs(boot - seen.boot) > 1) return true;
  return alertSeq > seen.seq;
}

export function markLogsSeen(seq: number, boot: number): void {
  logSeen.update((cur) => (Math.abs(boot - cur.boot) > 1 || seq > cur.seq ? { seq, boot } : cur));
}
