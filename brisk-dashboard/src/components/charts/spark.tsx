import { Area, AreaChart, ResponsiveContainer } from "recharts";

/** Tiny inline sparkline for KPI cards (no axes, no grid). */
export function Spark({
  data,
  color = "var(--chart-1)",
  height = 36,
}: {
  data: number[];
  color?: string;
  height?: number;
}) {
  const points = data.map((v, i) => ({ i, v }));
  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={points} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id="briskSpark" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.35} />
            <stop offset="100%" stopColor={color} stopOpacity={0} />
          </linearGradient>
        </defs>
        <Area type="monotone" dataKey="v" stroke={color} strokeWidth={1.5} fill="url(#briskSpark)" dot={false} />
      </AreaChart>
    </ResponsiveContainer>
  );
}
