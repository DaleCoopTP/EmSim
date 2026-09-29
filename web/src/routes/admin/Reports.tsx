import { useState } from "react";
import {
  failuresCsvUrl,
  useFailuresReport,
  useUsageReport,
  usageCsvUrl,
  type ReportPeriod,
  type UsageDay,
} from "../../api/admin";
import { taskKindLabels } from "../../adminLabels";
import { errorMessage } from "../../api/errors";
import { formatDateTime } from "../../format";

const utcDay = (offsetDays: number) => {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + offsetDays);
  return d.toISOString().slice(0, 10);
};

type Metric = { key: keyof Omit<UsageDay, "day">; label: string };
const metrics: Metric[] = [
  { key: "logins", label: "Входы" },
  { key: "active_users", label: "Активные пользователи" },
  { key: "lessons_started", label: "Начатые занятия" },
  { key: "lessons_stopped", label: "Остановленные занятия" },
  { key: "cards_closed", label: "Закрытые карточки" },
  { key: "auto_assessments", label: "Автооценки" },
  { key: "expert_assessments", label: "Экспертные оценки" },
  { key: "caller_replies", label: "Ответы ИИ-заявителя" },
  { key: "judge_calls", label: "Вызовы судьи" },
];

export function ReportsRoute() {
  const [tab, setTab] = useState<"usage" | "failures">("usage");
  const [draft, setDraft] = useState<ReportPeriod>({ from: utcDay(-29), to: utcDay(0) });
  const [period, setPeriod] = useState<ReportPeriod>(draft);
  const days = (Date.parse(draft.to) - Date.parse(draft.from)) / 86_400_000 + 1;
  const invalid = !(days >= 1) || days > 366;

  return (
    <section className="admin-reports">
      <div className="page-heading">
        <div>
          <h1>Отчёты</h1>
          <p>Использование и сбои за период. Только счётчики: без имён и без содержимого занятий. Сутки — по UTC.</p>
        </div>
      </div>
      <form
        className="status-panel audit-filter"
        onSubmit={(event) => {
          event.preventDefault();
          if (!invalid) setPeriod(draft);
        }}
      >
        <label>
          С даты
          <input type="date" value={draft.from} onChange={(e) => setDraft({ ...draft, from: e.target.value })} />
        </label>
        <label>
          По дату
          <input type="date" value={draft.to} onChange={(e) => setDraft({ ...draft, to: e.target.value })} />
        </label>
        <div className="audit-filter-actions">
          <button type="submit" disabled={invalid}>
            Показать
          </button>
          <a className="button-link" href={tab === "usage" ? usageCsvUrl(period) : failuresCsvUrl(period)} download={tab === "usage" ? "usage.csv" : "failures.csv"}>
            Скачать CSV
          </a>
        </div>
        {invalid && <p className="error">Период — от 1 до 366 дней.</p>}
      </form>
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={tab === "usage"} onClick={() => setTab("usage")}>
          Использование
        </button>
        <button role="tab" aria-selected={tab === "failures"} onClick={() => setTab("failures")}>
          Сбои
        </button>
      </div>
      {tab === "usage" ? <UsageTab period={period} /> : <FailuresTab period={period} />}
    </section>
  );
}

function UsageTab({ period }: { period: ReportPeriod }) {
  const usage = useUsageReport(period);
  const [metric, setMetric] = useState<Metric>(metrics[0]);
  if (usage.isPending) return <p>Загрузка…</p>;
  if (usage.isError) return <p className="error">{errorMessage(usage.error)}</p>;
  const report = usage.data;
  return (
    <>
      <div className="status-panel">
        <h2>Итоги за период</h2>
        <dl className="usage-totals">
          {metrics.map((m) => (
            <div key={m.key}>
              <dt>{m.label}</dt>
              <dd>{report.totals[m.key]}</dd>
            </div>
          ))}
        </dl>
      </div>
      <div className="status-panel">
        <div className="status-panel-heading">
          <h2>По дням</h2>
          <label>
            Показатель
            <select value={metric.key} onChange={(e) => setMetric(metrics.find((m) => m.key === e.target.value) ?? metrics[0])}>
              {metrics.map((m) => (
                <option key={m.key} value={m.key}>
                  {m.label}
                </option>
              ))}
            </select>
          </label>
        </div>
        <DayBars days={report.days} metric={metric} />
      </div>
      <div className="status-panel">
        <h2>По ролям</h2>
        <table>
          <thead>
            <tr>
              <th>Роль</th>
              <th>Входы</th>
              <th>Активные пользователи</th>
            </tr>
          </thead>
          <tbody>
            {report.by_role.map((r) => (
              <tr key={r.role}>
                <td>{r.role}</td>
                <td>{r.logins}</td>
                <td>{r.active_users}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <h2 style={{ marginTop: 16 }}>По типам упражнений</h2>
        <table>
          <thead>
            <tr>
              <th>Упражнение</th>
              <th>Начатые занятия</th>
              <th>Остановленные занятия</th>
              <th>Закрытые карточки</th>
            </tr>
          </thead>
          <tbody>
            {report.by_exercise.map((e) => (
              <tr key={e.exercise_type}>
                <td>{e.exercise_type === "dds_processing" ? "ДДС" : "Оператор 112"}</td>
                <td>{e.lessons_started}</td>
                <td>{e.lessons_stopped}</td>
                <td>{e.cards_closed}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function DayBars({ days, metric }: { days: UsageDay[]; metric: Metric }) {
  const width = 900;
  const height = 180;
  const gap = 2;
  const max = Math.max(1, ...days.map((d) => d[metric.key] as number));
  const barWidth = Math.max(2, (width - gap * (days.length - 1)) / days.length);
  return (
    <svg className="usage-chart" viewBox={`0 0 ${width} ${height + 20}`} role="img" aria-label={`${metric.label} по дням`}>
      <text x="0" y="10" className="usage-axis">
        {max}
      </text>
      <line x1="0" x2={width} y1={height} y2={height} className="usage-baseline" />
      {days.map((d, i) => {
        const value = d[metric.key] as number;
        const h = (value / max) * (height - 16);
        const x = i * (barWidth + gap);
        return (
          <g key={d.day}>
            <title>{`${d.day}: ${value}`}</title>
            <rect x={x} y={0} width={barWidth} height={height} fill="transparent" />
            {value > 0 && <rect className="usage-bar" x={x} y={height - h} width={barWidth} height={h} rx={Math.min(4, barWidth / 2)} />}
          </g>
        );
      })}
      <text x="0" y={height + 15} className="usage-axis">
        {days[0]?.day}
      </text>
      <text x={width} y={height + 15} textAnchor="end" className="usage-axis">
        {days[days.length - 1]?.day}
      </text>
    </svg>
  );
}

function FailuresTab({ period }: { period: ReportPeriod }) {
  const failures = useFailuresReport(period);
  if (failures.isPending) return <p>Загрузка…</p>;
  if (failures.isError) return <p className="error">{errorMessage(failures.error)}</p>;
  const r = failures.data;
  const counts = (title: string, rows: { key: string; count: number; last_at: string }[]) => (
    <div className="status-panel">
      <h2>{title}</h2>
      {rows.length === 0 ? (
        <p>Нет записей за период.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Что</th>
              <th>Раз</th>
              <th>Последний</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.key}>
                <td>{row.key || "—"}</td>
                <td>{row.count}</td>
                <td>{formatDateTime(row.last_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
  return (
    <>
      <div className="status-panel">
        <h2>Упавшие фоновые задачи</h2>
        {r.tasks.length === 0 ? (
          <p>Нет записей за период.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Задача</th>
                <th>Код ошибки</th>
                <th>Раз</th>
                <th>Последний</th>
              </tr>
            </thead>
            <tbody>
              {r.tasks.map((t) => (
                <tr key={`${t.kind}/${t.error_code}`}>
                  <td>{taskKindLabels[t.kind] ?? t.kind}</td>
                  <td>{t.error_code || "—"}</td>
                  <td>{t.count}</td>
                  <td>{formatDateTime(t.last_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {counts("Перезапуски сервера во время занятий", r.restarts)}
      {counts("Отклонённые входы по причине", r.rejected_logins)}
      {counts("Действия, завершившиеся ошибкой", r.audited_errors)}
      <div className="status-panel">
        <h2>Ответы 5xx</h2>
        <p>
          {r.server_errors.count} с запуска api ({formatDateTime(r.server_errors.since)}); не зависит от периода.
        </p>
      </div>
    </>
  );
}
