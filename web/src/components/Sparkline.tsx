type Props = {
  points: number[];
  max?: number;
  height?: number;
  tone?: 'accent' | 'ok' | 'warn' | 'crit';
};

/**
 * A filled area sparkline. Deliberately unlabelled — it carries shape, and the
 * number beside it carries the value.
 */
export function Sparkline({ points, max, height = 34, tone = 'accent' }: Props) {
  if (points.length < 2) {
    return <div style={{ height }} aria-hidden="true" />;
  }

  const width = 100;
  const ceiling = Math.max(max ?? Math.max(...points), 0.0001);
  const step = width / (points.length - 1);

  const coords = points.map((v, i) => {
    const x = i * step;
    const y = height - Math.max(0, Math.min(1, v / ceiling)) * (height - 2) - 1;
    return `${x.toFixed(2)},${y.toFixed(2)}`;
  });

  const color = `var(--${tone === 'accent' ? 'accent' : tone})`;

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      style={{ width: '100%', height, display: 'block' }}
      role="img"
      aria-label={`Trend, latest ${points[points.length - 1].toFixed(1)}`}
    >
      <polygon
        points={`0,${height} ${coords.join(' ')} ${width},${height}`}
        fill={color}
        opacity="0.13"
      />
      <polyline
        points={coords.join(' ')}
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
      />
      <circle
        cx={width}
        cy={coords[coords.length - 1].split(',')[1]}
        r="2"
        fill={color}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
