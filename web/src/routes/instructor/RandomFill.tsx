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
      <legend>Заполнить случайно</legend>
      <p className="notice">
        Для каждого выбранного РМ сервер подберёт разные сценарии его службы из выбранных разделов, уровень «{levelLabels[level]}» ({levelBands[level]}).
        Результат можно поправить до сохранения.
      </p>
      {categories.isPending && <p>Загрузка разделов…</p>}
      {categories.isError && <p className="error">{errorMessage(categories.error)}</p>}
      {categories.data?.length === 0 && <p>Нет утверждённых сценариев ДДС.</p>}
      {categories.data?.map((category) => (
        <label key={category.code} className="random-fill-category">
          <input type="checkbox" checked={chosen.includes(category.code)} onChange={() => toggle(category.code)} />
          {" "}Раздел {category.code}: {category.type_names.join(", ")} — на этом уровне сценариев: {category.count_by_level[level]}
        </label>
      ))}
      <label>Карточек на РМ
        <input type="number" min={1} max={20} value={count} onChange={(event) => setCount(event.target.value)} />
      </label>
      {pairs.length === 0 && <p className="notice">Сначала выберите рабочее место и обучаемого хотя бы в одной строке.</p>}
      {draw.isError && <p role="alert" className="error">{errorMessage(draw.error)}</p>}
      <p>
        <button type="button" className="arm-secondary-action" disabled={chosen.length === 0 || !countValid || pairs.length === 0 || draw.isPending} onClick={() => draw.mutate()}>
          Заполнить случайно
        </button>
      </p>
    </fieldset>
  );
}
