import { Card, Empty, Status } from '../components/ui';

export type Updates = {
  images: {
    container: string;
    stack: string;
    image: string;
    local_digest?: string;
    remote_digest?: string;
    behind: boolean;
    checked: boolean;
    reason?: string;
  }[];
  behind: number;
  packages: number;
  packages_known: boolean;
  reboot_required: boolean;
};

const short = (digest?: string) => (digest ? digest.replace('sha256:', '').slice(0, 12) : '—');

export function Updates({ updates }: { updates?: Updates }) {
  if (!updates) return <Empty>Comparing images against their registries…</Empty>;

  const behind = updates.images.filter((i) => i.behind);
  const current = updates.images.filter((i) => i.checked && !i.behind);
  const skipped = updates.images.filter((i) => !i.checked);

  return (
    <>
      <div className="grid">
        <Card title="Container images">
          <div className="stat">
            <span className="label">Behind their tag</span>
            <span className="value">{updates.behind}</span>
            <span className="sub">{current.length} up to date</span>
          </div>
        </Card>
        <Card title="System packages">
          <div className="stat">
            <span className="label">dnf updates</span>
            <span className="value">{updates.packages_known ? updates.packages : '—'}</span>
            <span className="sub">
              {updates.reboot_required ? 'reboot required' : 'no reboot needed'}
            </span>
          </div>
        </Card>
      </div>

      {behind.length > 0 && (
        <Card title={`${behind.length} containers running an older image`} bodyless>
          <div className="rows">
            {behind.map((i) => (
              <div className="row" key={i.container}>
                <div className="name">
                  <b>{i.container}</b>
                  <small>{i.image}</small>
                </div>
                <span className="tag mono">
                  {short(i.local_digest)} → {short(i.remote_digest)}
                </span>
                <Status tone="warn">behind</Status>
              </div>
            ))}
          </div>
        </Card>
      )}

      <Card title={`${current.length} up to date`} bodyless>
        <div className="rows">
          {current.map((i) => (
            <div className="row" key={i.container}>
              <div className="name">
                <b>{i.container}</b>
                <small>{i.image}</small>
              </div>
              <Status tone="up">current</Status>
            </div>
          ))}
        </div>
      </Card>

      {skipped.length > 0 && (
        <Card title={`${skipped.length} not compared`} bodyless>
          <div className="rows">
            {skipped.map((i) => (
              <div className="row" key={i.container}>
                <div className="name">
                  <b>{i.container}</b>
                  <small>{i.image}</small>
                </div>
                <span className="tag">{i.reason}</span>
              </div>
            ))}
          </div>
        </Card>
      )}
    </>
  );
}
