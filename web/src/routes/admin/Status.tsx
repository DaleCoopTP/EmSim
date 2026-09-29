import { useState, type ReactNode } from "react";
import { useAdminStatus, useRetryTask, useSetMaintenance, useStartBackup, useStartIntegrity, type AdminStatus, type AdminTaskSummary } from "../../api/admin";
import { errorMessage } from "../../api/errors";
import { integritySectionLabels, taskKindLabels as kindLabels, taskStatusLabels } from "../../adminLabels";
import { formatBytes, formatDateTime } from "../../format";

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

export function StatusRoute() {
  const status = useAdminStatus();
  const backup = useStartBackup();
  const retry = useRetryTask();
  const integrity = useStartIntegrity();

  return (
    <section className="admin-status">
      <div className="page-heading">
        <div>
          <h1>Состояние</h1>
          <p>Обновляется каждые 10 секунд{status.data ? ` · сервер: ${formatDateTime(status.data.server_time)} · версия ${status.data.build}` : ""}</p>
        </div>
      </div>
      {status.isPending && <p>Загрузка…</p>}
      {status.isError && <p className="error">{errorMessage(status.error)}</p>}
      {status.data && (
        <>
          <Overview status={status.data} />
          <MaintenancePanel status={status.data} />
          <LoadPanel status={status.data} />
          <BackupPanel
            status={status.data}
            onStart={() => backup.mutate()}
            starting={backup.isPending}
            error={backup.isError ? errorMessage(backup.error) : null}
          />
          <IntegrityPanel
            status={status.data}
            onStart={() => integrity.mutate()}
            starting={integrity.isPending}
            started={integrity.isSuccess}
            error={integrity.isError ? errorMessage(integrity.error) : null}
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
  const stt = status.models.stt;
  const workersAlive = status.workers.filter((w) => !isStale(w.checked_at, status.server_time));
  // A worker that reports no version is an older build than the api.
  const versionsMatch = status.workers.every((w) => w.version === status.build);
  return (
    <div className="status-cards">
      <StatusCard title="База данных" ok={schemaOk}>
        Схема {status.db_schema_version}
        {!schemaOk && ` — ожидается ${status.expected_schema_version}`}
      </StatusCard>
      <StatusCard title="Worker" ok={workersAlive.length > 0 && versionsMatch}>
        {status.workers.length === 0
          ? "не отвечает"
          : status.workers.map((w) => `${w.id}${w.role ? ` (${w.role})` : ""}: ${formatDateTime(w.checked_at)}`).join(", ")}
        {!versionsMatch && " · версия отличается от api"}
      </StatusCard>
      <StatusCard title="Модель" ok={llm ? llm.status === "ok" && !isStale(llm.checked_at, status.server_time) : null}>
        {llm ? `${llm.status === "ok" ? "отвечает" : "недоступна"}${llm.model ? ` · ${llm.model}` : ""}` : "не используется"}
      </StatusCard>
      <StatusCard title="Распознавание речи" ok={stt ? stt.status === "ok" && !isStale(stt.checked_at, status.server_time) : null}>
        {stt ? `${stt.status === "ok" ? "отвечает" : "недоступно"}${stt.model ? ` · ${stt.model}` : ""}` : "не используется"}
      </StatusCard>
      <StatusCard title="Место под записи" ok={status.disk_free_bytes === null ? null : status.disk_free_bytes > 1 << 30}>
        {formatBytes(status.disk_free_bytes)} свободно
      </StatusCard>
    </div>
  );
}

function MaintenancePanel({ status }: { status: AdminStatus }) {
  const set = useSetMaintenance();
  const [reason, setReason] = useState("");
  const on = status.maintenance.enabled;
  return (
    <div className={`status-panel${on ? " maintenance-on" : ""}`}>
      <div className="status-panel-heading">
        <h2>Режим обслуживания</h2>
        <button type="button" disabled={set.isPending} onClick={() => set.mutate({ enabled: !on, reason: on ? "" : reason.trim() })}>
          {on ? "Выключить" : "Включить"}
        </button>
      </div>
      {on ? (
        <p>
          Включён {formatDateTime(status.maintenance.set_at)}
          {status.maintenance.reason ? ` · ${status.maintenance.reason}` : ""}. Новые занятия и предпросмотры не запускаются; идущие занятия продолжаются.
        </p>
      ) : (
        <>
          <p>Выключен. Включайте перед обновлением или восстановлением из копии: новые занятия не запустятся, идущие продолжатся.</p>
          <label>
            Что показать пользователям (необязательно)
            <input type="text" maxLength={200} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Например: обновление до 15:00" />
          </label>
        </>
      )}
      {set.isError && <p className="error">{errorMessage(set.error)}</p>}
    </div>
  );
}

function LoadPanel({ status }: { status: AdminStatus }) {
  const { load } = status;
  const minutes = Math.round(load.window_seconds / 60);
  const memUsed =
    load.host.mem_total_bytes !== null && load.host.mem_available_bytes !== null
      ? load.host.mem_total_bytes - load.host.mem_available_bytes
      : null;
  const cpuBusy = load.host.load1 !== null && load.host.load1 > load.host.cpus;
  const memPercent =
    memUsed !== null && load.host.mem_total_bytes ? Math.round((memUsed / load.host.mem_total_bytes) * 100) : null;
  return (
    <div className="status-panel">
      <h2>Нагрузка</h2>
      <div className="status-cards">
        <StatusCard title="Процессор (load 1/5/15 мин)" ok={load.host.load1 === null ? null : !cpuBusy}>
          {load.host.load1 === null
            ? "нет данных"
            : `${[load.host.load1, load.host.load5, load.host.load15].map((v) => v?.toFixed(2)).join(" / ")} · ядер ${load.host.cpus}`}
        </StatusCard>
        <StatusCard title="Память" ok={memPercent === null ? null : memPercent < 90}>
          {memPercent === null ? "нет данных" : `${formatBytes(memUsed)} из ${formatBytes(load.host.mem_total_bytes)} (${memPercent}%)`}
        </StatusCard>
        <StatusCard title={`Запросы за ${minutes} мин`} ok={load.requests.errors_5xx === 0}>
          {load.requests.requests} · ошибок сервера {load.requests.errors_5xx} · p95 {formatLatency(load.requests.p95_ms)}
        </StatusCard>
        <StatusCard title={`Команды обучаемых за ${minutes} мин`} ok={load.commands.p95_ms === null ? null : load.commands.p95_ms <= 100}>
          {load.commands.requests} · p50 {formatLatency(load.commands.p50_ms)} · p95 {formatLatency(load.commands.p95_ms)}
        </StatusCard>
        <StatusCard title="Сеансы и потоки" ok={null}>
          сеансов {load.active_sessions ?? "—"} · SSE-подключений {load.sse_connections ?? "—"}
        </StatusCard>
        <StatusCard title="Занятия" ok={null}>
          идёт {load.running_lessons ?? "—"} · открытых карточек {load.open_items ?? "—"}
        </StatusCard>
      </div>
      <p className="notice">
        Окно запросов хранится в памяти api и начинается заново после его перезапуска; p50/p95 — верхняя граница интервала. Долгая история — в метриках Prometheus.
      </p>
    </div>
  );
}

function formatLatency(ms: number | null | undefined): string {
  return ms === null || ms === undefined ? "—" : `≤ ${ms.toLocaleString("ru-RU")} мс`;
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

function IntegrityPanel(props: { status: AdminStatus; onStart: () => void; starting: boolean; started: boolean; error: string | null }) {
  const report = props.status.integrity;
  return (
    <div className="status-panel admin-integrity">
      <div className="status-panel-heading">
        <h2>Целостность данных</h2>
        <button type="button" onClick={props.onStart} disabled={props.starting}>
          Проверить сейчас
        </button>
      </div>
      {props.error && <p className="error">{props.error}</p>}
      {props.started && <p className="notice">Проверка поставлена в очередь, результат появится здесь.</p>}
      {!report && <p>Проверка ещё не выполнялась. Она запускается раз в сутки.</p>}
      {report && (
        <>
          <p className={report.ok ? undefined : "error"}>
            {report.ok ? "Расхождений не найдено." : "Найдены расхождения или проверка не выполнилась."} Последняя проверка: {formatDateTime(report.checked_at)}
          </p>
          <table>
            <thead>
              <tr><th>Раздел</th><th>Проверено</th><th>Расхождений</th><th>Идентификаторы</th></tr>
            </thead>
            <tbody>
              {report.sections.map((section) => (
                <tr key={section.name} className={section.problems > 0 || section.error ? "audit-error" : undefined}>
                  <td>{integritySectionLabels[section.name] ?? section.name}</td>
                  <td>{section.checked}</td>
                  <td>{section.error ? "проверка не выполнилась" : section.problems}</td>
                  <td>{section.examples.length > 0 ? section.examples.map((id) => <code key={id}>{id} </code>) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
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
