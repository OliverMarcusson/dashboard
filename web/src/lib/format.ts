const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

export function bytes(n: number, digits = 1): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), UNITS.length - 1);
  const value = n / 1024 ** i;
  return `${value.toFixed(i === 0 ? 0 : digits)} ${UNITS[i]}`;
}

export function rate(bytesPerSecond: number): string {
  return `${bytes(bytesPerSecond)}/s`;
}

export function percent(n: number, digits = 0): string {
  return `${n.toFixed(digits)}%`;
}

/** Compact duration: 18d, 4h, 12m, 45s. */
export function duration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—';
  const d = Math.floor(seconds / 86400);
  if (d > 0) return `${d}d`;
  const h = Math.floor(seconds / 3600);
  if (h > 0) return `${h}h`;
  const m = Math.floor(seconds / 60);
  if (m > 0) return `${m}m`;
  return `${Math.floor(seconds)}s`;
}

/** "2 weeks ago" style, from an ISO timestamp or unix seconds. */
export function since(input: string | number | null | undefined): string {
  if (input === null || input === undefined) return '—';
  const ms = typeof input === 'number' ? input * 1000 : Date.parse(input);
  if (!Number.isFinite(ms)) return '—';
  return duration((Date.now() - ms) / 1000);
}

export function timeOfDay(input: string): string {
  const d = new Date(input);
  return Number.isNaN(d.getTime())
    ? '—'
    : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}
