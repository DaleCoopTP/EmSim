import { useAdminConfig, type AdminConfigParam } from "../../api/admin";
import { configGroupLabels, configGroupOrder, configParamLabels } from "../../adminLabels";
import { errorMessage } from "../../api/errors";

// ADR-038: the effective configuration, for reading only. Critical
// settings (TLS, session lifetime, password policy) are set in .env on
// the server by whoever has access to the host — this screen never edits
// anything and never shows a secret: an API key is only "задан".
export function ConfigRoute() {
  const config = useAdminConfig();
  return (
    <section className="admin-config">
      <div className="page-heading">
        <div>
          <h1>Конфигурация</h1>
          <p>Действующие значения, только для чтения.</p>
        </div>
      </div>
      <p className="notice">
        Чтобы изменить параметр, поправьте его в файле <code>.env</code> на сервере и выполните <code>docker compose up -d</code> — контейнеры
        перезапустятся с новыми значениями. Из интерфейса настройки не меняются.
      </p>
      {config.isPending && <p>Загрузка…</p>}
      {config.isError && <p className="error">{errorMessage(config.error)}</p>}
      {config.data && (
        <>
          <ProcessConfig title="api" version={config.data.api.version} params={config.data.api.params} />
          {config.data.workers.length === 0 && (
            <p className="notice">Настройки worker'а появятся, когда он пришлёт первый отчёт (до 30 секунд после запуска).</p>
          )}
          {config.data.workers.map((worker) => (
            <ProcessConfig key={worker.id} title={`worker ${worker.id}`} version={worker.version} params={worker.params} />
          ))}
        </>
      )}
    </section>
  );
}

function ProcessConfig({ title, version, params }: { title: string; version?: string; params: AdminConfigParam[] }) {
  const groups = configGroupOrder
    .map((group) => ({ group, items: params.filter((p) => p.group === group) }))
    .filter((g) => g.items.length > 0);
  return (
    <div className="status-panel">
      <h2>
        {title}
        {version ? <small> · версия {version}</small> : null}
      </h2>
      {groups.map(({ group, items }) => (
        <div key={group} className="config-group">
          <h3>{configGroupLabels[group] ?? group}</h3>
          <table>
            <thead>
              <tr>
                <th>Параметр</th>
                <th>Значение</th>
                <th>Переменная .env</th>
              </tr>
            </thead>
            <tbody>
              {items.map((p) => (
                <tr key={p.env}>
                  <td>{configParamLabels[p.env] ?? p.env}</td>
                  <td><code>{p.value === "" ? "—" : p.value}</code></td>
                  <td><code>{p.env}</code></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </div>
  );
}
