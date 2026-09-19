import { useState, type FormEvent } from "react";
import { useReplaceWorkstations, useWorkstations, type Workstation } from "../../api/admin";
import { errorMessage } from "../../api/errors";

interface Row {
  key: number;
  number: string;
  label: string;
  active: boolean;
}

// The whole list is edited locally and saved with one PUT
// /admin/workstations ("заменить список"): rows marked active are sent
// and end up active on the server, rows unmarked are simply left out
// and the server deactivates them — nothing is ever deleted, so an
// inactive workstation keeps its id and history and can be re-enabled
// later by ticking it again.
export function WorkstationsRoute() {
  const workstations = useWorkstations();
  const replace = useReplaceWorkstations();
  // null = "no local edits yet": rows are derived from the server list
  // until the first edit, and go back to being derived after a
  // successful save, so there is no effect syncing two copies.
  const [edited, setEdited] = useState<Row[] | null>(null);
  const rows = edited ?? (workstations.data ?? []).map(toRow);
  const dirty = edited !== null;

  const edit = (key: number, patch: Partial<Row>) => {
    setEdited(rows.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  };
  const add = () => {
    const nextNumber = rows.reduce((max, r) => Math.max(max, Number(r.number) || 0), 0) + 1;
    setEdited([...rows, { key: Date.now(), number: String(nextNumber), label: `РМ-${pad(nextNumber)}`, active: true }]);
  };
  const remove = (key: number) => {
    setEdited(rows.filter((r) => r.key !== key));
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const body = rows
      .filter((r) => r.active)
      .map((r) => ({ number: Number(r.number), label: r.label.trim() }));
    replace.mutate(body, { onSuccess: () => setEdited(null) });
  };

  return (
    <section>
      <h1>Рабочие места</h1>
      {workstations.isPending && <p>Загрузка…</p>}
      {workstations.isError && <p className="error">{errorMessage(workstations.error)}</p>}
      {workstations.data && (
        <form onSubmit={onSubmit}>
          <table>
            <thead>
              <tr>
                <th>Номер</th>
                <th>Название</th>
                <th>Активно</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key} className={r.active ? undefined : "inactive"}>
                  <td>
                    <input
                      type="number"
                      min={1}
                      required={r.active}
                      value={r.number}
                      onChange={(e) => edit(r.key, { number: e.target.value })}
                    />
                  </td>
                  <td>
                    <input required={r.active} value={r.label} onChange={(e) => edit(r.key, { label: e.target.value })} />
                  </td>
                  <td>
                    <input type="checkbox" checked={r.active} onChange={(e) => edit(r.key, { active: e.target.checked })} />
                  </td>
                  <td>
                    {/* A row that never reached the server can be dropped outright;
                        a saved one is only ever deactivated (unticked). */}
                    {!workstations.data.some((w) => w.number === Number(r.number)) && (
                      <button type="button" onClick={() => remove(r.key)}>
                        Убрать
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>
            <button type="button" onClick={add}>
              Добавить РМ
            </button>{" "}
            <button type="submit" disabled={!dirty || replace.isPending}>
              Сохранить
            </button>
          </p>
          {replace.isError && <p role="alert" className="error">{errorMessage(replace.error)}</p>}
          {replace.isSuccess && !dirty && <p>Сохранено.</p>}
        </form>
      )}
    </section>
  );
}

function toRow(w: Workstation, index: number): Row {
  return { key: index + 1, number: String(w.number), label: w.label, active: w.active };
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}
