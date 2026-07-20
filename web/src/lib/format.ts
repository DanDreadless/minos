// Shared display formatting helpers (no lore here — plain rendering).

// fmtLogTime renders a query-log timestamp: time-only while the entry is
// from today (the live tail would repeat today's date on every row), and
// date + time once it is from another day — which any filtered or paged
// history view quickly is. Pair with fmtLogTimeFull in a title attribute
// so the complete timestamp is always one hover away.
export function fmtLogTime(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  const today =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();
  return today ? d.toLocaleTimeString() : `${d.toLocaleDateString()} ${d.toLocaleTimeString()}`;
}

// fmtLogTimeFull always renders the complete date + time.
export function fmtLogTimeFull(iso: string): string {
  return new Date(iso).toLocaleString();
}
