# TASKS: MCP Browser Control

## 1. Назначение

Этот файл преобразует требования из `PRD.md` в исполнимый план. Задачи расположены в рекомендуемом порядке. Идентификаторы стабильны и могут использоваться в issue tracker, commit messages и pull requests.

## 2. Обозначения

- `[ ]` — не начато
- `[~]` — частично реализовано или требует переработки
- `[x]` — завершено и проверено
- **P0** — необходимо для MVP
- **P1** — необходимо для расширенной автоматизации
- **P2** — чувствительная или экспертная функция

## 3. Текущее состояние

- `[x]` Go-сервер разделён на protocol, registry, router, selection, tools и transport packages.
- `[x]` Работают STDIO, Streamable HTTP и отдельный legacy SSE режим.
- `[x]` WebSocket требует pairing/authentication и дополнительно ограничен loopback Host/extension Origin.
- `[x]` Реализованы protocol-v1 handshake, стабильный `browserId` и атомарная замена `connectionId` при reconnect.
- `[x]` Broadcast удалён: команды адресуются ровно одному браузеру.
- `[x]` Корреляция учитывает `browserId + connectionId + requestId`.
- `[~]` Базовые page tools и tabs list работают; полный каталог browser automation ещё не реализован.
- `[x]` Создано минимальное Manifest V3 расширение с English UI и optional host access.
- `[x]` Добавлены Go unit/race tests, WebSocket integration tests, JavaScript protocol tests и двухбраузерный MCP integration test.
- `[x]` Суммарное покрытие внутренних Go-пакетов первого инкремента — не менее 80%.
- `[x]` Точка входа перенесена в `cmd/server`, локальные операции унифицированы в `Makefile`.

## 4. Общий Definition of Done

Задача считается завершённой, когда:

1. Реализация соответствует `PRD.md` и не меняет публичный контракт без обновления schema.
2. Код, названия, UI и сообщения продукта написаны на английском.
3. Добавлены позитивные и негативные тесты.
4. `go test ./...` и применимые extension tests проходят.
5. Для конкурентного Go-кода проходит `go test -race ./...`.
6. Ошибки структурированы, а чувствительные данные не попадают в логи.
7. Документация обновлена вместе с поведением.
8. Изменение проверено хотя бы на одном реальном Chromium-браузере, если оно использует Browser Extensions API.

## 5. Этап 0 — решения и границы продукта

### T-001 — Согласовать продуктовые вопросы

- Приоритет: P0
- Зависимости: нет
- Статус: `[ ]`

Нужно решить и записать в `PRD.md`:

- только localhost или также удалённые браузеры;
- Chrome/Chromium only или обязательный Firefox;
- доступность raw CDP и arbitrary JavaScript;
- необходимость personal-data инструментов;
- unpacked, enterprise или Chrome Web Store distribution;
- политика конкурентного управления одной вкладкой;
- какие действия требуют подтверждения пользователя.

До решения использовать допущения из раздела 18 PRD.

### T-002 — Зафиксировать матрицу браузеров и версий

- Приоритет: P0
- Зависимости: T-001
- Статус: `[ ]`

Результат:

- список поддерживаемых Chrome, Edge и других Chromium-браузеров;
- минимальная версия для WebSocket service worker и используемых APIs;
- таблица capabilities по браузерам;
- решение о namespace `chrome`/`browser` и compatibility layer.

### T-003 — Зафиксировать permission profiles

- Приоритет: P0
- Зависимости: T-001
- Статус: `[ ]`

Определить точный состав Core, Observe, Debug и Personal data profiles. Для каждого permission описать:

- зачем он нужен;
- install-time или optional;
- какой warning видит пользователь;
- какие tools становятся доступны;
- какие данные необходимо редактировать.

## 6. Этап 1 — структура проекта и контракт

### T-010 — Разделить Go-приложение на пакеты

- Приоритет: P0
- Зависимости: нет
- Статус: `[x]`

Предлагаемая структура:

```text
cmd/server/
internal/config/
internal/protocol/
internal/registry/
internal/router/
internal/transport/mcp/
internal/transport/websocket/
internal/tools/
internal/security/
internal/artifacts/
```

Критерии:

- `main.go` содержит только сборку зависимостей и запуск;
- пакеты не используют изменяемые глобальные singleton;
- зависимости передаются явно;
- текущая функциональность сохраняется или мигрируется осознанно.

### T-011 — Спроектировать protocol v1

- Приоритет: P0
- Зависимости: T-010
- Статус: `[~]`

Реализовать envelope и типы `hello/welcome/pair/request/response/cancel/event/ping/pong` из FR-4.

Критерии:

- единый lower camelCase JSON casing;
- `protocolVersion` обязателен;
- request/response содержат `browserId` и `requestId`;
- target отделён от params;
- неизвестные обязательные версии отклоняются;
- неизвестные необязательные поля игнорируются;
- есть golden JSON fixtures.

### T-012 — Добавить JSON Schema контракта

- Приоритет: P0
- Зависимости: T-011
- Статус: `[x]`

Создать schema для всех envelope и общих payload. Настроить:

- validation тестовых fixtures;
- проверку Go и JavaScript реализаций на одном наборе fixtures;
- правило backward-compatible изменений;
- версионирование schema.

Добавлены Draft 2020-12 schema v1, политика совместимости и единый набор golden fixtures. Fixtures независимо проверяются `jsonschema/v6` в Go и Ajv 2020 в JavaScript.

### T-013 — Реализовать общую модель ошибок

- Приоритет: P0
- Зависимости: T-011
- Статус: `[x]`

Добавить коды из раздела 13 PRD, typed Go errors и JavaScript error mapping.

Критерии:

- клиент получает стабильный `code`;
- stack trace не уходит клиенту по умолчанию;
- `retryable` выставляется последовательно;
- target и requestId присутствуют в диагностике;
- ошибки Chrome API переводятся в продуктовые коды.

Реализованы единые typed errors, безопасная нормализация расширения, диагностика запроса и target, а также отображение ошибок Chrome API без утечки внутренних сообщений и stack trace.

### T-014 — Реализовать общий Target и Locator

- Приоритет: P0
- Зависимости: T-011
- Статус: `[x]`

Target включает `browserId/windowId/tabId/frameId/documentId`. Locator поддерживает CSS, XPath, text, role/name, label, placeholder, alt, title, test id, coordinates, nth и strict.

Добавить validation:

- tab/window/frame не принимаются без браузера после разрешения default selection;
- locator содержит ровно одну основную стратегию;
- координаты, nth и timeout имеют допустимые границы;
- устаревшие element/document references возвращают `STALE_TARGET`.

Target и Locator синхронизированы в Go, JavaScript и JSON Schema. Target привязывается к разрешённому browserId, а document-scoped target и element reference проверяются расширением до выполнения команды.

### T-015 — Ввести конфигурацию и безопасные defaults

- Приоритет: P0
- Зависимости: T-010
- Статус: `[x]`

Поддержать flags/env/config file для:

- MCP transport и address;
- WebSocket address;
- pairing и token storage;
- timeouts и payload limits;
- origin allowlist;
- permission/tool profiles;
- artifact directory и TTL;
- log level и redaction.

По умолчанию оба сетевых endpoint слушают loopback.

Реализован приоритет defaults → JSON file → environment → flags, строгая валидация, обязательный loopback bind, origin allowlist и применение настраиваемых timeout/payload/pairing limits. Все параметры и имена переменных окружения описаны в `docs/configuration.md`.

## 7. Этап 2 — сервер: соединения, registry и routing

### T-020 — Реализовать Browser Registry

- Приоритет: P0
- Зависимости: T-011, T-013
- Статус: `[x]`

Registry хранит:

- постоянный `browserId`;
- текущее `connectionId` и состояние;
- display name и browser metadata;
- capabilities и permissions;
- timestamps, latency и disconnect reason;
- ссылку на безопасный connection writer.

Критерии:

- конкурентные register/get/list/remove безопасны;
- duplicate `browserId` обрабатывается атомарно;
- старое соединение не может удалить новое;
- snapshot registry не раскрывает внутренние mutable objects.

Registry атомарно заменяет подключения, хранит активные и недавно отключённые immutable snapshots, сохраняет latency/reason/timestamps и защищает новое соединение от stale disconnect. Для очистки истории добавлен безопасный prune по времени.

### T-021 — Реализовать безопасную WebSocket connection abstraction

- Приоритет: P0
- Зависимости: T-015, T-020
- Статус: `[x]`

Добавить отдельные read/write pumps:

- один writer на connection;
- bounded send queue;
- read/write deadlines;
- ping/pong и lastSeen;
- max message size;
- graceful close;
- reconnect replacement;
- backpressure error вместо зависания.

Connection использует единственный write pump с bounded queue, немедленный `BACKPRESSURE`, write deadlines, control ping, pong/read deadline и graceful close frame. Ошибка writer закрывает соединение и разблокирует все ожидающие Send; stale connection не влияет на replacement.

### T-022 — Реализовать pairing и authentication

- Приоритет: P0
- Зависимости: T-015, T-021
- Статус: `[x]`

Минимальный поток:

1. Сервер создаёт одноразовый pairing code.
2. Пользователь вводит его в UI расширения.
3. Расширение получает долгоживущий credential.
4. Последующие handshake аутентифицируются.
5. Credential можно отозвать на сервере и удалить из расширения.

Добавить rate limit, expiration и защиту от replay.

Реализованы одноразовый восьмизначный код, TTL, глобальный лимит неуспешных попыток, атомарное потребление кода, SHA-256 hash store с правами владельца, authenticated reconnect и подтверждаемое revoke с обеих сторон.

### T-023 — Реализовать targeted Router

- Приоритет: P0
- Зависимости: T-020, T-021
- Статус: `[x]`

Удалить broadcast как способ выполнения target-команд.

Критерии:

- request отправляется ровно одному browser connection;
- несуществующий browser возвращает `BROWSER_NOT_FOUND`;
- отключённый browser возвращает `BROWSER_DISCONNECTED`;
- router проверяет capability до отправки;
- в логах есть requestId/browserId/tool/duration без payload secrets.

Router получает ровно один route из Registry, различает missing/disconnected browser, проверяет advertised capability до I/O, использует UUIDv7 и пишет только безопасные request metadata с duration/outcome без params или result payload.

### T-024 — Реализовать Pending Request Manager

- Приоритет: P0
- Зависимости: T-023
- Статус: `[x]`

Требования:

- ключ `browserId + requestId`;
- timeout через context/deadline;
- cancellation message в расширение;
- cleanup при disconnect и shutdown;
- duplicate/late/wrong-browser responses игнорируются;
- 100+ параллельных запросов без races;
- никакого удержания registry lock во время сетевого I/O.

Pending lifecycle закрыт для response, timeout, client cancel, disconnect и idempotent shutdown. Ошибки получают request/target diagnostics, cancel отправляется расширению, closed router отклоняет новые запросы; race-тест покрывает 128 параллельных requests и duplicate responses.

### T-025 — Реализовать selection state для MCP-сессий

- Приоритет: P0
- Зависимости: T-020
- Статус: `[~]`

Хранить отдельно:

- selected browser;
- selected tab для каждого browser;
- timestamps последнего использования.

Правила должны соответствовать FR-2. Добавить очистку состояния завершённых MCP-сессий и тесты двух одновременных клиентов.

### T-026 — Реализовать graceful shutdown

- Приоритет: P0
- Зависимости: T-021, T-024
- Статус: `[~]`

При остановке:

- прекратить принимать соединения;
- отменить pending requests;
- отправить расширениям server shutdown event;
- закрыть WebSocket с корректным code;
- завершить MCP transports;
- дождаться goroutines в пределах deadline.

## 8. Этап 3 — MCP transport и системные tools

### T-030 — Добавить Streamable HTTP

- Приоритет: P0
- Зависимости: T-015
- Статус: `[~]`

Критерии:

- единый endpoint `/mcp`;
- Origin validation;
- authentication;
- несколько MCP sessions;
- корректное session lifecycle;
- STDIO продолжает работать;
- legacy SSE изолирован feature flag и помечен deprecated.

### T-031 — Реализовать browser discovery tools

- Приоритет: P0
- Зависимости: T-020, T-025
- Статус: `[x]`

Инструменты:

- `browser_list`
- `browser_get`
- `browser_select`
- `browser_get_selected`
- `browser_rename`
- `browser_get_capabilities`
- `browser_ping`

Проверить no browser, one browser, multiple browsers, stale selection и disconnect.

### T-032 — Добавить MCP resources

- Приоритет: P1
- Зависимости: T-031
- Статус: `[ ]`

Ресурсы:

- `browser://instances`;
- `browser://instances/{browserId}`;
- `browser://instances/{browserId}/tabs`;
- `browser://instances/{browserId}/capabilities`.

Добавить resource update notifications, если библиотека и MCP client поддерживают их.

### T-033 — Стандартизировать результаты tools

- Приоритет: P0
- Зависимости: T-013, T-031
- Статус: `[~]`

Каждый result включает:

- фактический target;
- безопасные данные результата;
- warnings;
- duration/timestamp при необходимости;
- pagination cursor или artifact URI для больших данных.

Не оборачивать структурированный результат в неоднозначный JSON string, если SDK поддерживает structured content.

### T-034 — Добавить Artifact Store

- Приоритет: P1
- Зависимости: T-015, T-033
- Статус: `[ ]`

Для screenshot, PDF, HAR, trace и больших HTML:

- случайные непредсказуемые IDs;
- MIME type и size;
- TTL и автоматическая очистка;
- максимальная квота;
- MCP resource URI;
- отсутствие path traversal;
- redaction metadata.

## 9. Этап 4 — Chromium Extension Manifest V3

### T-040 — Создать extension scaffold

- Приоритет: P0
- Зависимости: T-002, T-003
- Статус: `[~]`

Структура:

```text
chrome-extension/
  manifest.json
  src/service-worker/
  src/content/
  src/debugger/
  src/protocol/
  src/ui/
  tests/
  icons/
```

Критерии:

- Manifest V3;
- минимальные обязательные permissions;
- optional permissions для чувствительных функций;
- весь UI и код на английском;
- нет remotely hosted code;
- есть lint, unit test и build commands.

### T-041 — Реализовать постоянную identity браузера

- Приоритет: P0
- Зависимости: T-040
- Статус: `[~]`

- Сгенерировать UUID при первом запуске.
- Хранить его в `chrome.storage.local`, не в sync storage.
- Хранить display name, server endpoint и credential.
- Предусмотреть reset identity с явным предупреждением.
- Не использовать tab/window IDs как identity браузера.

### T-042 — Реализовать pairing/status UI

- Приоритет: P0
- Зависимости: T-022, T-040, T-041
- Статус: `[~]`

Popup или side panel показывает:

- connected/disconnected/pairing/error;
- browser name и `browserId`;
- endpoint;
- latency и last connected;
- granted permission profiles;
- connect, disconnect, retry, pair, revoke и rename;
- ссылку на settings и диагностику.

Badge должен различать connected, disconnected и error.

### T-043 — Реализовать WebSocket lifecycle в service worker

- Приоритет: P0
- Зависимости: T-021, T-022, T-040, T-041
- Статус: `[x]`

Добавить:

- authenticated handshake;
- reconnect с exponential backoff и jitter;
- ping/pong;
- сохранение только необходимого состояния;
- восстановление после service worker restart;
- online/offline handling;
- корректную остановку по команде пользователя;
- защиту от нескольких одновременных reconnect loops.

### T-044 — Реализовать capability negotiation

- Приоритет: P0
- Зависимости: T-003, T-043
- Статус: `[~]`

Capabilities формируются из:

- browser/version;
- наличия Chrome API;
- текущих permissions;
- host access;
- feature flags.

При изменении permissions отправляется `capabilities_changed`.

### T-045 — Реализовать extension command router

- Приоритет: P0
- Зависимости: T-011, T-043
- Статус: `[~]`

Критерии:

- allowlist известных command names;
- schema validation params;
- отдельные handlers по доменам;
- поддержка cancellation;
- один response на request;
- structured error mapping;
- target validation перед выполнением;
- неизвестная команда возвращает `CAPABILITY_UNAVAILABLE` или `INVALID_COMMAND`.

### T-046 — Реализовать content-script bridge

- Приоритет: P0
- Зависимости: T-014, T-045
- Статус: `[~]`

- адресация tab/frame/document;
- автоматическая инъекция только при разрешённом host access;
- handshake готовности content script;
- повторная инъекция после navigation;
- restricted URL handling;
- timeout и cancellation;
- сообщения не доверяют payload страницы.

### T-047 — Реализовать permission management UI

- Приоритет: P1
- Зависимости: T-003, T-042, T-044
- Статус: `[~]`

Пользователь может включать/выключать optional profiles и видеть:

- объяснение permission;
- связанные tools;
- host allowlist;
- текущий статус;
- необходимость reload/reconnect.

## 10. Этап 5 — Core browser automation

### T-050 — Реализовать window tools

- Приоритет: P0
- Зависимости: T-031, T-045
- Статус: `[ ]`

List, get, create, update, focus и close. Покрыть normal, popup, minimized, maximized и fullscreen, если API позволяет.

### T-051 — Реализовать tab tools

- Приоритет: P0
- Зависимости: T-031, T-045
- Статус: `[~]`

List, get, create, activate, navigate, reload, stop, back, forward, move, duplicate, close, pin, mute и zoom.

Исправить текущую логику, которая всегда выбирает active tab и игнорирует адресный `tabId`.

### T-052 — Реализовать tab groups и sessions

- Приоритет: P1
- Зависимости: T-051
- Статус: `[ ]`

Group, ungroup, update group, recently closed и restore. Добавить capability fallback для неподдерживаемых браузеров.

### T-053 — Реализовать locator engine

- Приоритет: P0
- Зависимости: T-014, T-046
- Статус: `[ ]`

Критерии:

- все locator strategies из PRD;
- strict mode по умолчанию для действий;
- видимость и actionability checks;
- понятная диагностика 0 или нескольких совпадений;
- open shadow roots;
- element reference имеет document identity и TTL.

### T-054 — Реализовать page inspection tools

- Приоритет: P0
- Зависимости: T-046, T-053
- Статус: `[~]`

Page info, HTML, visible text, query и element details.

Добавить:

- maxChars/maxDepth;
- include/exclude selectors;
- pagination;
- redaction password/secret fields;
- frame metadata;
- нормализованные результаты вместо сырого неограниченного HTML.

### T-055 — Реализовать semantic snapshot

- Приоритет: P0
- Зависимости: T-053, T-054
- Статус: `[ ]`

Snapshot должен быть компактным и пригодным для LLM:

- roles, names, states и ключевой текст;
- стабильные временные element references;
- interactive-only режим;
- max depth/max nodes;
- frame boundaries;
- предупреждение о truncation.

### T-056 — Реализовать interaction tools

- Приоритет: P0
- Зависимости: T-053
- Статус: `[~]`

Click, double/context click, hover, focus/blur, fill/type/clear, press/chord, select, check/uncheck, scroll, drag/drop, dispatch и submit.

Критерии:

- actionability checks;
- scroll into view;
- выбор content-script или trusted CDP input;
- ожидание navigation опционально;
- password value не возвращается;
- действие возвращает фактический element/target.

### T-057 — Реализовать wait engine

- Приоритет: P0
- Зависимости: T-024, T-053
- Статус: `[ ]`

Поддержать условия FR-8, context cancellation, polling/event modes и общий deadline. Добавить тесты navigation, disappearing element и timeout.

### T-058 — Реализовать screenshots

- Приоритет: P0
- Зависимости: T-034, T-051
- Статус: `[ ]`

Viewport screenshot обязателен для MVP. Full page и element screenshot — P1.

Проверить:

- PNG/JPEG;
- quality для JPEG;
- artifact URI;
- корректный target;
- max dimensions/size;
- восстановление scroll/viewport после full-page capture.

### T-059 — Реализовать print to PDF

- Приоритет: P1
- Зависимости: T-034, T-060
- Статус: `[ ]`

Поддержать page ranges, landscape, margins, backgrounds и artifact result при наличии CDP capability.

### T-05A — Реализовать batch

- Приоритет: P1
- Зависимости: T-024, T-033
- Статус: `[ ]`

Ограничить число шагов, общий timeout и итоговый размер. Проверять каждую вложенную команду теми же policy и permission checks.

## 11. Этап 6 — CDP, console и network

### T-060 — Реализовать CDP Session Manager

- Приоритет: P1
- Зависимости: T-003, T-045, T-051
- Статус: `[ ]`

Требования:

- attach/detach lifecycle;
- reference counting потребителей;
- одна управляемая сессия на target;
- обработка DevTools conflict и `onDetach`;
- child targets и frames по capability;
- автоматический detach;
- allowlisted domains;
- bounded event fan-out.

### T-061 — Реализовать console и page error capture

- Приоритет: P0
- Зависимости: T-046
- Статус: `[ ]`

Заменить некорректное разовое получение console API на event-driven capture. Базовый P0-вариант реализовать через упакованный main-world bridge и content-script bridge без обязательного `debugger` permission. При наличии CDP capability расширять его событиями Runtime, Log и Network.

Добавить:

- start/stop/clear/read;
- уровни и фильтры;
- exceptions/unhandled rejections;
- cursor и ring buffer;
- serialization объектов с лимитами;
- redaction.

### T-062 — Реализовать network capture

- Приоритет: P1
- Зависимости: T-060, T-034
- Статус: `[ ]`

Заменить заглушку:

- подписка на request/response/loading events;
- request map и redirect chains;
- headers/timing/status/type/initiator;
- failed requests;
- body по requestId;
- HAR-like export;
- лимиты, TTL и redaction.

### T-063 — Реализовать emulation

- Приоритет: P1
- Зависимости: T-060
- Статус: `[ ]`

Viewport/device scale, mobile/touch, offline/network throttling, UA, locale, timezone, geolocation, media/color scheme. Все изменения должны иметь reset tool и очищаться при detach.

### T-064 — Реализовать accessibility tree

- Приоритет: P1
- Зависимости: T-060
- Статус: `[ ]`

Добавить full/partial tree, фильтры, frame association и связь узлов с locator/element reference.

### T-065 — Реализовать JavaScript evaluation

- Приоритет: P1
- Зависимости: T-003, T-060
- Статус: `[ ]`

По умолчанию isolated world. Ограничить timeout, depth, serialized size и типы результата. Main world включать отдельным feature flag.

### T-066 — Реализовать raw CDP tool

- Приоритет: P2
- Зависимости: T-060, security review T-083
- Статус: `[ ]`

Выключен по умолчанию. Добавить allowlist/denylist методов, params/result size limits, audit и запрет команд, выходящих за доступные домены `chrome.debugger`.

### T-067 — Реализовать performance diagnostics

- Приоритет: P2
- Зависимости: T-060, T-034
- Статус: `[ ]`

Performance metrics, tracing, coverage, profiler и audits как отдельные bounded sessions с artifact output.

## 12. Этап 7 — optional data domains

### T-070 — Реализовать cookies

- Приоритет: P1
- Зависимости: T-003, T-047, T-081
- Статус: `[ ]`

List/get/set/remove, domain filters, partition metadata и masking values по умолчанию. Значения доступны только при включённой sensitive-data настройке.

### T-071 — Реализовать web storage

- Приоритет: P1
- Зависимости: T-046, T-060, T-081
- Статус: `[ ]`

localStorage, sessionStorage, Cache Storage, IndexedDB metadata и clear origin data. Не возвращать неограниченные базы или blobs.

### T-072 — Реализовать downloads

- Приоритет: P1
- Зависимости: T-003, T-047
- Статус: `[ ]`

Create/list/status/pause/resume/cancel/erase history. Не читать содержимое скачанных файлов. Redact локальные пути в обычном режиме.

### T-073 — Реализовать history, bookmarks и reading list

- Приоритет: P2
- Зависимости: T-003, T-047, T-083
- Статус: `[ ]`

Разделить read и mutate tools. Массовое удаление требует `confirm: true`. Добавить audit и pagination.

### T-074 — Исследовать clipboard и file input

- Приоритет: P2
- Зависимости: T-003, T-083
- Статус: `[ ]`

Подготовить security design и browser matrix до реализации. Не добавлять обход user gesture или произвольный доступ к файловой системе.

### T-075 — Исследовать proxy/content settings/browsing data

- Приоритет: P2
- Зависимости: T-083
- Статус: `[ ]`

Отдельный design review. Эти функции не должны входить в стандартную сборку без явного решения владельца продукта.

## 13. Этап 8 — безопасность и ограничения

### T-080 — Закрыть сетевой контур

- Приоритет: P0
- Зависимости: T-015, T-022, T-030
- Статус: `[~]`

- loopback binding по умолчанию;
- Origin/Host validation;
- authentication MCP и extension endpoints;
- защита от DNS rebinding;
- WebSocket message size limit;
- rate limits;
- безопасные timeouts;
- отсутствие токенов в URL и логах.

### T-081 — Реализовать redaction и data limits

- Приоритет: P0
- Зависимости: T-013
- Статус: `[~]`

Редактировать:

- authorization/cookie headers;
- cookie values;
- password fields;
- form data;
- query/token patterns;
- clipboard;
- локальные paths.

Добавить лимиты HTML, text, DOM nodes, bodies, console args, events, artifacts и batch output.

### T-082 — Реализовать origin/action policy

- Приоритет: P0
- Зависимости: T-015, T-023
- Статус: `[~]`

- origin allowlist/denylist;
- restricted schemes;
- incognito policy;
- tool allowlist по профилю;
- `confirm: true` для destructive operations;
- запрет silent permission escalation;
- audit denied actions.

### T-083 — Провести security review P2 функций

- Приоритет: P1 до начала P2
- Зависимости: T-003, T-080, T-081, T-082
- Статус: `[ ]`

Рассмотреть угрозы:

- malicious page ↔ content script;
- malicious local website ↔ WebSocket/MCP;
- compromised MCP client;
- credential theft/replay;
- data exfiltration через logs/artifacts;
- arbitrary CDP/evaluate;
- history/bookmarks/cookies/clipboard;
- oversized payload и event flood.

Результат — threat model и список разрешённых/запрещённых функций.

## 14. Этап 9 — тестирование

### T-090 — Unit-тесты Go core

- Приоритет: P0
- Зависимости: T-020–T-025
- Статус: `[x]`

Покрыть registry, duplicate connection, router, selection, pending requests, timeout, cancellation, disconnect cleanup, error mapping, redaction и config.

Реализовано; суммарное statement coverage `internal/...` в atomic-режиме — 82,9%. Redaction дополнительно проверяется extension protocol tests.

### T-091 — Race и stress tests

- Приоритет: P0
- Зависимости: T-090
- Статус: `[~]`

Сценарии:

- одновременные connect/disconnect;
- 100+ parallel requests;
- duplicate и late responses;
- slow/dead connection;
- reconnect с тем же browserId;
- shutdown под нагрузкой;
- 10 000 команд без cross-routing.

### T-092 — Protocol contract tests

- Приоритет: P0
- Зависимости: T-012, T-045
- Статус: `[~]`

Один набор JSON fixtures проверяется Go и extension тестами. Добавить malformed, wrong version, unknown field, oversized и invalid target cases.

### T-093 — Integration tests с fake extensions

- Приоритет: P0
- Зависимости: T-024, T-030, T-031
- Статус: `[~]`

Поднять сервер и минимум два fake WebSocket browser clients. Проверить selection, routing, responses, timeouts, reconnect и несколько MCP sessions.

### T-094 — Extension unit tests

- Приоритет: P0
- Зависимости: T-045, T-046
- Статус: `[~]`

Mock Chrome APIs для identity, pairing, reconnect, capability detection, tab routing, content bridge, permission denied и command errors.

### T-095 — Двухбраузерный E2E

- Приоритет: P0
- Зависимости: T-050–T-058, T-093, T-094
- Статус: `[ ]`

Автоматизированный сценарий:

1. Запустить два изолированных Chromium profiles.
2. Загрузить extension.
3. Выполнить pairing обоих.
4. Открыть разные тестовые страницы.
5. Выбрать browser A и выполнить действия только в A.
6. Выбрать browser B и выполнить действия только в B.
7. Запустить параллельные команды.
8. Перезапустить service worker и проверить reconnect.

### T-096 — Security tests

- Приоритет: P0
- Зависимости: T-080–T-083
- Статус: `[~]`

Проверить bad Origin/Host, invalid token, replay, forbidden origin, restricted URL, missing confirm, secret redaction, message bomb и event flood.

### T-097 — Performance и soak tests

- Приоритет: P1
- Зависимости: T-091, T-095
- Статус: `[ ]`

Проверить критерии NFR и 8-часовой reconnect/event soak. Зафиксировать память, goroutines, latency и dropped events.

## 15. Этап 10 — документация, CI и релиз

### T-100 — Обновить README

- Приоритет: P0
- Зависимости: MVP implementation
- Статус: `[x]`

README на английском должен содержать:

- архитектуру;
- prerequisites;
- запуск STDIO и Streamable HTTP;
- сборку и установку расширения;
- pairing двух браузеров;
- выбор browser/tab;
- минимальный tool reference;
- security defaults;
- troubleshooting.

### T-101 — Написать Extension Installation Guide

- Приоритет: P0
- Зависимости: T-040–T-047
- Статус: `[~]`

На английском: unpacked installation для Chrome/Edge, permissions, pairing, update, revoke, diagnostics и uninstall.

### T-102 — Сгенерировать Tool Reference

- Приоритет: P1
- Зависимости: T-033, core tools
- Статус: `[ ]`

Для каждого tool: назначение, input schema, result, permissions, capabilities, errors и пример.

### T-103 — Настроить CI

- Приоритет: P0
- Зависимости: T-040, T-090, T-094
- Статус: `[~]`

CI запускает:

- Go format/vet/test/race;
- JavaScript format/lint/test/build;
- JSON Schema contract tests;
- dependency/license scan;
- secret scan;
- build artifacts.

Локальные команды `fmt-check`, `vet`, `lint`, `test-race`, `coverage`, extension checks и сборка добавлены в `Makefile`; CI workflow ещё не создан.

### T-104 — Подготовить reproducible builds

- Приоритет: P1
- Зависимости: T-103
- Статус: `[ ]`

- Go binaries для поддерживаемых ОС/архитектур;
- version metadata;
- checksums;
- extension ZIP;
- manifest version update;
- SBOM;
- release notes.

### T-105 — Подготовить release checklist

- Приоритет: P0
- Зависимости: все MVP-задачи
- Статус: `[ ]`

Проверить:

- все критерии MVP из PRD;
- два реальных браузера;
- fresh install и upgrade;
- permission warnings;
- pairing/revoke;
- no Cyrillic вне `AGENTS.md`, `TASKS.md` и `PRD.md`;
- no secrets;
- docs match CLI/UI;
- known limitations опубликованы.

## 16. Критический путь MVP

Рекомендуемый порядок:

1. T-001–T-003 — решения.
2. T-010–T-015 — структура и контракт.
3. T-020–T-026 — registry и безопасная адресная маршрутизация.
4. T-030, T-031, T-033 — MCP transport и выбор браузера.
5. T-040–T-046 — расширение, identity, pairing и command routing.
6. T-050, T-051, T-053–T-058 — core automation.
7. T-080–T-082 — security baseline.
8. T-090–T-096 — tests.
9. T-100, T-101, T-103, T-105 — документация и релиз.

P1/P2 задачи не должны задерживать MVP. Базовая часть T-061 относится к P0; CDP-расширение console diagnostics можно завершить на P1.

## 17. Предлагаемый первый инкремент

Первый вертикальный инкремент должен доказать мультибраузерную архитектуру до расширения каталога tools:

- protocol v1;
- два fake extension clients с разными browserId;
- Browser Registry;
- targeted Router;
- pending request correlation;
- `browser_list` и `browser_select`;
- одна адресная команда `browser_tab_list`;
- integration test, доказывающий отсутствие cross-routing;
- затем минимальное MV3 расширение, выполняющее тот же сценарий в двух профилях.

Только после прохождения этого теста следует массово добавлять page/action/network tools.
