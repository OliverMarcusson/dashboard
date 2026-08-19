import { useEffect, useState } from 'react';

export type Envelope<T> = { topic: string; at: string; data: T };

type State = 'connecting' | 'open' | 'closed';

/**
 * Subscribes to hub topics over one WebSocket and returns the latest payload
 * per topic. The server replays the most recent message on connect, so the
 * first render has real data rather than a spinner.
 */
export function useTopics<T extends Record<string, unknown>>(topics: string[]) {
  const [data, setData] = useState<Partial<T>>({});
  const [state, setState] = useState<State>('connecting');
  const key = topics.join(',');

  useEffect(() => {
    let socket: WebSocket | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let attempt = 0;
    let closed = false;

    const connect = () => {
      if (closed) return;
      setState((s) => (s === 'open' ? s : 'connecting'));

      const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
      socket = new WebSocket(`${scheme}://${location.host}/ws?topics=${encodeURIComponent(key)}`);

      socket.onopen = () => {
        attempt = 0;
        setState('open');
      };

      socket.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data) as Envelope<unknown>;
          setData((prev) => ({ ...prev, [msg.topic]: msg.data }));
        } catch {
          // A malformed frame is not worth tearing the connection down for.
        }
      };

      socket.onclose = () => {
        if (closed) return;
        setState('closed');
        // Back off to 10s so a restarting server does not get hammered.
        const delay = Math.min(1000 * 2 ** attempt++, 10000);
        retry = setTimeout(connect, delay);
      };

      socket.onerror = () => socket?.close();
    };

    connect();
    return () => {
      closed = true;
      clearTimeout(retry);
      socket?.close();
    };
  }, [key]);

  return { data, state };
}
