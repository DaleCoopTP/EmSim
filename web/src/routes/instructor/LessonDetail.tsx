import { useMutation, useQueryClient, useQueries } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../../api/client";
import { scenarioQueryKey, useScenarios, type Scenario } from "../../api/content";
import { errorMessage } from "../../api/errors";
import {
  lessonQueryKey, lessonsQueryKey, replaceAssignments, startLesson, stopLesson,
  useLesson, useLessonOptions, type Assignment,
} from "../../api/training";
import { formatDateTime } from "../../format";

const stateLabels: Record<string, string> = { draft: "Черновик", running: "Идёт", stopped: "Остановлено", finished: "Завершено" };
const levelLabels: Record<string, string> = { easy: "лёгкий", medium: "средний", hard: "сложный" };

interface RowState {
  key: string;
  workstationNo: string;
  userId: string;
  queue: string[]; // scenario ids, in order
}

function rowsFromAssignments(assignments: Assignment[] | undefined): RowState[] {
  if (!assignments || assignments.length === 0) return [{ key: crypto.randomUUID(), workstationNo: "", userId: "", queue: [] }];
  // scenario_version_ids are version ids, not scenario ids — the editor
  // cannot re-derive which scenario a version belongs to without another
  // lookup, so an already-saved queue is shown by count only and can be
  // replaced wholesale, not edited entry-by-entry. Good enough for the
  // slice's minimal-UI bar (slice-planning.md §1); a full round-trip
  // editor is not required here.
  return assignments.map((a) => ({
    key: crypto.randomUUID(), workstationNo: String(a.workstation_no), userId: a.user_id, queue: [],
  }));
}

export function LessonDetailRoute() {
  const { lessonId = "" } = useParams();
  const lesson = useLesson(lessonId);
  const options = useLessonOptions();
  const scenarios = useScenarios({ status: "approved", page: 1, pageSize: 200 });
  const queryClient = useQueryClient();
  const isDraft = lesson.data?.state === "draft";

  const [rows, setRows] = useState<RowState[] | null>(null);
  const effectiveRows = rows ?? rowsFromAssignments(lesson.data?.assignments);

  const usedWorkstations = new Set(effectiveRows.map((r) => r.workstationNo).filter(Boolean));
  const usedUsers = new Set(effectiveRows.map((r) => r.userId).filter(Boolean));

  const queueScenarioIds = Array.from(new Set(effectiveRows.flatMap((r) => r.queue)));
  const versionLookups = useQueries({
    queries: queueScenarioIds.map((id) => ({
      queryKey: scenarioQueryKey(id),
      queryFn: () => api.get<Scenario>(`/scenarios/${encodeURIComponent(id)}`),
    })),
  });
  const versionByScenario = new Map<string, string>();
  queueScenarioIds.forEach((id, i) => {
    const versionId = versionLookups[i]?.data?.version_id;
    if (versionId) versionByScenario.set(id, versionId);
  });
  const versionsLoading = versionLookups.some((q) => q.isPending);

  const updateRow = (key: string, patch: Partial<RowState>) => {
    setRows(effectiveRows.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  };
  const addRow = () => setRows([...effectiveRows, { key: crypto.randomUUID(), workstationNo: "", userId: "", queue: [] }]);
  const removeRow = (key: string) => setRows(effectiveRows.filter((r) => r.key !== key));
  const addToQueue = (key: string, scenarioId: string) => updateRow(key, { queue: [...(effectiveRows.find((r) => r.key === key)?.queue ?? []), scenarioId] });
  const removeFromQueue = (key: string, index: number) => {
    const row = effectiveRows.find((r) => r.key === key);
    if (!row) return;
    updateRow(key, { queue: row.queue.filter((_, i) => i !== index) });
  };
  const moveInQueue = (key: string, index: number, delta: number) => {
    const row = effectiveRows.find((r) => r.key === key);
    if (!row) return;
    const target = index + delta;
    if (target < 0 || target >= row.queue.length) return;
    const queue = [...row.queue];
    [queue[index], queue[target]] = [queue[target], queue[index]];
    updateRow(key, { queue });
  };

  const save = useMutation({
    mutationFn: async () => {
      const assignments: Assignment[] = effectiveRows.map((row) => ({
        workstation_no: Number(row.workstationNo),
        user_id: row.userId,
        scenario_version_ids: row.queue.map((id) => versionByScenario.get(id)).filter((v): v is string => !!v),
      }));
      return replaceAssignments(lessonId, assignments);
    },
    onSuccess: () => { setRows(null); return queryClient.invalidateQueries({ queryKey: lessonQueryKey(lessonId) }); },
  });
  const start = useMutation({
    mutationFn: () => startLesson(lessonId),
    onSuccess: async (started) => {
      queryClient.setQueryData(lessonQueryKey(lessonId), { ...lesson.data, ...started, assignments: lesson.data?.assignments });
      await queryClient.invalidateQueries({ queryKey: lessonsQueryKey });
    },
  });
  const [stopReason, setStopReason] = useState("");
  const stop = useMutation({
    mutationFn: () => stopLesson(lessonId, stopReason.trim() || undefined),
    onSuccess: async (stopped) => {
      queryClient.setQueryData(lessonQueryKey(lessonId), { ...lesson.data, ...stopped, assignments: lesson.data?.assignments });
      await queryClient.invalidateQueries({ queryKey: lessonsQueryKey });
    },
  });

  const onSave = (event: FormEvent) => { event.preventDefault(); save.mutate(); };
  if (lesson.isPending) return <p>Загрузка…</p>;
  if (lesson.isError) return <p className="error">{errorMessage(lesson.error)}</p>;
  const current = lesson.data;

  const rowsValid = effectiveRows.length > 0 && effectiveRows.every((r) =>
    r.workstationNo !== "" && r.userId !== "" && r.queue.length > 0 &&
    (current.level !== "hard" || r.queue.length === 1 || (current.timing?.spawn_every_s ?? 0) > 0));
  const canSave = isDraft && rowsValid && !versionsLoading;
  // Start reflects what the server actually has saved, not the editor's
  // own in-progress (and, right after a successful save, deliberately
  // queue-less — see rowsFromAssignments) local state: rowsValid would
  // otherwise disable Start immediately after a perfectly good save.
  const hasSavedAssignments = (current.assignments?.length ?? 0) > 0;

  return (
    <section>
      <p><Link to="/instructor/lessons">← К занятиям</Link></p>
      <h1>{current.title}</h1>
      <dl>
        <dt>Состояние</dt><dd>{stateLabels[current.state] ?? current.state}</dd>
        <dt>Режим</dt><dd>{current.mode === "training" ? "Обучение" : "Вводное"}</dd>
        <dt>Уровень</dt><dd>{levelLabels[current.level] ?? current.level}</dd>
        <dt>Таймеры</dt><dd>{current.timing?.open_s ?? 30} / {current.timing?.primary_s ?? 30} / {current.timing?.complete_s ?? 180} с{current.level === "hard" && current.timing?.spawn_every_s ? `; новая карточка каждые ${current.timing.spawn_every_s} с` : ""}</dd>
        <dt>Начато</dt><dd>{formatDateTime(current.started_at)}</dd>
        {current.state === "stopped" && <><dt>Остановлено</dt><dd>{formatDateTime(current.stopped_at)}{current.stop_reason ? ` — ${current.stop_reason}` : ""}</dd></>}
      </dl>

      {isDraft ? (
        <form className="lesson-form" onSubmit={onSave}>
          <h2>Назначения</h2>
          {(options.isPending || scenarios.isPending) && <p>Загрузка справочников…</p>}
          {options.isError && <p className="error">{errorMessage(options.error)}</p>}
          {scenarios.isError && <p className="error">{errorMessage(scenarios.error)}</p>}
          {effectiveRows.map((row) => (
            <AssignmentRow
              key={row.key} row={row} options={options.data} scenarios={scenarios.data?.items ?? []}
              usedWorkstations={usedWorkstations} usedUsers={usedUsers}
              onChange={(patch) => updateRow(row.key, patch)}
              onRemove={effectiveRows.length > 1 ? () => removeRow(row.key) : undefined}
              onAddToQueue={(scenarioId) => addToQueue(row.key, scenarioId)}
              onRemoveFromQueue={(index) => removeFromQueue(row.key, index)}
              onMoveInQueue={(index, delta) => moveInQueue(row.key, index, delta)}
            />
          ))}
          <p><button type="button" onClick={addRow}>+ Добавить рабочее место</button></p>
          {current.level === "hard" && effectiveRows.some((r) => r.queue.length > 1) && !((current.timing?.spawn_every_s ?? 0) > 0) && (
            <p role="alert" className="error">У занятия не задан интервал новых карточек — очередь длиннее одной версии недопустима.</p>
          )}
          {save.isError && <p role="alert" className="error">{errorMessage(save.error)}</p>}
          {save.isSuccess && <p>Назначения сохранены.</p>}
          <p><button type="submit" disabled={!canSave || save.isPending}>Сохранить назначения</button></p>
        </form>
      ) : (
        <>
          <h2>Назначения</h2>
          {current.assignments && current.assignments.length > 0 ? (
            <table>
              <thead><tr><th>РМ</th><th>Обучаемый</th><th>Карточек в очереди</th></tr></thead>
              <tbody>{current.assignments.map((a) => (
                <tr key={a.user_id}>
                  <td>№ {a.workstation_no}</td>
                  <td>{options.data?.trainees.find((t) => t.id === a.user_id)?.full_name ?? a.user_id}</td>
                  <td>{a.scenario_version_ids.length}</td>
                </tr>
              ))}</tbody>
            </table>
          ) : <p>Назначений нет.</p>}
        </>
      )}

      {isDraft && <p><button type="button" disabled={!hasSavedAssignments || start.isPending} onClick={() => start.mutate()}>Запустить</button>{start.isError && <span role="alert" className="error"> {errorMessage(start.error)}</span>}</p>}
      {current.state === "running" && (
        <div className="lesson-form">
          <p><Link to={`/instructor/lessons/${lessonId}/monitor`}>Открыть монитор занятия →</Link></p>
          <label>Причина остановки (необязательно)<input value={stopReason} maxLength={500} onChange={(event) => setStopReason(event.target.value)} /></label>
          <p><button type="button" onClick={() => stop.mutate()} disabled={stop.isPending}>Остановить занятие</button></p>
          {stop.isError && <p role="alert" className="error">{errorMessage(stop.error)}</p>}
        </div>
      )}
      {current.state === "stopped" && <p className="notice">Занятие остановлено; оставшиеся карточки закрываются фоновой задачей. <Link to={`/instructor/lessons/${lessonId}/monitor`}>Монитор</Link> · <Link to={`/instructor/lessons/${lessonId}/assessments`}>Разбор</Link></p>}
      {current.state === "finished" && <p className="notice">Занятие завершено. <Link to={`/instructor/lessons/${lessonId}/assessments`}>Открыть разбор</Link> · <Link to={`/instructor/lessons/${lessonId}/report`}>Открыть отчёт</Link></p>}
    </section>
  );
}

function AssignmentRow({
  row, options, scenarios, usedWorkstations, usedUsers, onChange, onRemove, onAddToQueue, onRemoveFromQueue, onMoveInQueue,
}: {
  row: RowState;
  options: ReturnType<typeof useLessonOptions>["data"];
  scenarios: { id: string; title: string; target_service: string; version: number }[];
  usedWorkstations: Set<string>;
  usedUsers: Set<string>;
  onChange: (patch: Partial<RowState>) => void;
  onRemove?: () => void;
  onAddToQueue: (scenarioId: string) => void;
  onRemoveFromQueue: (index: number) => void;
  onMoveInQueue: (index: number, delta: number) => void;
}) {
  const [scenarioToAdd, setScenarioToAdd] = useState("");
  const trainee = options?.trainees.find((t) => t.id === row.userId);
  const compatible = scenarios.filter((s) => !trainee?.service_code || s.target_service === trainee.service_code);
  const scenarioTitle = (id: string) => scenarios.find((s) => s.id === id)?.title ?? id;

  return (
    <fieldset className="assignment-row">
      <label>Рабочее место
        <select required value={row.workstationNo} onChange={(event) => onChange({ workstationNo: event.target.value })}>
          <option value="">Выберите РМ</option>
          {options?.workstations.map((ws) => (
            <option key={ws.id} value={ws.number} disabled={usedWorkstations.has(String(ws.number)) && row.workstationNo !== String(ws.number)}>
              № {ws.number}{ws.label ? ` · ${ws.label}` : ""}
            </option>
          ))}
        </select>
      </label>
      <label>Обучаемый
        <select required value={row.userId} onChange={(event) => onChange({ userId: event.target.value, queue: [] })}>
          <option value="">Выберите обучаемого</option>
          {options?.trainees.map((t) => (
            <option key={t.id} value={t.id} disabled={usedUsers.has(t.id) && row.userId !== t.id}>
              {t.full_name} · {t.service_name ?? t.service_code ?? "без службы"}
            </option>
          ))}
        </select>
      </label>
      <div className="queue-editor">
        <span>Очередь:</span>
        {row.queue.length === 0 && <span className="notice"> пусто</span>}
        <ol>
          {row.queue.map((scenarioId, index) => (
            <li key={`${scenarioId}-${index}`}>
              {scenarioTitle(scenarioId)}{" "}
              <button type="button" disabled={index === 0} onClick={() => onMoveInQueue(index, -1)}>↑</button>{" "}
              <button type="button" disabled={index === row.queue.length - 1} onClick={() => onMoveInQueue(index, 1)}>↓</button>{" "}
              <button type="button" onClick={() => onRemoveFromQueue(index)}>Убрать</button>
            </li>
          ))}
        </ol>
        <select value={scenarioToAdd} disabled={!row.userId} onChange={(event) => setScenarioToAdd(event.target.value)}>
          <option value="">Добавить сценарий в очередь</option>
          {compatible.map((s) => <option key={s.id} value={s.id}>{s.title} · версия {s.version}</option>)}
        </select>
        <button type="button" disabled={!scenarioToAdd} onClick={() => { onAddToQueue(scenarioToAdd); setScenarioToAdd(""); }}>+ В очередь</button>
      </div>
      {onRemove && <p><button type="button" onClick={onRemove}>Удалить строку</button></p>}
    </fieldset>
  );
}
