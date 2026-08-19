import { useEffect, useState } from 'react';
import { api } from '../api';
import { Card, Empty, Status } from '../components/ui';
import { since, timeOfDay } from '../lib/format';

export type Job = {
  id: string;
  action_id: string;
  label: string;
  actor: string;
  status: string;
  output: string;
  error?: string;
  started_at: string;
  finished_at: string | null;
};

export function Jobs({ live }: { live?: Job }) {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [open, setOpen] = useState<string>('');

  // Reload when a job is published, so the list reflects runs started anywhere.
  useEffect(() => {
    api<Job[]>('/api/jobs?limit=100').then(setJobs).catch(() => setJobs([]));
  }, [live?.id, live?.status]);

  if (!jobs.length) return <Empty>Nothing has been run yet.</Empty>;

  return (
    <Card bodyless title={`${jobs.length} recent ${jobs.length === 1 ? 'job' : 'jobs'}`}>
      <div className="rows">
        {jobs.map((job) => (
          <div key={job.id}>
            <div
              className="row"
              style={{ cursor: 'pointer' }}
              onClick={() => setOpen(open === job.id ? '' : job.id)}
            >
              <div className="name">
                <b>{job.label}</b>
                <small>
                  {timeOfDay(job.started_at)} · {since(job.started_at)} ago · {job.actor}
                </small>
              </div>
              <Status
                tone={job.status === 'succeeded' ? 'up' : job.status === 'failed' ? 'bad' : 'warn'}
              >
                {job.status}
              </Status>
            </div>
            {open === job.id && (job.output || job.error) && (
              <div className="logbox" style={{ maxHeight: '14rem', borderTop: '1px solid var(--line)' }}>
                {job.error && <div className="logline stderr">{job.error}</div>}
                {job.output
                  .split('\n')
                  .filter(Boolean)
                  .map((l, i) => (
                    <div key={i} className="logline stdout">
                      {l}
                    </div>
                  ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </Card>
  );
}
