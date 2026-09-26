import { useMutation } from "@tanstack/react-query";
import { useLocation, useNavigate, useOutletContext, useParams } from "react-router-dom";
import { stopLesson } from "../../api/training";
import type { Me } from "../../api/useMe";
import { useItem } from "../../api/workplace";
import { errorMessage } from "../../api/errors";
import { Operator112ProfileCase } from "../trainee/Operator112ProfileCase";
import type { IntakeItem } from "../trainee/Operator112Workplace";

// ScenarioPreviewRoute is 112-7/ADR-027's own "pass it yourself" screen
// (slice-112-7-plan.md's c8): POST /scenarios/{id}/preview-runs already
// created a real one-off lesson/run/item with the instructor as its sole
// participant and no workstation — from here on it is driven exactly
// like a trainee drives their own item (GET/POST /items/{id}...), so this
// route is a thin wrapper around the same Operator112ProfileCase/
// CallerChat components the trainee workplace uses, not a parallel
// implementation. lessonId travels through navigation state (set by
// whoever started this preview — ScenarioEditorRoute) purely so this
// screen can offer an explicit "завершить" button; the preview is not
// broken without it; StartPreview auto-stops the previous one anyway.
export function ScenarioPreviewRoute() {
  const { itemId = "" } = useParams();
  const me = useOutletContext<Me>();
  const navigate = useNavigate();
  const location = useLocation();
  const lessonId = (location.state as { lessonId?: string } | null)?.lessonId ?? "";
  const item = useItem(itemId);
  const stop = useMutation({
    mutationFn: () => stopLesson(lessonId),
    onSuccess: () => navigate("/instructor/scenarios"),
  });

  if (item.isPending) return <p>Загрузка предпросмотра…</p>;
  if (item.isError) return <p className="error">{errorMessage(item.error)}</p>;
  if (item.data.exercise_type !== "operator112_intake") {
    return <p className="error">Предпросмотр 112-7 поддерживает только сценарии оператора 112.</p>;
  }

  const terminal = item.data.state === "closed" || item.data.state === "interrupted";

  return (
    <section className="instructor-page scenario-preview">
      <header className="page-heading">
        <div><h1>Предпросмотр</h1><p>Вы проходите свой собственный черновик. Результат не входит в отчёты и статистику.</p></div>
        {lessonId && <button type="button" disabled={stop.isPending} onClick={() => stop.mutate()}>Завершить предпросмотр</button>}
      </header>
      {terminal && <p className="notice">Кейс завершён. Оценка появится в разборе карточки после автопроверки.</p>}
      {stop.isError && <p className="error">{errorMessage(stop.error)}</p>}
      <Operator112ProfileCase me={me} item={item.data as unknown as IntakeItem} onClose={() => navigate("/instructor/scenarios")} />
    </section>
  );
}
