"""Build offline architecture diagrams and atlas from reviewable Graphviz sources."""
from pathlib import Path
import html
import json
import subprocess
import base64

ROOT = Path(__file__).resolve().parents[1]
DIAGRAMS = ROOT / 'diagrams'


def q(s):
    return json.dumps(s, ensure_ascii=False)


BASE = '''digraph G {
graph [rankdir=TB, newrank=true, bgcolor="white", pad=0.28, nodesep=0.4, ranksep=0.65, splines=polyline, fontname="Arial", fontsize=17, compound=true];
node [shape=box, style="rounded,filled", fillcolor="#f3f6fa", color="#aebdce", penwidth=1.2, fontname="Arial", fontsize=15, margin="0.2,0.14", fontcolor="#17304d"];
edge [color="#6b829b", arrowsize=0.8, penwidth=1.4, fontname="Arial", fontsize=12, fontcolor="#405974"];
'''


def node(name, label, tone='normal'):
    themes = {
        'normal': '',
        'core': ',fillcolor="#173e69",fontcolor="white",color="#173e69"',
        'reuse': ',fillcolor="#eee9fb",color="#8a74b3",fontcolor="#443567"',
        'warn': ',fillcolor="#fff0d7",color="#c7a061",fontcolor="#66471a"',
        'data': ',fillcolor="#e8f4ef",color="#79aa93",fontcolor="#204e3a"',
    }
    return f'{name} [label={q(label)}{themes[tone]}];\n'


def edge(a, b, label='', style='solid'):
    return f'{a} -> {b} [label={q(label)},style={style}];\n'


def graph_context():
    s = BASE
    s += node('actors', 'Оператор · Преподаватель · Администратор\nWeb UI на русском · роли и принадлежность ресурса')
    s += node('iam', 'Identity & Access\nлокальные users / IdP\nsessions · permissions')
    s += node('curr', 'Curriculum\nScenarioVersion · Rubric\nматериалы · публикация')
    s += node('catalog', 'Catalog\nЕКП · службы · CardTemplate\nsystem / student / mixed')
    s += node('teach', 'Teaching\nLesson → Assignment\nплан · назначения · stop')
    s += node('train', 'Training — CORE DOMAIN\nTrainingRun → RunItem\nдиалог + карточка + реакции служб', 'core')
    s += node('assess', 'Assessment — CORE DOMAIN\nправила + semantic judge\nкритерии + экспертные ревизии', 'core')
    s += node('report', 'Reporting\nистория · прогресс · отчёты\nбез LLM на пути чтения', 'data')
    s += node('kernel', 'Execution kernel\nперенос и адаптация core\nleases · fencing · retries', 'reuse')
    s += node('ops', 'Operations\naudit · backup · diagnostics\nуправление конфигурацией')
    s += '{rank=same;curr;catalog;teach;}\n{rank=same;iam;train;kernel;}\n{rank=same;assess;ops;}\n'
    s += edge('actors','teach','управление') + edge('actors','iam','аутентификация')
    s += edge('curr','train','PublishedScenario') + edge('catalog','teach','frozen pool')
    s += edge('teach','train','LessonPlanSnapshot') + edge('train','assess','RunEvidenceV1')
    s += edge('assess','report','AssessmentResultV1') + edge('iam','train','','dashed')
    s += edge('kernel','train','turn / seal jobs','dashed') + edge('kernel','assess','evaluate jobs','dashed')
    s += edge('teach','ops','audit','dashed') + edge('assess','ops','audit revision','dashed')
    return s + '}\n'


def graph_code():
    s = BASE
    s += node('ui', 'web/src\noperator · instructor · admin\ncommand-buffer · event-stream · media')
    s += node('api', 'cmd/api → HTTP / SSE adapters\nauth → DTO validation → application service')
    s += 'subgraph cluster_training { label="internal/training"; labeljust=l; color="#bccbdd"; style=rounded;\n'
    s += node('apps', 'application\nStartRun · ApplyCommand · OfferNextCard\nGenerateTurn · SealRun')
    s += node('domain', 'domain\nRun · Card · Conversation · ServiceReaction\nTimingPolicy · DisclosurePolicy', 'core')
    s += node('ports', 'ports\nTrainingTransaction · TurnCommitter\nCallerSimulator · EvidenceReader')
    s += edge('apps','domain','правила') + edge('apps','ports','зависимости') + '}\n'
    s += node('assess', 'internal/assessment\nDeterministicEvaluator · SemanticJudge\nScoreAggregator · AssessmentCommitter', 'core')
    s += node('pg', 'adapters/postgres\natomic effect + task + audit + outbox\nlocks · unique keys · snapshots', 'data')
    s += node('llm', 'adapters/inference\nлокальный HTTP endpoint\nschema validation · deadlines')
    s += node('workers', 'cmd/worker\ninteractive · assessment · content\nreport · maintenance')
    s += node('exec', 'platform/execution\nqueue · worker · recovery\nадаптированный orchestration core', 'reuse')
    s += '{rank=same;domain;ports;}\n{rank=same;pg;llm;}\n{rank=same;workers;exec;}\n'
    s += edge('ui','api','commands / events') + edge('api','apps','use cases')
    s += 'ports -> pg [label="реализует порт",dir=back,arrowtail=empty,style=dashed];\n'
    s += 'ports -> llm [label="реализует порт",dir=back,arrowtail=empty,style=dashed];\n'
    s += edge('apps','assess','только immutable evidence')
    s += edge('workers','exec','Runner + Handler') + edge('exec','apps','dispatch','dashed')
    s += edge('assess','pg','result commit')
    return s+'}\n'


def graph_flows():
    s=BASE
    s+=node('start','Преподаватель: publish → assign → start\nFreeze scenario / rubric / classifier / source pool')
    s+='subgraph cluster_a {label="ЗАДАЧА 1 · call_card";color="#b0c7df";style=rounded;\n'
    for key,label,tone in [
        ('a1','Входящий текст / голос\nодин ответ CallerSimulator','normal'),
        ('a2','Действие оператора\nсообщение + ручная карточка','core'),
        ('a3','Durable command transaction\nreceipt + evidence + next turn task','data'),
        ('a4','SubmitItem / stop\nготовая или прерванная карточка','normal')]: s+=node(key,label,tone)
    s+=edge('a1','a2')+edge('a2','a3')+edge('a3','a1','следующая реплика')+edge('a3','a4','сохранить')+'}\n'
    s+='subgraph cluster_b {label="ЗАДАЧА 2 · card_workflow";color="#b0c7df";style=rounded;\n'
    for key,label,tone in [
        ('b1','Выбор из frozen pool\nsystem / student / mixed','normal'),
        ('b2','Действия своей службы\nстатус · комментарий · запрос службы','core'),
        ('b3','Workflow + durable command\nтаймер от направления карточки','data'),
        ('b4','SubmitItem / stop\nследующая карточка, если lesson active','normal')]: s+=node(key,label,tone)
    s+=edge('b1','b2')+edge('b2','b3')+edge('b3','b4')+edge('b4','b1','next-card')+'}\n'
    s+=node('evidence','Immutable evidence\nсообщения · карточка · действия · нормы\ncutoff + coverage + hashes','data')
    s+=node('score','Assessment → reporting\nправила + семантика + грамматика\nоценка / review / unavailable','core')
    s+='start -> a1 [lhead=cluster_a];\nstart -> b1 [lhead=cluster_b];\n'
    s+=edge('a4','evidence')+edge('b4','evidence')+edge('evidence','score')
    s+='{rank=same;a1;b1;}\n{rank=same;a2;b2;}\n{rank=same;a3;b3;}\n{rank=same;a4;b4;}\n'
    return s+'}\n'


def graph_stop():
    s=BASE
    s+=node('request','Преподаватель → StopLesson\ncommand_id + причина')
    s+=node('barrier','1. Атомарный барьер\nlesson FOR UPDATE → stopping → epoch++\naudit + durable close job → COMMIT','warn')
    s+=node('late','Поздний ответ LLM / команда / next-card\nпроверка lesson state + epoch\nэффект запрещён после барьера','warn')
    s+=node('seal','2. Завершение runs малыми batches\ncutoff sequence · interrupted item\nсохранить evidence · cancel interaction tasks','data')
    s+=node('terminal','3. TrainingRun = stopped\nLesson = finished, когда runs sealed\nповторный worker безопасен','core')
    s+=node('media','Media stop\nbridge / playback epoch\nне воспроизводить позднее аудио')
    s+=node('judge','4. Assessment продолжается отдельно\nready / needs_review / unavailable\nне удерживает занятие открытым','core')
    s+=node('offline','Ввод, восстановленный после stop\nсохранить как supplemental\nне менять frozen evidence','data')
    s+=edge('request','barrier')+edge('barrier','late','fencing')+edge('barrier','seal','background')
    s+=edge('barrier','media','revoke interaction')+edge('seal','terminal')+edge('terminal','judge')
    s+=edge('offline','judge','только явная expert revision','dashed')
    s+='{rank=same;late;seal;media;}\n{rank=same;terminal;offline;}\n'
    return s+'}\n'


def graph_data():
    s=BASE
    for key,label,tone in [
        ('user','User\nid · role grants','normal'),
        ('lesson','Lesson\nowner · state · epoch','normal'),
        ('assignment','Assignment\noperator · service profile','normal'),
        ('scenario','Published versions\nscenario · rubric · EKП · workflow','normal'),
        ('pool','Source pool\napproved CardTemplateVersion\nsource + provenance + seed','normal'),
        ('run','TrainingRun\nversion · epoch · event_seq\nstate · snapshot_id','core'),
        ('snapshot','RunSnapshot\nimmutable manifest + digest','data'),
        ('item','RunItem\nordinal · source · state','core'),
        ('conversation','Conversation + messages\npending_turn · disclosure\nтекст / audio refs','normal'),
        ('card','IncidentCard\nполя · признаки\nсписок служб','normal'),
        ('reactions','ServiceReaction 0..N\nservice · status\nappend-only history','normal'),
        ('events','Actions / events / receipts\ncommand_id · accepted\nrun_id + sequence','data'),
        ('evidence','EvidenceManifest\ncutoff · coverage · digest','data'),
        ('assessment','Assessment + revisions\ncriteria · nullable score\nautomatic / expert','core'),
        ('result','RunResult + progress\nrevision-aware projection','data'),
        ('tasks','ExecutionTask\nscope + kind + dedup_key\nlease token · status','reuse')]: s+=node(key,label,tone)
    s+='{rank=same;user;lesson;scenario;}\n{rank=same;assignment;pool;snapshot;}\n{rank=same;conversation;card;events;}\n{rank=same;assessment;tasks;}\n'
    for a,b,label in [('user','assignment','1 → N'),('lesson','assignment','1 → N'),('assignment','run','1 → N attempts'),('scenario','snapshot','frozen'),('snapshot','run','1 → 1'),('pool','item','selected version'),('run','item','1 → N'),('item','conversation','0..1'),('item','card','1'),('card','reactions','0..N'),('run','events','1 → N'),('events','evidence','sealed'),('card','evidence','snapshot'),('evidence','assessment','1 → N revisions'),('assessment','result','projection'),('tasks','assessment','execute')]:s+=edge(a,b,label)
    return s+'}\n'


def graph_deployment():
    s=BASE
    s+='subgraph cluster_lan {label="ЛОКАЛЬНЫЙ УЧЕБНЫЙ КОНТУР · без runtime internet";color="#9bb2ca";style=rounded;\n'
    for key,label,tone in [
        ('browser','АРМ: Windows 10/11 · Ubuntu 20.04+\nChrome · Firefox · Яндекс.Браузер\nIndexedDB + local audio buffer','normal'),
        ('phone','IP-телефон\nлокальный SIP-TLS / SRTP','normal'),
        ('proxy','Reverse proxy / LB\nHTTPS · local CA','normal'),
        ('api','API replicas × N\nHTTP commands + SSE\nstateless application processes','core'),
        ('sip','Asterisk / SIP\nWebRTC DTLS-SRTP\nтолько учебный dialplan','normal'),
        ('media','Media gateway replicas\nSTT/TTS bridge · chunks\ncall / playback epochs','core'),
        ('workers','Worker replicas\ninteractive · assessment\ncontent · report · maintenance','reuse'),
        ('db','PostgreSQL primary + sync standby\nTLS · single writable primary\nquorum / fencing для HA','data'),
        ('infer','Локальный inference\ncaller · judge · STT · TTS\nCPU profile / GPU acceleration\nglobal quotas + reserve','normal'),
        ('objects','Local object storage\nаудио · материалы · reports\nimmutable versions','data'),
        ('backup','Независимый backup node\ndaily DB + WAL + blobs\nrestore manifests','data'),
        ('ops','Local IAM · monitoring\nops-agent · audit · alerts\noffline update bundle','normal')]:s+=node(key,label,tone)
    s+='{rank=same;browser;phone;}\n{rank=same;proxy;sip;}\n{rank=same;api;media;}\n{rank=same;workers;infer;}\n{rank=same;db;objects;}\n{rank=same;backup;ops;}\n'
    for a,b,label in [('browser','proxy','HTTPS'),('proxy','api','TLS'),('phone','sip','SIP-TLS / SRTP'),('browser','sip','WSS + WebRTC'),('sip','media','protected media'),('media','infer','local streaming'),('api','db','TLS'),('workers','db','tasks / data, TLS'),('workers','infer','HTTPS, bounded'),('media','objects','chunks'),('db','backup','daily + WAL'),('objects','backup','versioned copy'),('ops','api','readiness / config'),('api','workers','durable DB tasks')]:s+=edge(a,b,label)
    s+='}\n'
    return s+'}\n'


PANELS = [
    ('contexts','01-contexts', 'Контексты', 'Границы предметной области',
     'Сценарий задаёт условия, прохождение производит доказательства, оценка формирует результат. Учебные модули живут в одном Go-приложении; execution kernel обслуживает их через порты.',graph_context,
     [('Lesson ≠ Run ≠ Task','Занятие преподавателя, попытка оператора и техническая задача имеют разных владельцев и жизненные циклы.'),('Два core domain','Training отвечает за достоверность действий. Assessment — за обоснованную оценку.'),('Обязательные источники','Системные, ученические и смешанные карточки проходят публикацию и попадают в frozen source pool.')]),
    ('code','02-code','Код','Компоненты и направление зависимостей',
     'Domain не зависит от HTTP, PostgreSQL и конкретной LLM. Application services используют узкие порты; адаптеры реализуют их. Эффект и завершение task фиксируются одной транзакцией.',graph_code,
     [('Переиспользуем','Leases, fencing, retries, worker lifecycle, snapshots и гарантийные тесты core.'),('Адаптируем','Queue ownership, task kinds, cancellation, per-turn execution, финализацию и SQL.'),('Создаём','IAM, занятия, карточки, пользовательский UI, voice path, rubric scoring и отчёты.')]),
    ('flows','03-flows','Учебные потоки','Два упражнения, единый контракт доказательств',
     'Задача 1 обязательно включает ручное заполнение карточки. Задача 2 выдаёт следующую карточку после завершения предыдущей, пока занятие активно.',graph_flows,
     [('Ожидание человека','Не занимает worker: durable task исполняет только одну реплику симулятора.'),('Статусы','Реакция каждой службы и общий статус карточки — отдельные сущности.'),('Оценка','Время и workflow проверяет код; смысл и грамматику — локальный evaluator с evidence refs.')]),
    ('stop','04-stop','Остановка','Остановка преподавателем и поздние результаты',
     'Точка остановки — commit изменения lesson state и epoch. После неё никакой интерактивный эффект не проходит проверку. Сохранение и оценивание уже накопленного продолжаются.',graph_stop,
     [('Строгий запрет','Проверка актуальности внутри транзакции защищает от потерянных cancel-сообщений.'),('Сохранность','Закрытие run сохраняет partial evidence и не обнуляет результаты.'),('Восстановленный ввод','Поздние offline данные остаются доступны для разбора, но не переписывают оценку автоматически.')]),
    ('data','05-data','Данные','Агрегаты, версии и доказательства',
     'Актуальное состояние хранится в реляционных таблицах; события и evidence дополняют его. Полное event sourcing не требуется. Отдельные результаты и ревизии питают прогресс.',graph_data,
     [('Атомарность','Command + receipt + state + event + audit + task — один DB commit.'),('Воспроизводимость','Снимок фиксирует версии сценария, рубрики, ЕКП, workflow и моделей. Seed не гарантирует одинаковый текст LLM.'),('Идемпотентность','Повтор с тем же ID возвращает receipt; task delivery допускает повторы, domain effect — один.')]),
    ('deployment','06-deployment','Развёртывание','Локальный control plane и отдельный media plane',
     'Все сетевые hop защищены. Базовое приложение не требует GPU; inference масштабируется отдельно. Для отказа узла нужен HA-профиль, одного Compose недостаточно.',graph_deployment,
     [('100 / 20','100 пользователей интерфейса и минимум 20 активных call/card sessions подтверждаются нагрузочным тестом.'),('2с / 150мс / 30с','UI, передача голоса и отчёты — разные бюджеты. Время генерации ответа модели измеряется отдельно.'),('Сеть и backup','ACK выдаётся после commit; клиент сохраняет непереданные данные. Backup — каждый день, на другом узле внутри контура.')]),
]


def build():
    DIAGRAMS.mkdir(parents=True, exist_ok=True)
    sections=[]
    nav=[]
    for index,(pid,stem,label,title,description,fn,notes) in enumerate(PANELS):
        dot=DIAGRAMS/(stem+'.dot')
        dot.write_text(fn(),encoding='utf-8')
        subprocess.run(['dot','-Tsvg',str(dot),'-o',str(DIAGRAMS/(stem+'.svg'))],check=True)
        subprocess.run(['dot','-Tpng','-Gdpi=120',str(dot),'-o',str(DIAGRAMS/(stem+'.png'))],check=True)
        nav.append(f'<button role="tab" id="tab-{pid}" aria-controls="panel-{pid}" aria-selected="{str(index==0).lower()}" type="button">{label}</button>')
        cards=''.join(f'<div><strong>{html.escape(a)}</strong><p>{html.escape(b)}</p></div>' for a,b in notes)
        embedded = base64.b64encode((DIAGRAMS/(stem+'.svg')).read_bytes()).decode('ascii')
        sections.append(f'''<section id="panel-{pid}" role="tabpanel" aria-labelledby="tab-{pid}" {"hidden" if index else ""}>
<div class="panel-heading"><div><h2>{title}</h2><p>{description}</p></div><a class="source" href="diagrams/{stem}.svg" target="_blank" rel="noopener">Открыть SVG ↗</a></div>
<div class="diagram"><img src="data:image/svg+xml;base64,{embedded}" alt="{html.escape(title)}"></div>
<div class="notes">{cards}</div></section>''')
    body='''<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>EmSim — архитектурный атлас</title>
<style>
:root{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Arial,sans-serif;color:#17304d;background:#f3f6fa;font-size:16px;color-scheme:light}*{box-sizing:border-box}body{margin:0}main{max-width:1480px;margin:auto;padding:36px 40px 48px}header{border-bottom:1px solid #cad5e3;padding-bottom:24px}.eyebrow{font-size:13px;letter-spacing:.09em;color:#466486;text-transform:uppercase}h1{font-size:34px;letter-spacing:-.03em;margin:10px 0 12px;font-weight:650}header p{max-width:900px;font-size:17px;line-height:1.55;margin:0;color:#45617f}.status{margin-top:14px;font-size:13px;color:#687b91}nav{display:flex;gap:6px;flex-wrap:wrap;margin:24px 0}button{font:inherit;cursor:pointer;border:1px solid #c4d0e0;border-radius:7px;background:white;color:#345270;padding:10px 17px;min-height:44px}button[aria-selected=true]{background:#173e69;color:white;border-color:#173e69}button:hover{border-color:#173e69}button:focus-visible,a:focus-visible{outline:3px solid #de9e30;outline-offset:3px}section{border:1px solid #d5deea;border-radius:10px;background:white;overflow:hidden}.panel-heading{padding:24px 28px 16px;display:flex;align-items:flex-start;justify-content:space-between;gap:24px}h2{font-size:23px;line-height:1.3;margin:0 0 9px;font-weight:600}.panel-heading p{font-size:15px;line-height:1.55;max-width:920px;margin:0;color:#48627e}.source{white-space:nowrap;font-size:14px;padding-top:6px}a{color:#225b94;text-underline-offset:3px}.diagram{padding:8px 24px 24px;overflow:auto;background:#fff}.diagram img{display:block;max-width:100%;height:auto;max-height:1000px;margin:auto}.notes{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:28px;padding:22px 28px;background:#f7f9fc;border-top:1px solid #dde5ef}.notes strong{font-size:15px;font-weight:600}.notes p{font-size:14px;line-height:1.55;color:#49637e;margin:7px 0 0}.documents{margin-top:28px}.documents h2{font-size:18px}.doc-links{display:flex;flex-wrap:wrap;gap:10px 24px;font-size:14px;line-height:1.6}.legend{display:flex;gap:24px;flex-wrap:wrap;margin:15px 0 0;font-size:13px;color:#516780}.legend span:before{content:"";display:inline-block;width:12px;height:12px;background:#173e69;border-radius:2px;margin-right:7px;vertical-align:-1px}.legend .reuse:before{background:#aa94cf}.legend .data:before{background:#88b59e}.legend .barrier:before{background:#dfb66e}footer{font-size:13px;color:#627891;margin-top:24px;line-height:1.6} [hidden]{display:none!important}@media(max-width:800px){main{padding:22px 16px}h1{font-size:28px}.panel-heading{padding:20px;display:block}.source{display:inline-block;margin-top:12px}.notes{grid-template-columns:1fr;gap:18px;padding:20px}.diagram{padding:8px}.diagram img{min-width:680px;max-height:none}header p{font-size:16px}nav button{padding:9px 12px}}@media print{nav{display:none}section[hidden]{display:block!important}section{break-before:page;border:0}main{padding:0}.source{display:none}.diagram img{min-width:0;max-height:800px}.notes{break-inside:avoid}}
</style></head><body><main><header><div class="eyebrow">EmSim / архитектура / 16 сентября 2026</div><h1>От учебного сценария до доказуемой оценки</h1><p>Два учебных процесса, надёжная интерактивная сессия и переиспользование orchestration core. Карта решений для перехода к полному плану реализации.</p><div class="status">Проект v1.0 · 26 обязательных дополнительных требований сопоставлены с компонентами и приёмкой · испытания продукта ещё предстоят</div></header>
<nav role="tablist" aria-label="Представления архитектуры">NAV</nav>
PANELS
<div class="legend" aria-label="Обозначения"><span>Core domain / application</span><span class="reuse">Адаптация orchestration core</span><span class="data">Данные / durable effects</span><span class="barrier">Барьер остановки</span></div>
<div class="documents"><h2>Спецификация и основание плана</h2><div class="doc-links"><a href="01-system-design.md">Архитектурное решение</a><a href="02-core-reuse.md">Проверка и перенос core</a><a href="03-requirements.md">26 требований → приёмка</a><a href="04-contracts.md">API и code-level контракты</a><a href="05-data-and-consistency.md">Данные и транзакции</a><a href="06-delivery-and-validation.md">12 рабочих пакетов</a><a href="07-decisions.md">ADR и защита решений</a><a href="README.md">Все источники и навигация</a></div></div>
<footer>Модульный Go backend · PostgreSQL · локальный inference · WebRTC / SIP. Диаграммы описывают целевую архитектуру, не текущую готовность кода. Параметры минимального железа, AI latency и сохранности аудио закрываются ранними испытаниями.</footer></main>
<script>
const tabs=[...document.querySelectorAll('[role=tab]')];
function select(tab){tabs.forEach(t=>{const on=t===tab;t.setAttribute('aria-selected',String(on));document.getElementById(t.getAttribute('aria-controls')).hidden=!on});history.replaceState(null,'','#'+tab.id.replace('tab-',''));}
tabs.forEach((tab,index)=>{tab.addEventListener('click',()=>select(tab));tab.addEventListener('keydown',e=>{if(e.key==='ArrowRight'||e.key==='ArrowLeft'){e.preventDefault();const next=tabs[(index+(e.key==='ArrowRight'?1:-1)+tabs.length)%tabs.length];select(next);next.focus()}})});
const initial=tabs.find(t=>t.id==='tab-'+location.hash.slice(1));if(initial)select(initial);
</script></body></html>'''
    (ROOT/'architecture-atlas.html').write_text(body.replace('NAV',''.join(nav)).replace('PANELS','\n'.join(sections)),encoding='utf-8')
    uml_titles = {
        '01-contexts':'Карта контекстов',
        '02-domain':'Агрегаты и отношения',
        '03-dialogue':'Задача 1: один интерактивный ход',
        '04-stop':'Остановка преподавателем',
        '05-states':'Независимые жизненные циклы',
        '06-reaction':'Реакция службы: пример policy',
        '07-reconnect':'Восстановление после обрыва сети',
    }
    uml = '# UML и последовательности\n\nРедактируемые схемы Mermaid; исходники лежат в `diagrams/*.mmd`. Это целевая модель. Полные правила и ограничения находятся в спецификации.\n\n'
    for stem,title in uml_titles.items():
        uml += f'## {title}\n\n```mermaid\n{(DIAGRAMS/(stem+".mmd")).read_text().strip()}\n```\n\n'
    (ROOT/'08-uml.md').write_text(uml,encoding='utf-8')


if __name__=='__main__':
    build()
