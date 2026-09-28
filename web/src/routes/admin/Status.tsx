import type { ReactNode } from "react";
import { useAdminStatus, useRetryTask, useStartBackup, type AdminStatus, type AdminTaskSummary } from "../../api/admin";
import { errorMessage } from "../../api/errors";
import { formatDateTime } from "../../format";

// ADR-033: what an administrator needs to keep the class running — the
// database schema, the task queue, the model, disk space and backups.
// Nothing here shows lesson content: tasks appear by kind and outcome
// only. The worker reports what the api cannot see (model, backup
// directory) as heartbeats; a heartbeat older than staleAfterMs means the
// worker has stopped reporting.
const staleAfterMs = 2 * 60_000;
// A daily backup is late once the newest copy is older than a day plus a
// margin for the nightly slot.
const backupLateAfterMs = 26 * 60 * 60_000;

const taskStatusLabels: Record<string, string> = {
  waiting: "ожидает",
  pending: "в очереди",
  leased: "выполняется",
  done: "готово",
  failed: "ошибка",
  dead_letter: "исчерпаны попытки",
  cancelled: "отменена",
};

const kindLabels: Record<string, string> = {
  "backup.run": "Резервная копия",
  "audit.prune": "Очистка журнала аудита",
  "lesson.close": "Закрытие занятия",
  "assessment.evaluate": "Автооценка",
  "report.build": "PDF-отчёт",
  "caller.reply": "Ответ заявителя",
  "caller.warmup": "Прогрев модели",
  "system.noop": "Проверка очереди",
};

export function StatusRoute() {
  const status = useAdminStatus();
  const backup = useStartBackup();
  const retry = useRetryTask();

  return (
    <section className="admin-status">
      <div className="page-heading">
        <div>
          <h1>Состояние</h1>
          <p>Обновляется каждые 10 секунд{status.data ? ` · сервер: ${formatDateTime(status.data.server_time)}` : ""}</p>
        </div>
      </div>
      {status.isPending && <p>Загрузка…</p>}
      {status.isError && <p className="error">{errorMessage(status.error)}</p>}
      {status.data && (
        <>
          <Overview status={status.data} />
          <BackupPanel
            status={status.data}
            onStart={() => backup.mutate()}
            starting={backup.isPending}
            error={backup.isError ? errorMessage(backup.error) : null}
          />
          <FailedTasks
            tasks={status.data.failed_tasks}
            onRetry={(id) => retry.mutate(id)}
            retrying={retry.isPending ? retry.variables : undefined}
            error={retry.isError ? errorMessage(retry.error) : null}
          />
          <TaskCounts tasks={status.data.tasks} />
        </>
      )}
    </section>
  );
}

function isStale(checkedAt: string | null | undefined, now: string): boolean {
  if (!checkedAt) return true;
  return new Date(now).getTime() - new Date(checkedAt).getTime() > staleAfterMs;
}

function Overview({ status }: { status: AdminStatus }) {
  const schemaOk = status.db_schema_version === status.expected_schema_version;
  const llm = status.models.llm;
  const workersAlive = status.workers.filter((w) => !isStale(w.checked_at, status.server_time));
  return (
    <div className="status-cards">
      <StatusCard title="База данных" ok={schemaOk}>
        Схема {status.db_schema_version}
        {!schemaOk && ` — ожидается ${status.expected_schema_version}`}
      </StatusCard>
      <StatusCard title="Worker" ok={workersAlive.length > 0}>
        {status.workers.length === 0
          ? "не отвечает"
          : status.workers.map((w) => `${w.id}${w.role ? ` (${w.role})` : ""}: ${formatDateTime(w.checked_at)}`).join(", ")}
      </StatusCard>
      <StatusCard title="Модель" ok={llm ? llm.status === "ok" && !isStale(llm.checked_at, status.server_time) : null}>
        {llm ? `${llm.status === "ok" ? "отвечает" : "недоступна"}${llm.model ? ` · ${llm.model}` : ""}` : "не используется"}
      </StatusCard>
      <StatusCard title="Место под записи" ok={status.disk_free_bytes === null ? null : status.disk_free_bytes > 1 << 30}>
        {formatBytes(status.disk_free_bytes)} свободно
      </StatusCard>
    </div>
  );
}

function StatusCard({ title, ok, children }: { title: string; ok: boolean | null; children: ReactNode }) {
  const tone = ok === null ? "neutral" : ok ? "ok" : "bad";
  return (
    <div className={`status-card status-${tone}`}>
      <span>{title}</span>
      <strong>{children}</strong>
    </div>
  );
}

function BackupPanel(props: { status: AdminStatus; onStart: () => void; starting: boolean; error: string | null }) {
  const { backup, server_time: now } = props.status;
  const active = backup.runs.some((run) => run.status === "pending" || run.status === "leased");
  const late = backup.enabled && (!backup.last_backup_at || new Date(now).getTime() - new Date(backup.last_backup_at).getTime() > backupLateAfterMs);
  return (
    <div className="status-panel">
      <div className="status-panel-heading">
        <h2>Резервные копии</h2>
        <button type="button" onClick={props.onStart} disabled={!backup.enabled || active || props.starting}>
          {active ? "Копия создаётся…" : "Создать копию сейчас"}
        </button>
      </div>
      {!backup.enabled && <p className="notice">Каталог копий не настроен: у worker'а не задан BACKUP_DIR.</p>}
      {late && <p className="error">Последней удачной копии больше суток. Проверьте неудачные задачи ниже.</p>}
      {props.error && <p className="error">{props.error}</p>}
      {backup.enabled && (
        <p>
          Последняя копия: {formatDateTime(backup.last_backup_at)} · свободно в каталоге копий: {formatBytes(backup.free_bytes)}
        </p>
      )}
      {backup.copies.length > 0 && (
        <table>
          <thead>
            <tr><th>Копия</th><th>Создана</th><th>Размер</th></tr>
          </thead>
          <tbody>
            {backup.copies.map((copy) => (
              <tr key={copy.name}>
                <td><code>{copy.name}</code></td>
                <td>{formatDateTime(copy.created_at)}</td>
                <td>{formatBytes(copy.size_bytes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function FailedTasks(props: { tasks: AdminTaskSummary[]; onRetry: (id: string) => void; retrying: string | undefined; error: string | null }) {
  return (
    <div className="status-panel">
      <h2>Неудачные задачи</h2>
      {props.error && <p className="error">{props.error}</p>}
      {props.tasks.length === 0 ? (
        <p>Нет.</p>
      ) : (
        <table>
          <thead>
            <tr><th>Задача</th><th>Состояние</th><th>Попытки</th><th>Код ошибки</th><th>Когда</th><th></th></tr>
          </thead>
          <tbody>
            {props.tasks.map((task) => (
              <tr key={task.id}>
                <td>{kindLabels[task.kind] ?? task.kind}</td>
                <td>{taskStatusLabels[task.status] ?? task.status}</td>
                <td>{task.attempts} из {task.max_attempts}</td>
                <td><code>{task.last_error_code ?? "—"}</code></td>
                <td>{formatDateTime(task.terminal_at)}</td>
                <td>
                  <button type="button" onClick={() => props.onRetry(task.id)} disabled={props.retrying === task.id}>
                    Повторить
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function TaskCounts({ tasks }: { tasks: AdminStatus["tasks"] }) {
  const kinds = Object.keys(tasks).sort();
  const statuses = ["waiting", "pending", "leased", "done", "failed", "dead_letter", "cancelled"];
  return (
    <div className="status-panel">
      <h2>Очередь задач</h2>
      {kinds.length === 0 ? (
        <p>Задач ещё не было.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Вид</th>
              {statuses.map((s) => <th key={s}>{taskStatusLabels[s]}</th>)}
            </tr>
          </thead>
          <tbody>
            {kinds.map((kind) => (
              <tr key={kind}>
                <td>{kindLabels[kind] ?? kind}</td>
                {statuses.map((s) => <td key={s}>{tasks[kind]?.[s] ?? 0}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined) return "—";
  const units = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toLocaleString("ru-RU", { maximumFractionDigits: unit === 0 ? 0 : 1 })} ${units[unit]}`;
}
