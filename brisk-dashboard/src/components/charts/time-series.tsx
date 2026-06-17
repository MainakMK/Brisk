import {
  Area,
  AreaChart,
  Line,
  LineChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  Legend,
} from "recharts";

export interface SeriesDef {
  key: string;
  label: string;
  color: string; // CSS var or hex
  stackId?: string;
}

/** Generic Voltage-themed time-series. type=area|line, supports stacked areas.
   Restrained styling (2px lines, gradient fill ~22%, muted grid/axes), tooltip
   with exact values + timestamp, responsive. */
export function TimeSeries({
  data,
  series,
  type = "area",
  valueFormatter = (v) => String(v),
  xFormatter = (v) => String(v),
  labelFormatter,
  height = 240,
  showLegend = false,
  yWidth = 52,
}: {
  data: Array<Record<string, unknown>>;
  series: SeriesDef[];
  type?: "area" | "line";
  valueFormatter?: (v: number) => string;
  xFormatter?: (v: string) => string;
  labelFormatter?: (v: string) => string;
  height?: number;
  showLegend?: boolean;
  yWidth?: number;
}) {
  const grid = (
    <CartesianGrid stroke="var(--border)" strokeDasharray="0" vertical={false} />
  );
  const xAxis = (
    <XAxis
      dataKey="time"
      stroke="var(--muted-foreground)"
      fontSize={11}
      tickLine={false}
      axisLine={false}
      minTickGap={40}
      tickFormatter={xFormatter}
    />
  );
  const yAxis = (
    <YAxis
      stroke="var(--muted-foreground)"
      fontSize={11}
      tickLine={false}
      axisLine={false}
      width={yWidth}
      tickFormatter={valueFormatter}
    />
  );
  const tooltip = (
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
      labelFormatter={(l) => (labelFormatter ? labelFormatter(String(l)) : xFormatter(String(l)))}
      formatter={(value: unknown, name: unknown) => {
        const def = series.find((s) => s.key === name);
        return [valueFormatter(Number(value)), def?.label ?? String(name)] as [string, string];
      }}
    />
  );
  const legend = showLegend ? (
    <Legend
      iconType="plainline"
      wrapperStyle={{ fontSize: 12, color: "var(--muted-foreground)" }}
      formatter={(value) => series.find((s) => s.key === value)?.label ?? value}
    />
  ) : null;

  return (
    <ResponsiveContainer width="100%" height={height}>
      {type === "area" ? (
        <AreaChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: -8 }}>
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`grad-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.25} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          {grid}
          {xAxis}
          {yAxis}
          {tooltip}
          {legend}
          {series.map((s) => (
            <Area
              key={s.key}
              type="monotone"
              dataKey={s.key}
              name={s.key}
              stackId={s.stackId}
              stroke={s.color}
              strokeWidth={2}
              fill={`url(#grad-${s.key})`}
              dot={false}
              activeDot={{ r: 3, fill: s.color }}
              isAnimationActive={false}
            />
          ))}
        </AreaChart>
      ) : (
        <LineChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: -8 }}>
          {grid}
          {xAxis}
          {yAxis}
          {tooltip}
          {legend}
          {series.map((s) => (
            <Line
              key={s.key}
              type="monotone"
              dataKey={s.key}
              name={s.key}
              stroke={s.color}
              strokeWidth={2}
              dot={false}
              activeDot={{ r: 3, fill: s.color }}
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      )}
    </ResponsiveContainer>
  );
}
