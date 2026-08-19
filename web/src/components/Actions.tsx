import { useState } from 'react';
import { Play, RotateCw, Square } from 'lucide-react';
import { api } from '../api';
import { Confirm } from './ui';

export type ActionKind = 'container' | 'stack' | 'unit';

type Job = { id: string; status: string; error?: string; output: string };

const VERBS = [
  { verb: 'start', Icon: Play, label: 'Start' },
  { verb: 'restart', Icon: RotateCw, label: 'Restart' },
  { verb: 'stop', Icon: Square, label: 'Stop', danger: true },
] as const;

/**
 * Start / restart / stop for any target. The buttons are not configured per
 * service — they exist because the target is a container, stack, or unit.
 */
export function ActionButtons({
  kind,
  target,
  name,
  running,
  onDone,
}: {
  kind: ActionKind;
  target: string;
  name: string;
  running: boolean;
  onDone?: (job: Job) => void;
}) {
  const [pending, setPending] = useState<(typeof VERBS)[number] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  async function run() {
    if (!pending) return;
    setBusy(true);
    setError('');
    try {
      const job = await api<Job>('/api/actions/run', {
        action_id: `${kind}.${pending.verb}.${target}`,
        confirmed: true,
      });
      onDone?.(job);
      if (job.status === 'failed') setError(job.error || 'The action failed.');
      setPending(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  const noun = kind === 'unit' ? 'service' : kind;

  return (
    <>
      <div className="actions">
        {VERBS.map((v) => (
          <button
            key={v.verb}
            className={`icon-btn${'danger' in v && v.danger ? ' danger' : ''}`}
            title={`${v.label} ${name}`}
            aria-label={`${v.label} ${name}`}
            disabled={v.verb === 'start' ? running : !running}
            onClick={() => setPending(v)}
          >
            <v.Icon size={15} />
          </button>
        ))}
      </div>

      {pending && (
        <Confirm
          question={`${pending.label} the ${name} ${noun}?`}
          detail={error || undefined}
          busy={busy}
          onConfirm={run}
          onCancel={() => {
            setPending(null);
            setError('');
          }}
        />
      )}
    </>
  );
}
