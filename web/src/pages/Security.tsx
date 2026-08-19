import { useCallback, useEffect, useState } from 'react';
import { Trash2 } from 'lucide-react';
import { api, type Passkey } from '../api';
import { Card, Confirm, Empty, Status } from '../components/ui';
import { since, timeOfDay } from '../lib/format';

type AuditEvent = {
  id: string;
  event: string;
  actor: string;
  detail: Record<string, unknown>;
  created_at: string;
};

export function Security() {
  const [keys, setKeys] = useState<Passkey[]>([]);
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [revoking, setRevoking] = useState<Passkey | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api<Passkey[]>('/api/security/passkeys').then(setKeys).catch(() => setKeys([]));
    api<AuditEvent[]>('/api/audit').then(setEvents).catch(() => setEvents([]));
  }, []);

  useEffect(load, [load]);

  async function revoke() {
    if (!revoking) return;
    setBusy(true);
    setError('');
    try {
      await api(`/api/security/passkeys/${revoking.id}`, { revoked: true }, 'PATCH');
      setRevoking(null);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Card title="Passkeys" actions={<span className="tag">{keys.length}</span>} bodyless>
        {keys.length === 0 ? (
          <Empty>No passkeys are enrolled.</Empty>
        ) : (
          <div className="rows">
            {keys.map((k) => (
              <div className="row" key={k.id}>
                <div className="name">
                  <b>{k.name}</b>
                  <small>
                    added {since(k.created_at)} ago
                    {k.last_used_at ? ` · last used ${since(k.last_used_at)} ago` : ' · never used'}
                    {k.backed_up ? ' · synced' : ''}
                  </small>
                </div>
                <Status tone={k.revoked ? 'down' : 'up'}>{k.revoked ? 'revoked' : 'active'}</Status>
                {!k.revoked && (
                  <div className="actions">
                    <button
                      className="icon-btn danger"
                      title={`Revoke ${k.name}`}
                      aria-label={`Revoke ${k.name}`}
                      onClick={() => setRevoking(k)}
                    >
                      <Trash2 size={15} />
                    </button>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </Card>

      <Card title="Audit log" bodyless>
        {events.length === 0 ? (
          <Empty>Nothing recorded yet.</Empty>
        ) : (
          <div className="rows">
            {events.slice(0, 60).map((e) => (
              <div className="row" key={e.id}>
                <div className="name">
                  <b>{e.event}</b>
                  <small>
                    {Object.entries(e.detail ?? {})
                      .map(([k, v]) => `${k}=${String(v)}`)
                      .join(' · ') || '—'}
                  </small>
                </div>
                <span className="tag">{e.actor || 'system'}</span>
                <span className="tag">{timeOfDay(e.created_at)}</span>
              </div>
            ))}
          </div>
        )}
      </Card>

      {revoking && (
        <Confirm
          question={`Revoke the ${revoking.name} passkey?`}
          detail={
            error ||
            'It will stop working immediately. This dashboard has no password fallback, so keep at least one passkey you can still use.'
          }
          busy={busy}
          onConfirm={revoke}
          onCancel={() => {
            setRevoking(null);
            setError('');
          }}
        />
      )}
    </>
  );
}
