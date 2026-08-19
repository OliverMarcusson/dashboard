import { useEffect, useState } from 'react';
import { api } from '../api';

type RangeResponse = { points: { ts: number; value: number }[] };

/**
 * Loads stored history for one series. Server-side storage means the shape of
 * the last hour is the same on every device, not whatever this browser happened
 * to observe.
 */
export function useHistory(metric: string, minutes = 60, kind = 'host', subject = '') {
  const [points, setPoints] = useState<number[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const params = new URLSearchParams({ metric, kind, subject, minutes: String(minutes) });
        const res = await api<RangeResponse>(`/api/metrics/range?${params}`);
        if (!cancelled) setPoints(res.points.map((p) => p.value));
      } catch {
        // A missing series is not an error worth showing; the chart stays empty.
      }
    };
    load();
    const timer = setInterval(load, 30000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [metric, minutes, kind, subject]);

  return points;
}
