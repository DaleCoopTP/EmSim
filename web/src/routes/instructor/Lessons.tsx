import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { errorMessage } from "../../api/errors";
import { createLesson, lessonsQueryKey, useLessons, type Level, type LessonCreate } from "../../api/training";
import { formatDateTime } from "../../format";

const stateLabels: Record<string, string> = { draft: "черновик", running: "идёт", stopped: "остановлено", finished: "завершено" };
const levelLabels: Record<Level, string> = { easy: "лёгкий", medium: "средний", hard: "сложный" };

export function LessonsRoute() {
  const lessons = useLessons();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [showCreate, setShowCreate] = useState(false);
  const [title, setTitle] = useState("");
  const [level, setLevel] = useState<Level>("easy");
  const [spawnEveryS, setSpawnEveryS] = useState("150");
  const create = useMutation({
    mutationFn: createLesson,
    onSuccess: async (lesson) => {
      await queryClient.invalidateQueries({ queryKey: lessonsQueryKey });
      navigate(`/instructor/lessons/${lesson.id}`);
    },
  });

  const spawnValue = Number(spawnEveryS);
  const spawnValid = level !== "hard" || (Number.isFinite(spawnValue) && spawnValue > 0);

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const body: LessonCreate = {
      exercise_type: "dds_processing",
      title: title.trim(),
      mode: "training",
      level,
      timing: {
        open_s: 30, primary_s: 30, complete_s: 180,
        spawn_every_s: level === "hard" ? spawnValue : undefined,
      },
    };
    create.mutate(body);
  };

  return (
    <section>
      <h1>Занятия</h1>
      <p><button type="button" onClick={() => setShowCreate((shown) => !shown)}>Создать занятие</button></p>
      {showCreate && (
        <form className="lesson-form" onSubmit={onSubmit}>
          <h2>Новое занятие</h2>
          <label>Название<input required maxLength={200} value={title} onChange={(event) => setTitle(event.target.value)} /></label>
          <label>Уровень
            <select value={level} onChange={(event) => setLevel(event.target.value as Level)}>
              {(Object.keys(levelLabels) as Level[]).map((option) => <option key={option} value={option}>{levelLabels[option]}</option>)}
            </select>
          </label>
          {level === "hard" && (
            <label>Интервал новых карточек, с
              <input type="number" min={1} required value={spawnEveryS} onChange={(event) => setSpawnEveryS(event.target.value)} />
            </label>
          )}
          <p>Режим: обучение · таймеры: 30 / 30 / 180 с</p>
          {create.isError && <p role="alert" className="error">{errorMessage(create.error)}</p>}
          <p>
            <button type="submit" disabled={create.isPending || title.trim() === "" || !spawnValid}>Создать</button>{" "}
            <button type="button" onClick={() => setShowCreate(false)}>Отмена</button>
          </p>
        </form>
      )}
      {lessons.isPending && <p>Загрузка…</p>}
      {lessons.isError && <p className="error">{errorMessage(lessons.error)}</p>}
      {lessons.data?.length === 0 && <p>Занятий пока нет.</p>}
      {lessons.data && lessons.data.length > 0 && (
        <table>
          <thead><tr><th>Название</th><th>Режим</th><th>Уровень</th><th>Состояние</th><th>Начато</th></tr></thead>
          <tbody>{lessons.data.map((lesson) => (
            <tr key={lesson.id}>
              <td><Link to={`/instructor/lessons/${lesson.id}`}>{lesson.title}</Link></td>
              <td>{lesson.mode === "training" ? "обучение" : "вводное"}</td>
              <td>{levelLabels[lesson.level] ?? lesson.level}</td>
              <td>{stateLabels[lesson.state] ?? lesson.state}</td>
              <td>{formatDateTime(lesson.started_at)}</td>
            </tr>
          ))}</tbody>
        </table>
      )}
    </section>
  );
}
