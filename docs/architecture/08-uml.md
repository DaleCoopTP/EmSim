# UML и последовательности

Редактируемые схемы Mermaid; исходники лежат в `diagrams/*.mmd`. Это целевая модель. Полные правила и ограничения находятся в спецификации.

## Карта контекстов

```mermaid
flowchart TB
  I[Identity & Access<br/>роль + принадлежность ресурса]
  C[Curriculum<br/>сценарии · рубрики · материалы]
  K[Catalog<br/>ЕКП · службы · карточки-источники]
  T[Teaching<br/>занятие · назначения · start / stop]
  R[Training — core domain<br/>run · item · диалог · карточка · реакции]
  A[Assessment — core domain<br/>правила · semantic judge · экспертная ревизия]
  P[Reporting<br/>история · прогресс · отчёты]
  X[Execution kernel из core<br/>tasks · leases · fencing · recovery]
  O[Operations<br/>аудит · backup · конфигурация]
  C -->|PublishedScenario + Rubric| T
  K -->|Frozen source pool| T
  T -->|LessonPlan + Assignment| R
  C -->|Immutable snapshot| R
  R -->|RunEvidenceV1| A
  A -->|AssessmentResultV1| P
  R -->|Кандидат карточки + approval| K
  I -.-> T
  I -.-> R
  I -.-> A
  X -.-> R
  X -.-> A
  T -.-> O
  R -.-> O
  A -.-> O
```

## Агрегаты и отношения

```mermaid
classDiagram
  class Lesson {
    +LessonId id
    +UserId instructorId
    +LessonState state
    +Epoch interactionEpoch
    +LessonPlanSnapshot plan
    +Start()
    +RequestStop(reason)
  }
  class Assignment {
    +UserId operatorId
    +ServiceProfileId serviceProfile
    +AttemptPolicy attempts
  }
  class TrainingRun {
    +RunId id
    +RunState state
    +Version version
    +Epoch interactionEpoch
    +uint64 eventSequence
    +Apply(command)
    +OfferNextCard()
    +Seal(cutoff)
  }
  class RunItem {
    +ItemId id
    +RunMode mode
    +ItemState state
    +SourceVersion source
    +Submit()
  }
  class IncidentCard {
    +Address address
    +FeatureSet features
    +string description
    +NotificationList services
  }
  class ServiceReaction {
    +ServiceId service
    +ReactionStatus status
    +Record(status, comment)
  }
  class Conversation {
    +TurnId pendingTurnId
    +Epoch conversationEpoch
    +DisclosureState disclosed
  }
  class RunEvidence {
    +Digest digest
    +uint64 cutoffSequence
    +Coverage coverage
  }
  class Assessment {
    +EvidenceRef evidence
    +RubricSnapshot rubric
    +AssessmentState state
    +ScoreBreakdown criteria
    +PublishRevision()
  }
  class ExecutionTask {
    +ScopeId scope
    +TaskKind kind
    +string dedupKey
    +uint64 leaseToken
  }
  Lesson "1" --> "1..*" Assignment : assigns
  Assignment "1" --> "0..*" TrainingRun : attempts
  TrainingRun "1" *-- "1..*" RunItem : serial active item
  RunItem "1" *-- "1" IncidentCard
  IncidentCard "1" *-- "0..*" ServiceReaction
  RunItem "1" *-- "0..1" Conversation
  TrainingRun "1" --> "1..*" RunEvidence : seals
  RunEvidence "1" --> "0..*" Assessment : immutable input
  ExecutionTask ..> TrainingRun : technical owner only
  ExecutionTask ..> Assessment : executes handler
```

## Задача 1: один интерактивный ход

```mermaid
sequenceDiagram
  actor O as Оператор
  participant UI as Browser + IndexedDB
  participant API as Training API
  participant DB as PostgreSQL
  participant W as Turn worker
  participant L as Local CallerSimulator
  O->>UI: Реплика / заполнение карточки
  UI->>UI: Сохранить command_id и intent
  UI->>API: POST command, expected_version, epoch
  API->>DB: BEGIN; lesson SHARE; run UPDATE; dedup
  API->>DB: Message/action + audit + event + turn task + receipt
  API->>DB: COMMIT
  API-->>UI: ACK(version, sequence)
  W->>DB: Claim task, lease token; read frozen input
  W->>L: Generate one turn (вне DB transaction)
  L-->>W: Text + disclosed fact IDs
  W->>DB: BEGIN; check lesson/run epoch + turn + lease
  alt Ещё активен и lease актуален
    W->>DB: Message + task done + event + outbox; COMMIT
    DB-->>API: Wakeup hint / poll
    API-->>UI: SSE persisted event(sequence)
    UI-->>O: Реплика заявителя
  else Stop или lease lost
    W->>DB: Rollback effect; cancel/reconcile
  end
  Note over W,L: Во время ожидания человека worker свободен
```

## Остановка преподавателем

```mermaid
sequenceDiagram
  actor I as Преподаватель
  participant API as Teaching API
  participant DB as PostgreSQL barrier
  participant W as Turn worker
  participant M as Maintenance + media
  participant A as Assessment
  W->>W: Inference начат вне транзакции
  I->>API: StopLesson(command_id, reason)
  API->>DB: BEGIN; lesson FOR UPDATE
  API->>DB: state=stopping; epoch++; audit; close job
  API->>DB: COMMIT — точка остановки
  API-->>I: Stop effective; closing runs
  W->>DB: CommitTurn: lesson FOR SHARE + epoch check
  DB-->>W: Denied: lesson stopped / stale epoch
  M->>DB: Seal each run, cutoff, interrupted item
  M->>DB: Cancel interaction tasks; retain evidence; terminal run
  M->>M: Stop bridge/playback by epoch
  M->>DB: Ensure missing assessment jobs; lesson finished
  DB-->>A: Immutable evidence
  A->>DB: Result / needs_review / unavailable
  Note over DB,A: Judge не удерживает занятие открытым
```

## Независимые жизненные циклы

```mermaid
stateDiagram-v2
  state Lesson {
    [*] --> draft
    draft --> ready: validate
    ready --> running: start
    running --> stopping: finish / stop / technical abort
    stopping --> finished: all runs sealed
  }
  state TrainingRun {
    [*] --> active
    active --> sealing: submit run / stop / failure
    sealing --> completed: normal
    sealing --> stopped: instructor stop
    sealing --> failed: technical failure
  }
  state Assessment {
    [*] --> pending
    pending --> evaluating
    evaluating --> result_ready: valid criteria
    evaluating --> needs_review: insufficient evidence
    evaluating --> unavailable: technical retries exhausted
    needs_review --> result_ready: new expert revision
    unavailable --> pending: explicit new evaluation revision
  }
```

## Реакция службы: пример policy

```mermaid
stateDiagram-v2
  [*] --> added: служба в списке
  added --> received: открытие / получение
  received --> accepted: принятие
  received --> not_accepted: отказ с комментарием
  not_accepted --> accepted: решение реагировать
  accepted --> responding: выезд
  accepted --> arrived: по разрешённой policy
  accepted --> working: по разрешённой policy
  accepted --> completed: работы завершены
  accepted --> refused: отказ с комментарием
  responding --> arrived
  arrived --> working
  working --> completed
  responding --> refused
  arrived --> refused
  working --> refused
  completed --> [*]
  refused --> [*]
  note right of accepted
    Пример policy по памятке, не полный нормативный граф.
    Переходы утверждаются по службе и версии.
    Пропуск этапа может быть допустим структурно,
    но ошибочен по рубрике конкретного кейса.
  end note
  note right of not_accepted
    Для 103 отказные статусы заменяются
    completed_without_team.
    Обязательность комментария для 104
    определяется отдельной policy.
  end note
```

## Восстановление после обрыва сети

```mermaid
sequenceDiagram
  participant UI as Browser + local buffer
  participant API as Any API replica
  participant DB as PostgreSQL
  UI->>API: Command X
  API->>DB: Commit X + receipt
  API--xUI: ACK потерян
  Note over UI,API: Обрыв 5 / 15 / 30 секунд
  UI->>UI: Сохранить новые intents локально
  UI->>API: Re-auth if needed; reconnect(run, lastSeq)
  API->>DB: Authorize + snapshot + receipts
  API-->>UI: Snapshot, run state, event frontier
  UI->>API: Повтор Command X с тем же ID
  API->>DB: Read receipt after authorization
  API-->>UI: Original result; replayed=true
  UI->>API: Следующий неподтверждённый intent Y
  alt Run активен, версия согласована
    API->>DB: Apply Y atomically
    API-->>UI: ACK
  else Run sealed / конфликт
    API->>DB: Preserve recovered input, no score mutation
    API-->>UI: recovered_only / conflict + current version
  end
  UI->>API: SSE after current per-run sequence
  API-->>UI: Persisted events, duplicates deduplicated
```

