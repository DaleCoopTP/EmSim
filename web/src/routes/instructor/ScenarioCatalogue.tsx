import { useState } from "react";
import { Link } from "react-router-dom";
import { useScenarios, useServices, type ScenarioFilter, type ScenarioStatus } from "../../api/content";
import { errorMessage } from "../../api/errors";
import { formatDateTime } from "../../format";

const pageSize = 20;

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
    service: service || undefined,
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
    <section>
      <h1>Сценарии</h1>
      <form className="scenario-filters" onSubmit={(e) => e.preventDefault()}>
        <label>
          Служба
          <select value={service} onChange={(e) => setServiceFiltered(e.target.value)}>
            <option value="">Все</option>
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
      </form>

      {scenarios.isPending && <p>Загрузка…</p>}
      {scenarios.isError && <p className="error">{errorMessage(scenarios.error)}</p>}
      {scenarios.data && scenarios.data.items.length === 0 && <p>Сценарии не найдены.</p>}
      {scenarios.data && scenarios.data.items.length > 0 && (
        <>
          <table>
            <thead>
              <tr>
                <th>Название</th>
                <th>Служба</th>
                <th>Сложность</th>
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
                  <td>{s.target_service}</td>
                  <td>{s.difficulty}</td>
                  <td>{s.version}</td>
                  <td>{formatDateTime(s.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>
            Всего: {total}. Страница {page} из {pages}.{" "}
            <button type="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              ←
            </button>{" "}
            <button type="button" disabled={page >= pages} onClick={() => setPage(page + 1)}>
              →
            </button>
          </p>
        </>
      )}
    </section>
  );
}
