import { useState } from "react";
import { Link } from "react-router-dom";
import { useScenarios, useServices, type ScenarioFilter, type ScenarioStatus } from "../../api/content";
import { errorMessage } from "../../api/errors";
import { formatDateTime } from "../../format";
import { scenarioStatusLabel } from "../../labels";

const pageSize = 20;
// The "Служба" filter's own entry for operator 112 scenarios: they have no
// target service, so it filters by exercise type instead.
const operator112Filter = "__operator112";

// GET /scenarios' catalogue (slice-2-plan.md's C6): filters (service,
// status — defaults to approved since that is what an instructor
// actually assigns; slice 11's drafts are a minority case), a table, and
// pagination — mirrors admin/Users.tsx's own list+filter+paginate shape.
export function ScenarioCatalogueRoute() {
  const [page, setPage] = useState(1);
  const [service, setService] = useState("");
  const [status, setStatus] = useState<ScenarioStatus | "">("approved");
  const [difficultyMin, setDifficultyMin] = useState("");
  const [difficultyMax, setDifficultyMax] = useState("");

  const services = useServices();

  const filter: ScenarioFilter = {
    service: service && service !== operator112Filter ? service : undefined,
    exerciseType: service === operator112Filter ? "operator112_intake" : undefined,
    status: status || undefined,
    difficultyMin: difficultyMin ? Number(difficultyMin) : undefined,
    difficultyMax: difficultyMax ? Number(difficultyMax) : undefined,
    page,
    pageSize,
  };
  const scenarios = useScenarios(filter);

  const total = scenarios.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / pageSize));

  function updateFilter<T>(setter: (value: T) => void) {
    return (value: T) => {
      setter(value);
      setPage(1);
    };
  }
  const setServiceFiltered = updateFilter(setService);
  const setStatusFiltered = updateFilter(setStatus);
  const setDifficultyMinFiltered = updateFilter(setDifficultyMin);
  const setDifficultyMaxFiltered = updateFilter(setDifficultyMax);

  return (
    <section className="instructor-page scenario-catalogue">
      <header className="page-heading">
        <div><h1>Готовые сценарии</h1><p>Каталог учебных происшествий для назначения на рабочие места.</p></div>
        <Link to="/instructor/scenarios/new" className="arm-button arm-primary-action">+ Создать сценарий 112</Link>
      </header>
      <form className="scenario-filters arm-filters" onSubmit={(e) => e.preventDefault()}>
        <label>
          Служба
          <select value={service} onChange={(e) => setServiceFiltered(e.target.value)}>
            <option value="">Все</option>
            <option value={operator112Filter}>112 — оператор (все сценарии 112)</option>
            {services.data?.map((s) => (
              <option key={s.code} value={s.code}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Статус
          <select value={status} onChange={(e) => setStatusFiltered(e.target.value as ScenarioStatus | "")}>
            <option value="">Все</option>
            <option value="approved">Утверждён</option>
            <option value="draft">Черновик</option>
            <option value="archived">Архив</option>
          </select>
        </label>
        <label>
          Сложность от
          <input
            type="number"
            min={1}
            max={10}
            value={difficultyMin}
            onChange={(e) => setDifficultyMinFiltered(e.target.value)}
          />
        </label>
        <label>
          Сложность до
          <input
            type="number"
            min={1}
            max={10}
            value={difficultyMax}
            onChange={(e) => setDifficultyMaxFiltered(e.target.value)}
          />
        </label>
        <span className="page-count scenario-filters-count">Найдено: {total}</span>
      </form>

      {scenarios.isPending && <p>Загрузка…</p>}
      {scenarios.isError && <p className="error">{errorMessage(scenarios.error)}</p>}
      {scenarios.data && scenarios.data.items.length === 0 && <p>Сценарии не найдены.</p>}
      {scenarios.data && scenarios.data.items.length > 0 && (
        <>
          <div className="arm-table-wrap">
          <table className="scenario-table">
            <thead>
              <tr>
                <th>Название</th>
                <th>Служба</th>
                <th>Сложность</th>
                <th>Статус</th>
                <th>Версия</th>
                <th>Обновлён</th>
              </tr>
            </thead>
            <tbody>
              {scenarios.data.items.map((s) => (
                <tr key={s.id}>
                  <td>
                    <Link to={`/instructor/scenarios/${s.id}`}>{s.title}</Link>
                  </td>
                  <td>{s.exercise_type === "operator112_intake" ? "112" : s.target_service}</td>
                  <td>{s.difficulty}</td>
                  <td><span className={`status-badge status-${s.status}`}>{scenarioStatusLabel(s.status)}</span></td>
                  <td>{s.version}</td>
                  <td>{formatDateTime(s.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
          <div className="arm-pagination">
            <span>Всего: {total}. Страница {page} из {pages}.</span>
            <span><button type="button" aria-label="Предыдущая страница" disabled={page <= 1} onClick={() => setPage(page - 1)}>←</button><button type="button" aria-label="Следующая страница" disabled={page >= pages} onClick={() => setPage(page + 1)}>→</button></span>
          </div>
        </>
      )}
    </section>
  );
}
