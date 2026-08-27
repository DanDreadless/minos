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

// Host readings are all optional: a platform that cannot measure one omits
// it. These helpers take undefined and return an em dash, so absence is
// rendered as "not measured" rather than silently becoming a zero.

export function fmtBytes(v: number | undefined): string {
  if (v === undefined) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let n = v;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  // Sub-10 values keep a decimal so "1.4 GB" does not collapse to "1 GB".
  return (n < 10 && i > 0 ? n.toFixed(1) : Math.round(n).toString()) + ' ' + units[i];
}

export function fmtPercentValue(v: number | undefined, digits = 0): string {
  if (v === undefined) return '—';
  return v.toFixed(digits) + '%';
}

export function fmtNumber(v: number | undefined, digits = 2): string {
  if (v === undefined) return '—';
  return v.toFixed(digits);
}

export function fmtCelsius(v: number | undefined): string {
  if (v === undefined) return '—';
  return v.toFixed(1) + '°C';
}

// Coarse by design: an uptime panel wants "12 days", not "12d 4h 31m 9s".
export function fmtUptime(seconds: number | undefined): string {
  if (seconds === undefined) return '—';
  const d = Math.floor(seconds / 86400);
  if (d >= 1) return d + (d === 1 ? ' day' : ' days');
  const h = Math.floor(seconds / 3600);
  if (h >= 1) return h + (h === 1 ? ' hour' : ' hours');
  const m = Math.max(1, Math.floor(seconds / 60));
  return m + (m === 1 ? ' minute' : ' minutes');
}
