import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import { useScenario, useScenarios } from "../../api/content";
import { errorMessage } from "../../api/errors";
import { lessonQueryKey, lessonsQueryKey, replaceAssignments, startLesson, useLesson, useLessonOptions } from "../../api/training";
import { formatDateTime } from "../../format";

const stateLabels: Record<string, string> = { draft: "Черновик", running: "Идёт", stopped: "Остановлено", finished: "Завершено" };

export function LessonDetailRoute() {
  const { lessonId = "" } = useParams();
  const lesson = useLesson(lessonId);
  const options = useLessonOptions();
  const scenarios = useScenarios({ status: "approved", page: 1, pageSize: 200 });
  const [traineeId, setTraineeId] = useState("");
  const [workstationNo, setWorkstationNo] = useState("");
  const [scenarioId, setScenarioId] = useState("");
  const scenario = useScenario(scenarioId);
  const queryClient = useQueryClient();
  const assignment = lesson.data?.assignments?.[0];
  const selectedTraineeId = traineeId || assignment?.user_id || "";
  const selectedWorkstationNo = workstationNo || (assignment ? String(assignment.workstation_no) : "");
  const trainee = options.data?.trainees.find((candidate) => candidate.id === selectedTraineeId);
  const compatibleScenarios = scenarios.data?.items.filter(
    (candidate) => !trainee?.service_code || candidate.target_service === trainee.service_code,
  ) ?? [];

  const save = useMutation({
    mutationFn: async () => {
      const versionId = scenario.data?.version_id;
      if (!versionId) throw new Error("У выбранного сценария нет утверждённой версии.");
      return replaceAssignments(lessonId, [{ workstation_no: Number(selectedWorkstationNo), user_id: selectedTraineeId, scenario_version_ids: [versionId] }]);
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: lessonQueryKey(lessonId) }),
  });
  const start = useMutation({
    mutationFn: () => startLesson(lessonId),
    onSuccess: async (started) => {
      queryClient.setQueryData(lessonQueryKey(lessonId), { ...lesson.data, ...started, assignments: lesson.data?.assignments });
      await queryClient.invalidateQueries({ queryKey: lessonsQueryKey });
    },
  });

  const onSave = (event: FormEvent) => { event.preventDefault(); save.mutate(); };
  if (lesson.isPending) return <p>Загрузка…</p>;
  if (lesson.isError) return <p className="error">{errorMessage(lesson.error)}</p>;
  const current = lesson.data;
  const isDraft = current.state === "draft";
  const canSave = isDraft && selectedTraineeId !== "" && selectedWorkstationNo !== "" && scenarioId !== "" && !!scenario.data?.version_id;

  return (
    <section>
      <p><Link to="/instructor/lessons">← К занятиям</Link></p>
      <h1>{current.title}</h1>
      <dl>
        <dt>Состояние</dt><dd>{stateLabels[current.state] ?? current.state}</dd>
        <dt>Режим</dt><dd>{current.mode === "training" ? "Обучение" : "Вводное"}</dd>
        <dt>Уровень</dt><dd>{current.level}</dd>
        <dt>Таймеры</dt><dd>{current.timing?.open_s ?? 30} / {current.timing?.primary_s ?? 30} / {current.timing?.complete_s ?? 180} с</dd>
        <dt>Начато</dt><dd>{formatDateTime(current.started_at)}</dd>
      </dl>
      {isDraft ? (
        <form className="lesson-form" onSubmit={onSave}>
          <h2>Назначение</h2>
          {(options.isPending || scenarios.isPending) && <p>Загрузка справочников…</p>}
          {options.isError && <p className="error">{errorMessage(options.error)}</p>}
          {scenarios.isError && <p className="error">{errorMessage(scenarios.error)}</p>}
          <label>Обучаемый
            <select required value={selectedTraineeId} onChange={(event) => { setTraineeId(event.target.value); setScenarioId(""); }}>
              <option value="">Выберите обучаемого</option>
              {options.data?.trainees.map((option) => <option key={option.id} value={option.id}>{option.full_name} · {option.service_name ?? option.service_code ?? "без службы"}</option>)}
            </select>
          </label>
          <label>Рабочее место
            <select required value={selectedWorkstationNo} onChange={(event) => setWorkstationNo(event.target.value)}>
              <option value="">Выберите РМ</option>
              {options.data?.workstations.map((option) => <option key={option.id} value={option.number}>№ {option.number}{option.label ? ` · ${option.label}` : ""}</option>)}
            </select>
          </label>
          <label>Сценарий
            <select required value={scenarioId} onChange={(event) => setScenarioId(event.target.value)} disabled={!selectedTraineeId}>
              <option value="">Выберите утверждённый сценарий</option>
              {compatibleScenarios.map((option) => <option key={option.id} value={option.id}>{option.title} · версия {option.version}</option>)}
            </select>
          </label>
          {scenario.isError && <p className="error">{errorMessage(scenario.error)}</p>}
          {save.isError && <p role="alert" className="error">{errorMessage(save.error)}</p>}
          {save.isSuccess && <p>Назначение сохранено.</p>}
          <p><button type="submit" disabled={!canSave || save.isPending}>Сохранить назначение</button></p>
        </form>
      ) : <h2>Назначение</h2>}
      {assignment ? (
        <dl>
          <dt>Обучаемый</dt><dd>{options.data?.trainees.find((option) => option.id === assignment.user_id)?.full_name ?? assignment.user_id}</dd>
          <dt>Рабочее место</dt><dd>№ {assignment.workstation_no}</dd>
          <dt>Версия сценария</dt><dd>{assignment.scenario_version_ids[0]}</dd>
        </dl>
      ) : <p>Назначение ещё не сохранено.</p>}
      {isDraft && <p><button type="button" disabled={!assignment || start.isPending} onClick={() => start.mutate()}>Запустить</button>{start.isError && <span role="alert" className="error"> {errorMessage(start.error)}</span>}</p>}
      {current.state === "running" && <p className="notice">Занятие запущено.</p>}
      {current.state === "finished" && <p className="notice">Занятие завершено.</p>}
    </section>
  );
}
