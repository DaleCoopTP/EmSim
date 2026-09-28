import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { errorMessage } from "../../api/errors";
import {
  lessonQueryKey, lessonRubricQueryKey, updateLessonSettings, useLessonRubric,
  type Lesson,
} from "../../api/training";
import { TimingFields } from "../../components/LessonTimingFields";
import { timingProblemText } from "../../lessonTiming";

// ДДС-6/ADR-035: the time norm and the rubric's weights and pass threshold
// of one DDS lesson. Editable only while the lesson is a draft — start
// freezes both into every card — and read-only afterwards.
export function LessonSettings({ lesson }: { lesson: Lesson }) {
  const draft = lesson.state === "draft";
  return (
    <>
      <TimingSettings lesson={lesson} draft={draft} />
      <ScoringSettings lesson={lesson} draft={draft} />
    </>
  );
}

function TimingSettings({ lesson, draft }: { lesson: Lesson; draft: boolean }) {
  const queryClient = useQueryClient();
  const timing = lesson.timing;
  const [edits, setEdits] = useState<{ open: string; primary: string; complete: string } | null>(null);
  const values = edits ?? { open: String(timing?.open_s ?? 30), primary: String(timing?.primary_s ?? 30), complete: String(timing?.complete_s ?? 180) };
  const problem = timingProblemText(Number(values.open), Number(values.primary), Number(values.complete));
  const save = useMutation({
    mutationFn: () => updateLessonSettings(lesson.id, {
      timing: {
        open_s: Number(values.open), primary_s: Number(values.primary), complete_s: Number(values.complete),
        spawn_every_s: timing?.spawn_every_s ?? undefined,
      },
    }),
    onSuccess: async () => { setEdits(null); await queryClient.invalidateQueries({ queryKey: lessonQueryKey(lesson.id) }); },
  });
  const onSubmit = (event: FormEvent) => { event.preventDefault(); save.mutate(); };

  if (!draft) return null; // the read-only norm is already in the lesson's summary
  return (
    <form className="lesson-form lesson-timing-settings" onSubmit={onSubmit}>
      <h2>Нормативы</h2>
      <TimingFields
        open={values.open} primary={values.primary} complete={values.complete}
        onOpen={(open) => setEdits({ ...values, open })} onPrimary={(primary) => setEdits({ ...values, primary })} onComplete={(complete) => setEdits({ ...values, complete })}
      />
      {problem && <p role="alert" className="error">{problem}</p>}
      {save.isError && <p role="alert" className="error">{errorMessage(save.error)}</p>}
      {save.isSuccess && edits === null && <p>Нормативы сохранены.</p>}
      <p><button type="submit" className="arm-secondary-action" disabled={edits === null || problem !== null || save.isPending}>Сохранить нормативы</button></p>
    </form>
  );
}

const weightSumTolerance = 0.01;

function ScoringSettings({ lesson, draft }: { lesson: Lesson; draft: boolean }) {
  const queryClient = useQueryClient();
  const rubric = useLessonRubric(lesson.id);
  const [weightEdits, setWeightEdits] = useState<Record<string, string> | null>(null);
  const [thresholdEdit, setThresholdEdit] = useState<string | null>(null);

  const invalidate = async () => {
    setWeightEdits(null);
    setThresholdEdit(null);
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: lessonQueryKey(lesson.id) }),
      queryClient.invalidateQueries({ queryKey: lessonRubricQueryKey(lesson.id) }),
    ]);
  };
  const save = useMutation({
    mutationFn: (scoring: { weights: Record<string, number>; pass_threshold: number }) => updateLessonSettings(lesson.id, { scoring }),
    onSuccess: invalidate,
  });
  const reset = useMutation({
    mutationFn: () => updateLessonSettings(lesson.id, { scoring: null }),
    onSuccess: invalidate,
  });

  if (rubric.isPending) return <p>Загрузка рубрики…</p>;
  if (rubric.isError) return <p className="error">{errorMessage(rubric.error)}</p>;
  const data = rubric.data;
  const weightText = (id: string, current: number) => weightEdits?.[id] ?? String(current);
  const thresholdText = thresholdEdit ?? String(data.pass_threshold);
  const sum = data.criteria.reduce((total, c) => total + Number(weightText(c.id, c.weight)), 0);
  const weightsValid = data.criteria.every((c) => {
    const w = Number(weightText(c.id, c.weight));
    return Number.isFinite(w) && w >= 0;
  });
  const threshold = Number(thresholdText);
  const thresholdValid = thresholdText.trim() !== "" && Number.isFinite(threshold) && threshold >= 0 && threshold <= 100;
  const sumValid = Math.abs(sum - 100) <= weightSumTolerance;
  const dirty = weightEdits !== null || thresholdEdit !== null;
  const custom = lesson.scoring != null;

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    save.mutate({
      weights: Object.fromEntries(data.criteria.map((c) => [c.id, Number(weightText(c.id, c.weight))])),
      pass_threshold: threshold,
    });
  };

  return (
    <form className="lesson-form lesson-scoring-settings" onSubmit={onSubmit}>
      <h2>Оценивание</h2>
      <p className="notice">
        Рубрика {data.rubric_version}. {custom ? "Веса и порог заданы преподавателем." : "Используются веса и порог рубрики."}
        {" "}Веса сценария переопределяются весами занятия; отключённые и критические критерии сценария продолжают действовать.
      </p>
      <div className="arm-table-wrap">
        <table>
          <thead><tr><th>Критерий</th><th>Тип</th><th>Вес по рубрике</th><th>Вес занятия</th></tr></thead>
          <tbody>{data.criteria.map((c) => (
            <tr key={c.id}>
              <td>{c.title || c.id}{c.critical ? " ⚠" : ""}</td>
              <td>{c.kind === "llm" ? "ИИ" : c.kind === "manual" ? "ручной" : "правила"}</td>
              <td>{c.default_weight}</td>
              <td>{draft
                ? <input type="number" min={0} step="any" aria-label={`Вес: ${c.title || c.id}`} value={weightText(c.id, c.weight)}
                    onChange={(event) => setWeightEdits({ ...Object.fromEntries(data.criteria.map((x) => [x.id, weightText(x.id, x.weight)])), [c.id]: event.target.value })} />
                : c.weight}</td>
            </tr>
          ))}</tbody>
          <tfoot><tr><td colSpan={3}>Сумма весов</td><td className={sumValid ? "" : "error"}>{Number.isFinite(sum) ? Math.round(sum * 100) / 100 : "—"} / 100</td></tr></tfoot>
        </table>
      </div>
      <label>Порог зачёта, баллов (по рубрике — {data.default_pass_threshold})
        {draft
          ? <input type="number" min={0} max={100} step="any" value={thresholdText} onChange={(event) => setThresholdEdit(event.target.value)} />
          : <strong> {data.pass_threshold}</strong>}
      </label>
      {draft && !sumValid && weightsValid && <p role="alert" className="error">Сумма весов должна быть 100.</p>}
      {draft && !weightsValid && <p role="alert" className="error">Вес — неотрицательное число.</p>}
      {draft && !thresholdValid && <p role="alert" className="error">Порог — число от 0 до 100.</p>}
      {(save.isError || reset.isError) && <p role="alert" className="error">{errorMessage(save.error ?? reset.error)}</p>}
      {draft && (
        <p>
          <button type="submit" className="arm-secondary-action" disabled={!dirty || !weightsValid || !sumValid || !thresholdValid || save.isPending}>Сохранить оценивание</button>{" "}
          <button type="button" disabled={(!custom && !dirty) || reset.isPending} onClick={() => reset.mutate()}>Сбросить к рубрике</button>
        </p>
      )}
    </form>
  );
}
