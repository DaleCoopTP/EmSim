# ADR-006. Evidence, единый вход оценки и ручная оценка без автоматики

Статус: accepted · уточнён ADR-016 от 2026-09-18

## Решение
- Закрытие сохраняет неизменяемый evidence: действия до cutoff, события, манифест записи, серверные сроки и interruptions. Поздние команды/control_report его не меняют.
- call_end объявляет hash/размер/MIME; поздняя загрузка принимает только этот файл до upload deadline. Аудио до upload находится в памяти вкладки и может потеряться.
- STT после close сохраняется в tasks.result. Coordinator удерживает задачи-зависимости до копирования полных текстов/происхождения в assessment_inputs либо отмены оценки.
- Единственный assessment_inputs содержит rubric_effective, результаты правил, тексты STT и причины отсутствия, версии модели/промптов и semantic_input. Отдельных call_transcripts, assessment_rule_results и prepare-задачи нет. Нет отдельного UI предварительных правил.
- Ждём upload deadline только для отсутствующего файла; загруженный файл ждёт терминальную задачу STT, без общего лимита очереди. Coordinator polling каждые 2 с, без LISTEN, не занимает LLM-слот/attempts.
- После sealed input evaluate считает одну auto revision=1. needs_review/unavailable → score=NULL, passed=NULL; unavailable не нормируется в полный балл.
- Исчерпание inference-попыток через общий finalizer handler/reaper атомарно создаёт needs_review auto и failed/dead_letter. Ошибка подготовки без input допускает failed без auto.
- Expert доступна после close независимо от автоматики. base_revision=0 при отсутствии оценки; первая expert тогда revision=2, input_id=NULL. Причина и полный набор применимых критериев обязательны. Иначе base — текущая итоговая ревизия, новая = base+1.
- Под item lock expert отменяет незавершённую evaluate; auto после expert запрещена. Последняя expert — итог; пара ИИ/эксперт сохраняется только при существующей auto. Auto-пересчёта нет.

## Последствия
Снимок для воспроизведения один; task results можно очистить только после переноса содержимого или отмены зависимой оценки. Неизменяемость evidence/inputs/revisions защищена DDL. Поля без обязательного содержимого — 0, технически отсутствующий источник — unavailable.
