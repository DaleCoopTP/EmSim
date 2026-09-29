import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import { useLogout } from "../../api/auth";
import type { Me } from "../../api/useMe";
import { useItem, type ItemSummary, type MyRun } from "../../api/workplace";
import { armCardNumber, armOperatorNumber, armShortName, formatPhone } from "../../arm112Number";
import {
  BadgeIcon, BookmarkIcon, ChartIcon, CheckCircleIcon, ChevronDownIcon, ChevronUpIcon, ClipboardIcon, CloseIcon, DocIcon, EditIcon, ExitIcon,
  GearIcon, GlassesIcon, GlobeIcon, HeadsetIcon, HelicopterIcon, HelpIcon, InfoIcon, MenuIcon, PushpinIcon, ScreenIcon, SearchIcon, StopwatchIcon,
} from "../../components/Arm112Icons";
import type { IntakeItem } from "./Operator112Workplace";
import { Arm112Tour } from "./Arm112Tour";
import "../../arm112-main.css";

const unavailable = "Недоступно в учебном АРМ";

// The ARM-112 main screen for an operator 112 run: the search panel and the
// dark operator block on top, then the grid "Список происшествий"
// (docs/reference-ui, instruction p. 12). Only "журнал" and "отчеты" lead
// somewhere; the other tabs and switches mirror the reference and are off.
// The DDS workplace uses the same screen (user decision 2026-09-29) with its
// own status column: statusOf maps a row to the status shown in the grid.
export function Arm112Main({ me, run, items, search, onSearch, onOpen, notices, statusOf = statusLabel }: {
  statusOf?: (item: ItemSummary) => string;
  me: Me;
  run: MyRun;
  items: ItemSummary[];
  search: string;
  onSearch: (value: string) => void;
  onOpen: (id: string, accept?: boolean) => void;
  notices?: ReactNode;
}) {
  const [tourOpen, setTourOpen] = useState(false);
  const mainRef = useRef<HTMLElement>(null);
  const tourButtonRef = useRef<HTMLButtonElement>(null);
  const closeTour = useCallback(() => {
    setTourOpen(false);
    window.requestAnimationFrame(() => tourButtonRef.current?.focus());
  }, []);
  const needle = search.trim().toLocaleLowerCase("ru-RU");
  const visible = needle === "" ? items : items.filter((candidate) =>
    [candidate.card_number, armCardNumber(candidate.card_number), candidate.incident_type, candidate.address_short]
      .filter(Boolean).some((value) => value!.toLocaleLowerCase("ru-RU").includes(needle)));
  const operatorNo = armOperatorNumber(me.user.id);
  const workstationNo = String(run.workstation_no).padStart(3, "0");

  return <section ref={mainRef} className="arm112-main" aria-label="Главный экран АРМ-112">
    <header className="arm112-main-top">
      <div className="arm112-main-search">
        <label className="arm112-main-search-line">
          <input type="search" aria-label="Поиск происшествий" placeholder="Поиск происшествий" value={search} onChange={(event) => onSearch(event.target.value)} />
          <SearchIcon size={30} />
        </label>
        <div className="arm112-main-search-foot">
          <button type="button" className="arm112-main-link" disabled title={unavailable}>расширенный по параметрам <ChevronDownIcon size={12} /></button>
          <button type="button" className="arm112-main-reset" onClick={() => onSearch("")}>сбросить</button>
        </div>
      </div>
      <OperatorBlock me={me} operatorNo={operatorNo} workstationNo={workstationNo}
        is112={run.exercise_type === "operator112_intake"} tourButtonRef={tourButtonRef}
        onStartTour={() => setTourOpen(true)} />
    </header>

    {notices}

    <section className="arm112-main-list" aria-labelledby="arm112-main-list-title">
      <header className="arm112-main-list-head">
        <h2 id="arm112-main-list-title">Список происшествий <ChevronUpIcon size={16} /></h2>
        <div className="arm112-main-switches">
          <span className="arm112-main-switch is-info"><InfoIcon size={16} /> уведомления</span>
          <span className="arm112-main-switch is-framed" data-tour-target="auto-update"><span className="arm112-main-toggle is-on" aria-hidden="true" /> автообновление</span>
          <span className="arm112-main-switch"><span className="arm112-main-toggle" aria-hidden="true" /> обращения в очереди</span>
          <select aria-label="Что показать" disabled title={unavailable}><option>выберите что показать</option></select>
        </div>
      </header>
      <div className="arm112-main-grid-wrap">
        <table className="arm112-main-grid" data-tour-target="grid">
          <colgroup>
            <col className="c-chevron" /><col className="c-links" /><col className="c-icon" /><col className="c-icon" /><col className="c-icon" />
            <col className="c-num" /><col className="c-num" /><col className="c-number" /><col className="c-date" /><col className="c-time" />
            <col /><col className="c-victims" /><col className="c-status" /><col className="c-address" /><col className="c-icon" /><col className="c-check" />
          </colgroup>
          <thead>
            <tr>
              <th /><th>Связи</th><th colSpan={3}>ЧС</th><th>Опер.</th><th>АРМ</th><th>Номер</th><th>Дата <span aria-hidden="true">↓</span></th><th>Время</th>
              <th>Тип происшествия</th><th>Постр.</th><th>Статус</th><th>Адрес</th><th /><th>Проверена</th>
            </tr>
          </thead>
          {visible.map((candidate) => <GridRow key={candidate.id} item={candidate} operatorNo={operatorNo} workstationNo={workstationNo} onOpen={onOpen} status={statusOf(candidate)} />)}
          {visible.length === 0 && <tbody><tr><td colSpan={16} className="arm112-main-empty">
            {items.length === 0 ? "Новых карточек пока нет." : "По этому запросу происшествий нет."}</td></tr></tbody>}
        </table>
      </div>
      <footer className="arm112-main-foot">
        <div className="arm112-main-lesson">
          <h1>{run.lesson.title}</h1>
          <span>{run.mode === "intro" ? "ознакомительный режим" : "тренировка"} · в очереди {run.queue_left}</span>
        </div>
        <span>Страница: 1 <ChevronDownIcon size={10} /></span>
        <span>Записей на странице: 10 <ChevronDownIcon size={10} /></span>
        <span>{visible.length ? `1-${visible.length}` : "0"} из {visible.length}</span>
      </footer>
    </section>

    <IncomingCallBanner items={items} onAccept={(id) => onOpen(id, true)} />
    {tourOpen && <Arm112Tour rootRef={mainRef} onClose={closeTour} />}
  </section>;
}

function OperatorBlock({ me, operatorNo, workstationNo, is112, tourButtonRef, onStartTour }: {
  me: Me;
  operatorNo: string;
  workstationNo: string;
  is112: boolean;
  tourButtonRef: React.RefObject<HTMLButtonElement>;
  onStartTour: () => void;
}) {
  const logout = useLogout();
  const navigate = useNavigate();
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const interval = window.setInterval(() => setNow(new Date()), 1_000);
    return () => window.clearInterval(interval);
  }, []);
  const capitalize = (value: string) => value.charAt(0).toLocaleUpperCase("ru-RU") + value.slice(1);
  const date = `${capitalize(now.toLocaleDateString("ru-RU", { weekday: "long" }))}, ${now.getDate()} ${capitalize(now.toLocaleDateString("ru-RU", { month: "long" }))} ${now.getFullYear()}`;
  const two = (value: number) => String(value).padStart(2, "0");
  const tabs: { label: string; icon: ReactNode; to?: string }[] = [
    { label: "журнал", icon: <MenuIcon size={20} />, to: "/my" },
    { label: "экран", icon: <ScreenIcon size={20} /> },
    { label: "статистика", icon: <ChartIcon size={20} /> },
    { label: "УЕР", icon: <GearIcon size={20} /> },
    { label: "БДПН", icon: <DocIcon size={20} /> },
    { label: "вики", icon: <HelpIcon size={20} /> },
    { label: "заявители", icon: <BadgeIcon size={20} /> },
    { label: "техника", icon: <HelicopterIcon size={20} /> },
    { label: "аудит", icon: <GlassesIcon size={20} /> },
    { label: "отчеты", icon: <DocIcon size={20} />, to: "/my/history" },
    { label: "контроль", icon: <EditIcon size={20} /> },
    { label: "смена", icon: <BadgeIcon size={20} /> },
    { label: "регионы", icon: <GlobeIcon size={20} /> },
  ];

  return <div className="arm112-main-operator">
    <div className="arm112-main-status">
      <div className="arm112-main-who">
        <strong>{date}</strong>
        <span>оп. {operatorNo}, {armShortName(me.user.full_name)} <span className="arm112-main-arm">АРМ {workstationNo}</span>
          <button type="button" aria-label="Настроить" title={unavailable} disabled><GearIcon size={14} /></button>
          <button type="button" aria-label="Справка" title={unavailable} disabled data-tour-target="help"><HelpIcon size={14} /></button>
          <button type="button" aria-label="Выйти" title="Выйти" disabled={logout.isPending}
            onClick={() => logout.mutate(undefined, { onSettled: () => navigate("/login", { replace: true }) })}><ExitIcon size={14} /></button>
        </span>
      </div>
      <div className="arm112-main-phone" title="Телефония учебного АРМ подключена">
        <HeadsetIcon size={30} /><span>доступен</span>
      </div>
      <time className="arm112-main-clock" dateTime={now.toISOString()} aria-label="Текущее время">
        {two(now.getHours())}:{two(now.getMinutes())}<sup>:{two(now.getSeconds())}</sup>
      </time>
    </div>
    {is112
      ? <button ref={tourButtonRef} type="button" className="arm112-main-create" onClick={onStartTour}><span>ознакомительный режим</span></button>
      : <button type="button" className="arm112-main-create" disabled title="В тренажёре карточка создаётся входящим вызовом"><span>создать новую карточку</span></button>}
    <nav className="arm112-main-tabs" aria-label="Разделы АРМ">
      {tabs.map((tab) => tab.to
        ? <NavLink key={tab.label} to={tab.to} end>{tab.icon}<span>{tab.label}</span></NavLink>
        : <span key={tab.label} className="is-off" title={unavailable}>{tab.icon}<span>{tab.label}</span></span>)}
    </nav>
  </div>;
}

function statusLabel(item: ItemSummary): string {
  if (item.state === "interrupted") return "Прервана";
  if (item.state === "closed") return item.notified || item.dispatched ? "Отработана" : "Завершена";
  if (item.state === "offered") return "Зарегистрирована";
  return "В работе";
}

function GridRow({ item, operatorNo, workstationNo, onOpen, status }: { item: ItemSummary; operatorNo: string; workstationNo: string; onOpen: (id: string) => void; status: string }) {
  const offered = new Date(item.offered_at);
  const two = (value: number) => String(value).padStart(2, "0");
  const number = armCardNumber(item.card_number);
  const closed = item.state === "closed" || item.state === "interrupted";
  return <tbody className={`arm112-main-row${item.state === "offered" ? " is-new" : ""}`}>
    <tr onClick={() => onOpen(item.id)}>
      <td className="c-chevron"><ChevronDownIcon size={16} /></td>
      <td className="c-links" />
      <td className="c-icon"><BookmarkIcon size={18} /></td>
      <td className="c-icon"><PushpinIcon size={17} /></td>
      <td className="c-icon"><StopwatchIcon size={17} /></td>
      <td className="c-num">{operatorNo}</td>
      <td className="c-num">{workstationNo}</td>
      <td className="c-num">{number}</td>
      <td className="c-num">{two(offered.getDate())}.{two(offered.getMonth() + 1)}.{String(offered.getFullYear()).slice(2)}</td>
      <td className="c-time">{two(offered.getHours())}:{two(offered.getMinutes())}<sup>{two(offered.getSeconds())}</sup></td>
      <td className="c-type">{item.incident_type ?? (item.call_status === "ringing" ? "Входящий вызов" : "")}{item.interruptions.length > 0 && <span title="Карточка прервана перезапуском сервера"> ⚠</span>}</td>
      <td className="c-small">—</td>
      <td className="c-small">{status}</td>
      <td className="c-address">{item.address_short ?? ""}</td>
      <td className="c-icon"><button type="button" aria-label={`Открыть карточку № ${number}`} title="Открыть карточку"
        onClick={(event) => { event.stopPropagation(); onOpen(item.id); }}><ClipboardIcon size={18} /></button></td>
      <td className={`c-check${closed ? " is-done" : ""}`}><CheckCircleIcon size={22} /></td>
    </tr>
  </tbody>;
}

// Instruction p. 3, fig. 3: an incoming call pops up over the list with a
// single "Принять" button. Only a full_case call is answered from here; a
// pre-112-3 incoming_call item keeps its own workplace buttons.
function IncomingCallBanner({ items, onAccept }: { items: ItemSummary[]; onAccept: (id: string) => void }) {
  const [dismissed, setDismissed] = useState<string[]>([]);
  const ringing = items.find((candidate) => candidate.call_status === "ringing" && (candidate.state === "offered" || candidate.state === "opened") && !dismissed.includes(candidate.id));
  const detail = useItem(ringing?.id ?? "", !!ringing);
  const item = detail.data as unknown as IntakeItem | undefined;
  if (!ringing || !item || item.id !== ringing.id || item.intake_state?.mode !== "full_case") return null;
  return <IncomingCallDialog aon={item.card.aon} onAccept={() => onAccept(ringing.id)} onDismiss={() => setDismissed((current) => [...current, ringing.id])} />;
}

export function IncomingCallDialog({ aon, onAccept, onDismiss, busy }: { aon: string; onAccept: () => void; onDismiss?: () => void; busy?: boolean }) {
  return <div className="arm112-call-banner" role="dialog" aria-label="Входящий звонок">
    {onDismiss && <button type="button" className="arm112-call-banner-close" aria-label="Скрыть окно вызова" onClick={onDismiss}><CloseIcon size={18} /></button>}
    <strong>Входящий звонок</strong>
    <span>с номера {formatPhone(aon)}</span>
    <button type="button" className="arm112-call-banner-accept" disabled={busy} onClick={onAccept}>Принять</button>
  </div>;
}
