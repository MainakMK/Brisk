import {
  Area,
  AreaChart as RechartsAreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

/** Themed area chart (Tremor-style restraint over Recharts).
   One accent (chart-1) + 12%-ish gradient fill, muted grid/axes via Voltage tokens.
   This is the single unified token system — no competing Tremor theme. */

export interface AreaPoint {
  label: string;
  value: number;
  [k: string]: string | number;
}

export function AreaChart({
  data,
  valueFormatter = (v) => String(v),
  height = 260,
  color = "var(--chart-1)",
}: {
  data: AreaPoint[];
  valueFormatter?: (v: number) => string;
  height?: number;
  color?: string;
}) {
  return (
    <ResponsiveContainer width="100%" height={height}>
      <RechartsAreaChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
        <defs>
          <linearGradient id="briskArea" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.28} />
            <stop offset="100%" stopColor={color} stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid stroke="var(--border)" strokeDasharray="0" vertical={false} />
        <XAxis
          dataKey="label"
          stroke="var(--muted-foreground)"
          fontSize={11}
          tickLine={false}
          axisLine={false}
          minTickGap={24}
        />
        <YAxis
          stroke="var(--muted-foreground)"
          fontSize={11}
          tickLine={false}
          axisLine={false}
          width={48}
          tickFormatter={valueFormatter}
        />
        <Tooltip
          cursor={{ stroke: "var(--border)" }}
          contentStyle={{
            background: "var(--popover)",
            border: "1px solid var(--border)",
            borderRadius: 8,
            color: "var(--popover-foreground)",
            fontSize: 12,
          }}
          labelStyle={{ color: "var(--muted-foreground)" }}
          formatter={(value) => [valueFormatter(Number(value)), ""] as [string, string]}
        />
        <Area
          type="monotone"
          dataKey="value"
          stroke={color}
          strokeWidth={2}
          fill="url(#briskArea)"
          dot={false}
          activeDot={{ r: 3, fill: color }}
        />
      </RechartsAreaChart>
    </ResponsiveContainer>
  );
}
