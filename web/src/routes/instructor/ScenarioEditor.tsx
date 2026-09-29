import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  emptyIntake112Body,
  useApproveScenario,
  useCreateScenario,
  useIntake112Catalog,
  useProbeScenario,
  useSaveScenario,
  useScenario,
  useServices,
  useStartPreviewRun,
  useValidateScenario,
  type Intake112EditorBody,
  type Intake112Fact,
  type ProbeMatch,
  type ValidationIssue,
} from "../../api/content";
import { errorMessage } from "../../api/errors";
import { IntakeReferenceEditor } from "../../components/IntakeReferenceEditor";

type Tab = "general" | "caller" | "facts" | "reference" | "check";
const tabs: { id: Tab; label: string }[] = [
  { id: "general", label: "Общее" },
  { id: "caller", label: "Заявитель" },
  { id: "facts", label: "Факты" },
  { id: "reference", label: "Эталон" },
  { id: "check", label: "Проверка" },
];

const knowledgeLabels: Record<Intake112Fact["knowledge"], string> = {
  initial: "сообщает сразу",
  on_question: "только по вопросу",
  unknown: "не знает",
};

function newFact(): Intake112Fact {
  return { id: `fact_${Math.random().toString(36).slice(2, 8)}`, label: "", knowledge: "initial", value: "" };
}

function patternsToText(patterns: string[] | undefined): string {
  return (patterns ?? []).join("\n");
}
function textToPatterns(text: string): string[] | undefined {
  const lines = text.split("\n").map((line) => line.trim()).filter(Boolean);
  return lines.length ? lines : undefined;
}

// ScenarioEditorRoute is 112-7/ADR-027's own editor (slice-112-7-plan.md's
// c7/c8): a single component with a tab switcher rather than five
// separate routed screens — the plan describes five sections of one
// form, not five independent pages with their own navigation/back
// buttons. With no :scenarioId (route "/instructor/scenarios/new") it
// creates a brand-new full_case/free_text draft on first save and then
// switches to editing it in place (replacing the URL) — a scenario only
// starts existing once the server has assigned it an id.
export function ScenarioEditorRoute() {
  const { scenarioId } = useParams();
  const isNew = !scenarioId;
  const navigate = useNavigate();
  const existing = useScenario(scenarioId ?? "");
  const catalog = useIntake112Catalog();
  const services = useServices();
  const createMutation = useCreateScenario();
  const saveMutation = useSaveScenario();
  const validateMutation = useValidateScenario(scenarioId ?? "");
  const probeMutation = useProbeScenario(scenarioId ?? "");
  const approveMutation = useApproveScenario();
  const startPreview = useStartPreviewRun();

  const [tab, setTab] = useState<Tab>("general");
  const [title, setTitle] = useState("Новый сценарий 112");
  const [body, setBody] = useState<Intake112EditorBody>(() => emptyIntake112Body());
  const [savedVersionId, setSavedVersionId] = useState("");
  const [savedDigest, setSavedDigest] = useState("");
  const [issues, setIssues] = useState<ValidationIssue[]>([]);
  const [probeText, setProbeText] = useState("");
  const [probeResult, setProbeResult] = useState<ProbeMatch[] | null>(null);

  // The form follows the server's scenario only while it holds no
  // unsaved edits. A background refetch (window focus, reconnect, a
  // mutation's own invalidation) must never overwrite what the author is
  // typing (review 2026-09-26, item 11): with unsaved edits, a server
  // version this form neither loaded nor saved (e.g. another tab saved)
  // only raises a notice and the author decides; saving meanwhile gets
  // 409 stale_draft. Without edits — including a first load from a stale
  // cache followed by the mount refetch — the newer version is simply
  // taken. All of this is adjusted during render (react.dev's "storing
  // information from previous renders"), not in an effect.
  const [loadedFor, setLoadedFor] = useState<string | null>(null);
  // Every digest this form itself loaded or saved: the query cache still
  // holds the pre-save digest until the post-save refetch lands, and
  // that is not someone else's change.
  const [ownDigests, setOwnDigests] = useState<string[]>([]);
  // The title/body last loaded from or saved to the server; the form is
  // dirty when it differs.
  const [baseline, setBaseline] = useState("");
  const snapshot = (t: string, b: unknown) => JSON.stringify([t, b]);
  const dirty = baseline !== "" && snapshot(title, body) !== baseline;
  const loadFromServer = () => {
    if (!existing.data) return;
    setTitle(existing.data.title);
    setBody(existing.data.body as unknown as Intake112EditorBody);
    setBaseline(snapshot(existing.data.title, existing.data.body));
    setSavedVersionId(existing.data.version_id ?? "");
    setSavedDigest(existing.data.digest ?? "");
    setOwnDigests((current) => [...current, existing.data?.digest ?? ""]);
    setIssues(existing.data.issues ?? []);
  };
  const editable = !!existing.data && existing.data.body.exercise_type === "operator112_intake" && "intake112" in existing.data.body;
  const unseenServerVersion = !!scenarioId && loadedFor === scenarioId && !!existing.data?.digest &&
    !ownDigests.includes(existing.data.digest);
  if (scenarioId && editable && (loadedFor !== scenarioId || (unseenServerVersion && !dirty))) {
    setLoadedFor(scenarioId);
    loadFromServer();
  }
  const serverChanged = unseenServerVersion && dirty;

  if (!isNew && existing.isPending) return <p>Загрузка…</p>;
  if (!isNew && existing.isError) return <p className="error">{errorMessage(existing.error)}</p>;
  if (!isNew && existing.data && (existing.data.body.exercise_type !== "operator112_intake" || !("intake112" in existing.data.body))) {
    return <p className="error">Редактор 112-7 поддерживает только сценарии оператора 112.</p>;
  }

  const intake = body.intake112;
  const errorCount = issues.filter((i) => i.severity === "error").length;
  const hasSavedVersion = savedVersionId !== "";

  const updateIntake = (patch: Partial<Intake112EditorBody["intake112"]>) =>
    setBody((current) => ({ ...current, intake112: { ...current.intake112, ...patch } }));
  const updateCall = (patch: Partial<Intake112EditorBody["intake112"]["call"]>) =>
    updateIntake({ call: { ...intake.call, ...patch } });
  const updateReference = (patch: Partial<Intake112EditorBody["intake112"]["reference"]>) =>
    updateIntake({ reference: { ...intake.reference, ...patch } });
  const updateFacts = (facts: Intake112Fact[]) => updateIntake({ dialogue: { ...intake.dialogue, facts } });
  const updateFact = (index: number, patch: Partial<Intake112Fact>) =>
    updateFacts(intake.dialogue.facts.map((fact, i) => (i === index ? { ...fact, ...patch } : fact)));
  const save = () => {
    if (isNew) {
      createMutation.mutate(
        { title, difficulty: body.difficulty, body: { ...body, difficulty: body.difficulty } },
        {
          onSuccess: (created) => {
            setIssues(created.issues ?? []);
            navigate(`/instructor/scenarios/${created.id}/edit`, { replace: true });
          },
        },
      );
      return;
    }
    saveMutation.mutate(
      { scenarioId: scenarioId!, baseVersionId: savedVersionId, baseDigest: savedDigest, title, difficulty: body.difficulty, body },
      {
        onSuccess: (result) => {
          setSavedVersionId(result.version_id);
          setSavedDigest(result.digest);
          setOwnDigests((current) => [...current, result.digest]);
          setBaseline(snapshot(title, body));
          setIssues(result.issues ?? []);
        },
      },
    );
  };
  const saving = createMutation.isPending || saveMutation.isPending;
  const saveError = createMutation.error ?? saveMutation.error;

  const validate = () => {
    if (!scenarioId) return;
    validateMutation.mutate(body, { onSuccess: (result) => setIssues(result.issues ?? []) });
  };

  const probe = () => {
    if (!scenarioId || !probeText.trim()) return;
    probeMutation.mutate({ text: probeText.trim(), body }, { onSuccess: (result) => setProbeResult(result.opened) });
  };

  const approve = () => {
    if (!scenarioId || !hasSavedVersion) return;
    approveMutation.mutate({ scenarioId, versionId: savedVersionId, baseDigest: savedDigest });
  };

  const preview = () => {
    if (!scenarioId || !hasSavedVersion) return;
    startPreview.mutate(
      { scenarioId, versionId: savedVersionId },
      { onSuccess: (result) => navigate(`/instructor/preview/${result.item_id}?lesson=${encodeURIComponent(result.lesson_id)}`) },
    );
  };

  return (
    <section className="instructor-page scenario-editor">
      <p className="back-link"><Link to="/instructor/scenarios">← К сценариям</Link></p>
      <header className="page-heading">
        <div>
          <h1>{isNew ? "Новый сценарий 112" : title}</h1>
          <p>Полный кейс с ИИ-заявителем (112-7). Каждое сохранение создаёт новую версию черновика.</p>
        </div>
        {!isNew && existing.data && <span className={`status-badge status-${existing.data.version_status ?? existing.data.status}`}>{existing.data.version_status ?? existing.data.status}</span>}
      </header>

      <nav className="scenario-editor-tabs" aria-label="Разделы редактора">
        {tabs.map((t) => (
          <button key={t.id} type="button" className={tab === t.id ? "active" : undefined} onClick={() => setTab(t.id)}>
            {t.label}{t.id === "check" && errorCount > 0 ? ` (${errorCount})` : ""}
          </button>
        ))}
      </nav>

      {tab === "general" && (
        <div className="scenario-editor-panel">
          <div className="editor-form-grid">
            <label className="editor-wide">Название<input value={title} maxLength={200} onChange={(e) => setTitle(e.target.value)} /></label>
            <label>Сложность (1–10)<input type="number" min={1} max={10} value={body.difficulty}
              onChange={(e) => setBody((current) => ({ ...current, difficulty: Number(e.target.value) }))} /></label>
            <label>АОН (+7 и 10 цифр)<input value={intake.call.aon} placeholder="+79161234567" onChange={(e) => updateCall({ aon: e.target.value })} /></label>
            <label>Время вызова (ЧЧ:ММ)<input value={intake.call.local_time} placeholder="12:00" onChange={(e) => updateCall({ local_time: e.target.value })} /></label>
          </div>
          <p className="editor-hint">Часовой пояс: Europe/Moscow (единственный поддерживаемый).</p>
        </div>
      )}

      {tab === "caller" && (
        <div className="scenario-editor-panel">
          <p className="editor-hint">Персона и вступление ИИ-заявителя. Необязательно — без них диалог отвечает заглушкой (CALLER_REPLIER=stub).</p>
          <div className="editor-form-grid editor-form-stack">
            <label>Персона (кто звонит и что о себе знает)
              <textarea value={intake.dialogue.caller?.persona ?? ""} rows={4}
                onChange={(e) => updateIntake({ dialogue: { ...intake.dialogue, caller: { persona: e.target.value, opening: intake.dialogue.caller?.opening ?? { id: "opening", text: "", reveals: [] } } } })} />
            </label>
            <label>Вступительная реплика
              <textarea value={intake.dialogue.caller?.opening.text ?? ""} rows={3}
                onChange={(e) => updateIntake({ dialogue: { ...intake.dialogue, caller: { persona: intake.dialogue.caller?.persona ?? "", opening: { id: "opening", text: e.target.value, reveals: intake.dialogue.caller?.opening.reveals ?? [] } } } })} />
            </label>
          </div>
          {intake.dialogue.caller && (
            <fieldset className="editor-fieldset">
              <legend>Что заявитель сразу раскрывает во вступлении</legend>
              <p className="editor-hint">Только факты со знанием «сообщает сразу».</p>
              <ul className="scenario-editor-checklist">
                {intake.dialogue.facts.filter((f) => f.knowledge === "initial").map((fact) => {
                  const reveals = intake.dialogue.caller!.opening.reveals;
                  const checked = reveals.includes(fact.id);
                  return (
                    <li key={fact.id}>
                      <label>
                        <input type="checkbox" checked={checked} onChange={() =>
                          updateIntake({ dialogue: { ...intake.dialogue, caller: { ...intake.dialogue.caller!, opening: { ...intake.dialogue.caller!.opening, reveals: checked ? reveals.filter((id) => id !== fact.id) : [...reveals, fact.id] } } } })
                        } />
                        {fact.label || fact.id}
                      </label>
                    </li>
                  );
                })}
                {intake.dialogue.facts.filter((f) => f.knowledge === "initial").length === 0 && <li className="editor-hint">Нет фактов со знанием «сообщает сразу» — добавьте их на вкладке «Факты».</li>}
              </ul>
              <button type="button" className="arm-secondary-action" onClick={() => updateIntake({ dialogue: { facts: intake.dialogue.facts, initial: intake.dialogue.initial, questions: intake.dialogue.questions } })}>
                Убрать профиль заявителя (диалог останется на заглушке)
              </button>
            </fieldset>
          )}
        </div>
      )}

      {tab === "facts" && (
        <div className="scenario-editor-panel">
          <p className="editor-hint">Каждый факт — одно сведение, которое заявитель может сообщить. card_path привязывает его к полю карточки (необязательно для чисто повествовательных фактов).</p>
          <div className="arm-table-wrap">
            <table className="scenario-editor-table">
              <colgroup><col style={{ width: "9%" }} /><col style={{ width: "13%" }} /><col style={{ width: "11%" }} /><col style={{ width: "11%" }} /><col style={{ width: "14%" }} /><col style={{ width: "19%" }} /><col style={{ width: "19%" }} /><col style={{ width: "4%" }} /></colgroup>
              <thead><tr><th>ID</th><th>Название</th><th>card_path</th><th>Знание</th><th>Значение</th><th>Реплика заявителя</th><th>Фразы-триггеры (по одной в строке)</th><th /></tr></thead>
              <tbody>
                {intake.dialogue.facts.map((fact, index) => (
                  <tr key={fact.id}>
                    <td><input value={fact.id} onChange={(e) => updateFact(index, { id: e.target.value })} /></td>
                    <td><input value={fact.label} onChange={(e) => updateFact(index, { label: e.target.value })} /></td>
                    <td><input value={fact.card_path ?? ""} placeholder="/address/city" onChange={(e) => updateFact(index, { card_path: e.target.value || undefined })} /></td>
                    <td>
                      <select value={fact.knowledge} onChange={(e) => updateFact(index, { knowledge: e.target.value as Intake112Fact["knowledge"], value: e.target.value === "unknown" ? "" : fact.value })}>
                        {(Object.keys(knowledgeLabels) as Intake112Fact["knowledge"][]).map((k) => <option key={k} value={k}>{knowledgeLabels[k]}</option>)}
                      </select>
                    </td>
                    <td>{fact.knowledge !== "unknown" && <input value={fact.value ?? ""} onChange={(e) => updateFact(index, { value: e.target.value })} />}</td>
                    <td><textarea rows={2} value={fact.statement ?? ""} placeholder="своими словами" onChange={(e) => updateFact(index, { statement: e.target.value || undefined })} /></td>
                    <td><textarea rows={2} value={patternsToText(fact.ask_patterns)} onChange={(e) => updateFact(index, { ask_patterns: textToPatterns(e.target.value) })} /></td>
                    <td><button type="button" className="editor-row-remove" aria-label={`Убрать факт ${fact.label || fact.id}`} title="Убрать" onClick={() => updateFacts(intake.dialogue.facts.filter((_, i) => i !== index))}>×</button></td>
                  </tr>
                ))}
                {intake.dialogue.facts.length === 0 && <tr><td colSpan={8} className="editor-hint">Фактов пока нет.</td></tr>}
              </tbody>
            </table>
          </div>
          <div><button type="button" className="arm-secondary-action" onClick={() => updateFacts([...intake.dialogue.facts, newFact()])}>+ Добавить факт</button></div>
        </div>
      )}

      {tab === "reference" && (
        <div className="scenario-editor-panel">
          <p className="editor-hint">Заполните карточку так, как её должен заполнить оператор, — она сохраняется эталоном для автооценки. Незаполненное поле в эталон не входит, соответствующий блок получает 0.</p>
          <div className="editor-form-grid editor-form-stack">
            <label>Закрытое описание ситуации (видит только преподаватель)
              <textarea rows={3} value={intake.reference.case_description ?? ""} onChange={(e) => updateReference({ case_description: e.target.value })} />
            </label>
          </div>
          <IntakeReferenceEditor reference={intake.reference} onChange={updateReference} catalog={catalog.data}
            services={services.data ?? []} aon={intake.call.aon} localTime={intake.call.local_time} />
        </div>
      )}

      {tab === "check" && (
        <div className="scenario-editor-panel">
          <div><button type="button" disabled={!scenarioId || validateMutation.isPending} onClick={validate}>Проверить</button></div>
          {!scenarioId && <p className="editor-hint">Сохраните черновик, чтобы проверить его.</p>}
          {validateMutation.isError && <p className="error">{errorMessage(validateMutation.error)}</p>}
          {issues.length === 0 ? scenarioId && <p className="editor-ok">Замечаний нет.</p> : (
            <ul className="scenario-editor-issues">
              {issues.map((issue, i) => (
                <li key={i} className={issue.severity === "error" ? "error" : "warning"}>
                  <strong>{issue.severity === "error" ? "Ошибка" : "Замечание"}:</strong> {issue.path} — {issue.code}{issue.message ? `: ${issue.message}` : ""}
                </li>
              ))}
            </ul>
          )}
          <h3>Тестер фраз</h3>
          <p className="editor-hint">Проверьте, какие факты классификатор откроет по пробной реплике заявителя (без обращения к модели).</p>
          <div className="editor-probe">
            <input aria-label="Пробная реплика" value={probeText} placeholder="Например: рядом сильно пахнет газом" onChange={(e) => setProbeText(e.target.value)} />
            <button type="button" disabled={!scenarioId || !probeText.trim() || probeMutation.isPending} onClick={probe}>Проверить фразу</button>
          </div>
          {probeResult && (
            probeResult.length === 0 ? <p>Фраза не открывает ни одного факта.</p> : (
              <ul className="editor-probe-result">{probeResult.map((match, i) => <li key={i}>{match.fact_id} — {match.kind === "reveal" ? "раскрывает" : "отвечает на вопрос"}</li>)}</ul>
            )
          )}
        </div>
      )}

      <footer className="scenario-editor-actions">
        <button type="button" className="arm-primary-action" disabled={saving} onClick={save}>{isNew ? "Создать черновик" : "Сохранить как новую версию"}</button>
        <button type="button" className="arm-secondary-action" disabled={!hasSavedVersion || errorCount > 0 || startPreview.isPending} onClick={preview}>Пройти самому (предпросмотр)</button>
        <button type="button" className="arm-secondary-action" disabled={!hasSavedVersion || errorCount > 0 || approveMutation.isPending} onClick={approve}>Утвердить</button>
        {saveError && <p className="error">{errorMessage(saveError)}</p>}
        {serverChanged && (
          <p className="notice" role="alert">
            На сервере сохранена другая версия этого сценария (например, из другой вкладки). Ваши изменения в форме не тронуты; при сохранении будет конфликт.{" "}
            <button type="button" onClick={loadFromServer}>Загрузить сохранённую версию (изменения в форме будут потеряны)</button>
          </p>
        )}
        {startPreview.isError && <p className="error">{errorMessage(startPreview.error)}</p>}
        {approveMutation.isError && <p className="error">{errorMessage(approveMutation.error)}</p>}
        {approveMutation.isSuccess && <p>Сценарий утверждён.</p>}
      </footer>
    </section>
  );
}
