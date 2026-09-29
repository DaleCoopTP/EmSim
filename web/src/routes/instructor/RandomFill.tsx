import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { errorMessage } from "../../api/errors";
import { drawAssignments, useScenarioCategories, type Assignment, type Level } from "../../api/training";

const levelLabels: Record<Level, string> = { easy: "лёгкий", medium: "средний", hard: "сложный" };
const levelBands: Record<Level, string> = { easy: "сложность 1–3", medium: "сложность 4–6", hard: "сложность 7–10" };

// ДДС-6/ADR-035: proposes a random queue for every chosen workstation/
// trainee pair from the picked classifier sections, at the lesson level's
// difficulty. The proposal only fills the editor — nothing is saved until
// the instructor presses "Сохранить назначения".
export function RandomFill({ lessonId, level, pairs, onDrawn }: {
  lessonId: string;
  level: Level;
  pairs: { workstation_no: number; user_id: string }[];
  onDrawn: (assignments: Assignment[]) => void;
}) {
  const categories = useScenarioCategories();
  const [chosen, setChosen] = useState<string[]>([]);
  const [count, setCount] = useState("3");
  const draw = useMutation({
    mutationFn: () => drawAssignments(lessonId, { categories: chosen, count: Number(count), rows: pairs }),
    onSuccess: onDrawn,
  });
  const countValue = Number(count);
  const countValid = Number.isInteger(countValue) && countValue >= 1 && countValue <= 20;
  const toggle = (code: string) => setChosen((current) => (current.includes(code) ? current.filter((c) => c !== code) : [...current, code]));

  return (
    <fieldset className="random-fill">
      <legend>Случайная выдача карточек</legend>
      <p className="editor-hint">
        Вместо ручного выбора кейсов для каждого РМ: отметьте разделы классификатора происшествий и число карточек —
        сервер подберёт каждому обучаемому разные случайные сценарии его службы уровня «{levelLabels[level]}» ({levelBands[level]}).
        Очереди в таблице выше заполнятся, их можно поправить, затем нажмите «Сохранить назначения».
      </p>
      {categories.isPending && <p>Загрузка разделов…</p>}
      {categories.isError && <p className="error">{errorMessage(categories.error)}</p>}
      {categories.data?.length === 0 && <p>Нет утверждённых сценариев ДДС.</p>}
      {categories.data && categories.data.length > 0 && (
        <ul className="random-fill-categories">
          {categories.data.map((category) => (
            <li key={category.code}>
              <label>
                <input type="checkbox" checked={chosen.includes(category.code)} onChange={() => toggle(category.code)} />
                <span><strong>Раздел {category.code}</strong> — {category.type_names.join(", ")}</span>
                <small>сценариев этого уровня: {category.count_by_level[level]}</small>
              </label>
            </li>
          ))}
        </ul>
      )}
      <div className="random-fill-controls">
        <label>Карточек на РМ
          <input type="number" min={1} max={20} value={count} onChange={(event) => setCount(event.target.value)} />
        </label>
        <button type="button" className="arm-secondary-action" disabled={chosen.length === 0 || !countValid || pairs.length === 0 || draw.isPending} onClick={() => draw.mutate()}>
          Заполнить случайно
        </button>
      </div>
      {pairs.length === 0 && <p className="editor-hint">Сначала выберите рабочее место и обучаемого хотя бы в одной строке.</p>}
      {draw.isError && <p role="alert" className="error">{errorMessage(draw.error)}</p>}
    </fieldset>
  );
}
