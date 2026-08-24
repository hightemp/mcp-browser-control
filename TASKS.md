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
- Статус: `[x]`

Нужно решить и записать в `PRD.md`:

- только localhost или также удалённые браузеры;
- Chrome/Chromium only или обязательный Firefox;
- доступность raw CDP и arbitrary JavaScript;
- необходимость personal-data инструментов;
- unpacked, enterprise или Chrome Web Store distribution;
- политика конкурентного управления одной вкладкой;
- какие действия требуют подтверждения пользователя.

До решения использовать допущения из раздела 18 PRD.

В разделе 18 PRD зафиксирован baseline v1: только loopback и один локальный
пользователь; обязательные Chrome/Edge и отложенный Firefox; типизированный API
с opt-in allowlisted CDP/isolated-world evaluation; optional Personal data в
одной сборке; unpacked/enterprise distribution; конкурентные адресные команды
без глобального tab lease; явные подтверждения для pairing, permissions,
закрытия окна и destructive multi-item операций. Условия пересмотра вынесены в
раздел 19 PRD.

### T-002 — Зафиксировать матрицу браузеров и версий

- Приоритет: P0
- Зависимости: T-001
- Статус: `[x]`

Результат:

- список поддерживаемых Chrome, Edge и других Chromium-браузеров;
- минимальная версия для WebSocket service worker и используемых APIs;
- таблица capabilities по браузерам;
- решение о namespace `chrome`/`browser` и compatibility layer.

В `docs/browser-support.md` зафиксированы поддерживаемые desktop Chrome/Edge
116+, совместимый Chromium, best-effort Chromium-производные и исключённые
Firefox/mobile; приведены capability/permission и release-validation matrices.
Минимум 116 синхронизирован между manifest и capability detection тестом и
основан на MV3 service-worker WebSocket lifecycle. V1 использует promise-based
`chrome` без polyfill; будущий Firefox требует отдельного adapter/manifest/E2E.
Определение browser brand вынесено в тестируемый модуль и корректно отличает
Chrome, Edge и другие Chromium-продукты независимо от порядка UA brands.

### T-003 — Зафиксировать permission profiles

- Приоритет: P0
- Зависимости: T-001
- Статус: `[x]`

Определить точный состав Core, Observe, Debug и Personal data profiles. Для каждого permission описать:

- зачем он нужен;
- install-time или optional;
- какой warning видит пользователь;
- какие tools становятся доступны;
- какие данные необходимо редактировать.

Матрица профилей зафиксирована в разделе 10 PRD: install-time Core, optional Observe (`http://*/*`, `https://*/*`), Debug (`debugger`) и Personal data (`bookmarks`, `browsingData`, `clipboardRead`, `clipboardWrite`, `cookies`, `downloads`, `history`, `sessions`, `tabGroups`). Для каждого профиля указаны системные предупреждения, связанные tool domains, redaction и зависимость Personal data от Observe для origin-scoped данных. Permission events применяются без reload/reconnect.

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
- Статус: `[x]`

Реализовать envelope и типы `hello/welcome/auth_error/revoke/request/response/cancel/event/ping/pong/capabilities_changed` из FR-4. Pairing выполняется атомарно первым `hello` с `pairingCode`, без отдельного неаутентифицированного `pair` состояния.

Критерии:

- единый lower camelCase JSON casing;
- `protocolVersion` обязателен;
- request/response содержат `browserId` и `requestId`;
- target отделён от params;
- неизвестные обязательные версии отклоняются;
- неизвестные необязательные поля игнорируются;
- есть golden JSON fixtures.

Protocol v1 реализован единообразно в Go и JavaScript, зафиксирован общей JSON Schema и golden fixtures. Fixtures также подтверждают игнорирование неизвестных optional полей; несовместимая версия отклоняется до регистрации browser connection.

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
- Статус: `[x]`

Хранить отдельно:

- selected browser;
- selected tab для каждого browser;
- timestamps последнего использования.

Правила должны соответствовать FR-2. Добавить очистку состояния завершённых MCP-сессий и тесты двух одновременных клиентов.

Store изолирует browser и per-browser tab selection по MCP session, сохраняет updated/last-used timestamps, клонирует snapshots и удаляется session hook. Добавлен `browser_select_tab`; tab-scoped tools используют сохранённый tab только своего browser, а explicit tab имеет приоритет без изменения selection.

### T-026 — Реализовать graceful shutdown

- Приоритет: P0
- Зависимости: T-021, T-024
- Статус: `[x]`

При остановке:

- прекратить принимать соединения;
- отменить pending requests;
- отправить расширениям server shutdown event;
- закрыть WebSocket с корректным code;
- завершить MCP transports;
- дождаться goroutines в пределах deadline.

Остановка закрывает MCP и WebSocket listeners, атомарно запрещает новые browser connections, отменяет pending router requests и ждёт server/connection goroutines в рамках общего deadline. Каждому подключённому расширению перед закрытием гарантированно записывается событие `server.shutdown`, после чего WebSocket завершается кодом `1001`; при исчерпании deadline соединения принудительно обрываются.

## 8. Этап 3 — MCP transport и системные tools

### T-030 — Добавить Streamable HTTP

- Приоритет: P0
- Зависимости: T-015
- Статус: `[x]`

Критерии:

- единый endpoint `/mcp`;
- Origin validation;
- authentication;
- несколько MCP sessions;
- корректное session lifecycle;
- STDIO продолжает работать;
- legacy SSE изолирован feature flag и помечен deprecated.

Streamable HTTP работает на едином `/mcp`, защищён Host/Origin/size guards и обязательным постоянным Bearer token из owner-only файла. Криптографические session IDs изолируют параллельных клиентов, а `DELETE /mcp` завершает session и очищает её selection state. STDIO не требует HTTP authentication; legacy SSE запускается только с явным `enable_legacy_sse` и пишет deprecation warning.

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
- Статус: `[x]`

Ресурсы:

- `browser://instances`;
- `browser://instances/{browserId}`;
- `browser://instances/{browserId}/tabs`;
- `browser://instances/{browserId}/capabilities`.

Добавить resource update notifications, если библиотека и MCP client поддерживают их.

Зарегистрирован статический JSON resource со списком instances и три URI template для instance metadata, live tabs и capabilities/permissions. Browser-specific resources используют только явный browserId из URI; tabs читаются через тот же безопасный router. В `mcp-go v0.32.0` отсутствует server-side обработка `resources/subscribe` и `resources/unsubscribe`, поэтому subscription capability и update notifications не заявляются до обновления SDK.

### T-033 — Стандартизировать результаты tools

- Приоритет: P0
- Зависимости: T-013, T-031
- Статус: `[x]`

Каждый result включает:

- фактический target;
- безопасные данные результата;
- warnings;
- duration/timestamp при необходимости;
- pagination cursor или artifact URI для больших данных.

Не оборачивать структурированный результат в неоднозначный JSON string, если SDK поддерживает structured content.

Все handlers возвращают единый envelope с `success`, фактическим `target`, безопасным `data`, массивом `warnings` и timestamp; browser round-trip дополнительно содержит `durationMs`. `nextCursor` и `artifactUri` поднимаются из результата расширения в стандартные поля. Используемый MCP SDK v0.32.0 ещё не имеет structured content, поэтому envelope передаётся как один JSON object в text content без повторного JSON-кодирования.

### T-034 — Добавить Artifact Store

- Приоритет: P1
- Зависимости: T-015, T-033
- Статус: `[x]`

Для screenshot, PDF, HAR, trace и больших HTML:

- случайные непредсказуемые IDs;
- MIME type и size;
- TTL и автоматическая очистка;
- максимальная квота;
- MCP resource URI;
- отсутствие path traversal;
- redaction metadata.

Disk-backed store использует 256-bit cryptographically random URL-safe IDs, owner-only directory/files и атомарную запись content/JSON metadata. MIME type, size, timestamps, TTL и нормализованные redaction rules сохраняются между перезапусками. Expired/orphaned entries очищаются при старте, чтении, записи и фоновым cleanup; настраиваемая общая квота вытесняет самые старые artifacts. MCP templates отдают text/blob content и отдельный metadata resource, а строгая проверка ID/URI исключает path traversal.

## 9. Этап 4 — Chromium Extension Manifest V3

### T-040 — Создать extension scaffold

- Приоритет: P0
- Зависимости: T-002, T-003
- Статус: `[x]`

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

Создано Manifest V3 расширение с service worker, popup и options UI. Постоянные
permissions ограничены базовыми API управления вкладками и выполнения scripts;
доступ к сайтам и чувствительным API запрашивается только через optional
permissions. Весь runtime-код поставляется внутри расширения. Отдельный
manifest contract test проверяет версию manifest, точный набор обязательных и
optional permissions, локальные entrypoints, отсутствие remote imports и
кириллицы в коде/UI. npm scripts и Makefile предоставляют format, lint, unit
test и production build команды.

### T-041 — Реализовать постоянную identity браузера

- Приоритет: P0
- Зависимости: T-040
- Статус: `[x]`

- Сгенерировать UUID при первом запуске.
- Хранить его в `chrome.storage.local`, не в sync storage.
- Хранить display name, server endpoint и credential.
- Предусмотреть reset identity с явным предупреждением.
- Не использовать tab/window IDs как identity браузера.

Identity вынесена в тестируемый модуль: UUID создаётся один раз и хранится только в `chrome.storage.local` вместе с settings и credential. Popup предоставляет отдельный destructive reset с явным подтверждением и предупреждением о server-side credential; reset сохраняет display name/endpoint, удаляет local credential, создаёт новый UUID и требует повторного pairing.

### T-042 — Реализовать pairing/status UI

- Приоритет: P0
- Зависимости: T-022, T-040, T-041
- Статус: `[x]`

Popup или side panel показывает:

- connected/disconnected/pairing/error;
- browser name и `browserId`;
- endpoint;
- latency и last connected;
- granted permission profiles;
- connect, disconnect, retry, pair, revoke и rename;
- ссылку на settings и диагностику.

Badge должен различать connected, disconnected и error.

Popup покрывает pairing/revoke, connect/disconnect/retry через Connect, rename/settings и destructive identity reset. Он показывает runtime browser, browser/connection IDs, endpoint, pairing state, heartbeat latency, last connected и granted profiles. Отдельная English options page выводит полные capabilities/permissions и diagnostics; badge имеет различимые `ON`, `OFF`, `!`, `PAIR` и переходные состояния.

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
- Статус: `[x]`

Capabilities формируются из:

- browser/version;
- наличия Chrome API;
- текущих permissions;
- host access;
- feature flags.

При изменении permissions отправляется `capabilities_changed`.

Чистый capability detector учитывает minimum Chrome version, фактическое наличие `tabs`/`scripting` APIs, API- и host-permissions и локальные feature flags. Тот же deterministic список используется в `hello`, popup/options diagnostics и `capabilities_changed`; permission add/remove listeners обновляют server registry без reconnect.

### T-045 — Реализовать extension command router

- Приоритет: P0
- Зависимости: T-011, T-043
- Статус: `[x]`

Критерии:

- allowlist известных command names;
- schema validation params;
- отдельные handlers по доменам;
- поддержка cancellation;
- один response на request;
- structured error mapping;
- target validation перед выполнением;
- неизвестная команда возвращает `CAPABILITY_UNAVAILABLE` или `INVALID_COMMAND`.

Router вынесен в чистый тестируемый модуль с явным allowlist и command-specific validation параметров. Browser, tabs и page handlers разделены по доменам; capability и target проверяются до вызова handler. Активные request IDs защищены от повторного выполнения, cancellation завершается единым structured response, а ошибки нормализуются без утечки stack trace.

### T-046 — Реализовать content-script bridge

- Приоритет: P0
- Зависимости: T-014, T-045
- Статус: `[x]`

- адресация tab/frame/document;
- автоматическая инъекция только при разрешённом host access;
- handshake готовности content script;
- повторная инъекция после navigation;
- restricted URL handling;
- timeout и cancellation;
- сообщения не доверяют payload страницы.

Service worker обращается к frame/document через отдельный versioned bridge и проверяет readiness перед каждой page-командой. После navigation отсутствующий content script инъецируется on demand только после host-access проверки; устаревшая версия требует reload. Весь вызов ограничен request timeout и cancellation, а bridge принимает только собственные extension messages и валидирует envelope и allowlisted error codes ответа.

### T-047 — Реализовать permission management UI

- Приоритет: P1
- Зависимости: T-003, T-042, T-044
- Статус: `[x]`

Пользователь может включать/выключать optional profiles и видеть:

- объяснение permission;
- связанные tools;
- host allowlist;
- текущий статус;
- необходимость reload/reconnect.

English options UI показывает Core, Observe, Debug и Personal data отдельными карточками с disabled/partial/enabled status, точными permissions или host allowlist, предупреждением и связанными tool domains. Optional profiles запрашиваются и удаляются только по клику пользователя; Personal data автоматически включает зависимость Observe, но при удалении сохраняет её. Permission listeners немедленно обновляют capabilities и server registry без reload/reconnect.

## 10. Этап 5 — Core browser automation

### T-050 — Реализовать window tools

- Приоритет: P0
- Зависимости: T-031, T-045
- Статус: `[x]`

List, get, create, update, focus и close. Покрыть normal, popup, minimized, maximized и fullscreen, если API позволяет.

Добавлены шесть MCP tools и protocol commands для полного window lifecycle. Extension handler работает с normal/popup окнами, поддерживает focused/incognito, bounds, drawAttention и состояния normal/minimized/maximized/fullscreen; несовместимые state+bounds отклоняются до Chrome API. Window target не смешивается с выбранным tab, capability зависит от наличия `chrome.windows`, а закрытые окна возвращают отдельный `WINDOW_NOT_FOUND`.

### T-051 — Реализовать tab tools

- Приоритет: P0
- Зависимости: T-031, T-045
- Статус: `[x]`

List, get, create, activate, navigate, reload, stop, back, forward, move, duplicate, close, pin, mute и zoom.

Исправить текущую логику, которая всегда выбирает active tab и игнорирует адресный `tabId`.

Реализованы MCP tools и extension commands для list/get/create/activate/navigate/reload/stop/back/forward/move/duplicate/close/pin/mute и get/set zoom. Явный `tabId` и browser-scoped session selection доходят до каждого Chrome API call; fallback к active tab применяется только при отсутствии обоих. `tabs.stop` рекламируется только при наличии scripting API и host access, а остальные tab capabilities зависят от `tabs` API/permission.

### T-052 — Реализовать tab groups и sessions

- Приоритет: P1
- Зависимости: T-051
- Статус: `[x]`

Group, ungroup, update group, recently closed и restore. Добавить capability fallback для неподдерживаемых браузеров.

Добавлены пять MCP tools и protocol commands: group/ungroup до 100 уникальных tabs, update title/color/collapsed, список до 25 recently closed sessions и restore по sessionId либо самого свежего entry. Extension нормализует TabGroup/Session/Tab/Window без сырых Chrome objects и возвращает отдельные `TAB_GROUP_NOT_FOUND`/`SESSION_NOT_FOUND`. Group/ungroup рекламируются при наличии соответствующих `chrome.tabs` methods; update дополнительно требует API и optional `tabGroups`, sessions — API и optional `sessions`, поэтому неподдерживаемые браузеры получают capability fallback.

### T-053 — Реализовать locator engine

- Приоритет: P0
- Зависимости: T-014, T-046
- Статус: `[x]`

Критерии:

- все locator strategies из PRD;
- strict mode по умолчанию для действий;
- видимость и actionability checks;
- понятная диагностика 0 или нескольких совпадений;
- open shadow roots;
- element reference имеет document identity и TTL.

Добавлен единый locator engine для CSS, XPath, text, role/name, label, placeholder, alt, title, test id, coordinates и element reference. Действия используют strict mode по умолчанию, `nth` и `strict: false` остаются явными модификаторами; ошибки нулевого и неоднозначного результата содержат match count и краткие сведения о кандидатах. Движок умеет обходить open shadow roots через `includeShadowDOM`, проверяет attached/visible/enabled/viewport/pointer overlap и выдаёт стабильные ссылки с document identity и sliding TTL 60 секунд. Click/fill MCP tools принимают locator, frameId и documentId, а content bridge всегда сверяет актуальный Chrome documentId.

### T-054 — Реализовать page inspection tools

- Приоритет: P0
- Зависимости: T-046, T-053
- Статус: `[x]`

Page info, HTML, visible text, query и element details.

Добавить:

- maxChars/maxDepth;
- include/exclude selectors;
- pagination;
- redaction password/secret fields;
- frame metadata;
- нормализованные результаты вместо сырого неограниченного HTML.

Добавлены `browser_page_info`, bounded `browser_get_html`, `browser_get_text`, `browser_query` и `browser_get_element`. Page info объединяет document/viewport/scroll данные content script с нормализованным frame list из webNavigation. HTML ограничен maxChars/maxDepth и include/exclude selectors; text и query имеют numeric cursor, query limit и nextCursor. Password/secret/token/credential fields редактируются до ответа, HTML и text сообщают truncation/redaction warnings, а element details возвращают bounded sanitized HTML, states, value и document-scoped reference.

### T-055 — Реализовать semantic snapshot

- Приоритет: P0
- Зависимости: T-053, T-054
- Статус: `[x]`

Snapshot должен быть компактным и пригодным для LLM:

- roles, names, states и ключевой текст;
- стабильные временные element references;
- interactive-only режим;
- max depth/max nodes;
- frame boundaries;
- предупреждение о truncation.

Добавлен `browser_snapshot`/`page.snapshot`: компактный flat semantic tree с nodeId/parentId/depth, tag, role, accessible name, direct text, states и document-scoped element reference. Поддерживаются interactive-only, max depth до 50, max nodes до 5000 и обход open shadow roots; корень ответа содержит frameId/documentId/URL, shadow nodes помечаются, а достижение лимита возвращает truncation warning.

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

Реализована content-script часть: click/double/context click, hover,
focus/blur, fill/type/clear, press с modifiers, select, set/toggle checked,
page/element scroll, drag/drop, CustomEvent dispatch и submit доступны как
типизированные MCP tools. Все действия используют общий locator/actionability
pipeline, возвращают фактический element/target, редактируют password values и
могут ожидать document или same-document navigation в пределах общего
deadline. `backend: auto|content` работает сейчас; явный `backend: cdp`
возвращает `CAPABILITY_UNAVAILABLE` до завершения CDP Session Manager в T-060,
поэтому задача остаётся частично выполненной. Добавлены проверки параметров и
тесты маршрутизации, DOM-событий, redaction и navigation listener cleanup.

### T-057 — Реализовать wait engine

- Приоритет: P0
- Зависимости: T-024, T-053
- Статус: `[x]`

Поддержать условия FR-8, context cancellation, polling/event modes и общий deadline. Добавить тесты navigation, disappearing element и timeout.

Добавлен типизированный `browser_wait`/`page.wait` с единым command deadline и
условиями delay, ready state, exact/wildcard URL, element state
(attached/detached/visible/hidden/enabled/disabled), text, value, match count,
navigation, network idle и безопасным attribute predicate без JavaScript eval.
DOM-условия поддерживают polling, event и auto modes; navigation использует
`webNavigation`, а network idle — bounded per-tab observer на optional
`webRequest` из Observe profile. MCP cancellation передаётся активной операции
content script и освобождает timers, MutationObserver и listeners. Тесты
покрывают все формы условий, navigation, disappearing element, network quiet
interval, timeout и cancellation cleanup.

### T-058 — Реализовать screenshots

- Приоритет: P0
- Зависимости: T-034, T-051
- Статус: `[~]`

Viewport screenshot обязателен для MVP. Full page и element screenshot — P1.

Проверить:

- PNG/JPEG;
- quality для JPEG;
- artifact URI;
- корректный target;
- max dimensions/size;
- восстановление scroll/viewport после full-page capture.

Реализован обязательный для MVP viewport capture через
`chrome.tabs.captureVisibleTab`: типизированный `browser_screenshot` поддерживает
PNG/JPEG, JPEG quality, явный browser/tab target и ограничиваемые width, height
и encoded size. Для неактивной целевой вкладки расширение сериализует захваты
по окну, временно активирует её и восстанавливает предыдущую вкладку. Сервер
повторно проверяет MIME type, base64, сигнатуру и реальные размеры изображения,
после чего удаляет inline payload из ответа и сохраняет бинарные данные в
временный `browser://artifacts/{artifactId}`. Добавлены тесты PNG/JPEG,
маршрутизации, лимитов, корректной вкладки и восстановления active tab.
Full-page и element capture, включая восстановление scroll/viewport, остаются
P1 и будут реализованы после CDP Session Manager (T-060), поэтому задача имеет
статус частично выполненной.

### T-059 — Реализовать print to PDF

- Приоритет: P1
- Зависимости: T-034, T-060
- Статус: `[x]`

Поддержать page ranges, landscape, margins, backgrounds и artifact result при наличии CDP capability.

Добавлен full-profile MCP tool `browser_print_to_pdf` и типизированная extension
команда `page.printToPDF`, доступная только при Observe site access и явно
выданном Debug permission. Реализация использует общий CDP Session Manager с
exact allowlist единственного метода `Page.printToPDF`, повторно проверяет URL
вкладки перед печатью и не входит в batch/raw пути. Поддержаны нормализованные
page ranges, landscape, backgrounds, scale, paper width/height, четыре margin и
CSS `@page` preference; произвольные header/footer templates не выставлены.
Base64 PDF ограничен 2 MB, независимо проверяется расширением и Go-сервером по
размеру, MIME, сигнатуре и EOF, удаляется из обычного результата и сохраняется
только как TTL/quota-controlled artifact с предупреждением о чувствительном
содержимом. Добавлены Go/extension tests для permission/capability gates,
параметров CDP, invalid ranges/margins/PDF, target binding и отсутствия inline
данных.

### T-05A — Реализовать batch

- Приоритет: P1
- Зависимости: T-024, T-033
- Статус: `[x]`

Ограничить число шагов, общий timeout и итоговый размер. Проверять каждую вложенную команду теми же policy и permission checks.

Реализован `browser_batch`: до 25 типизированных команд выполняются
последовательно для одного заранее разрешённого `browserId`, с общим deadline
30 секунд по умолчанию (не более 120 секунд), `stopOnError` и ограничением
совокупного результата через `mcpMaxResultBytes`. Каждый шаг повторно проходит
MCP profile middleware, capability checks, action policy, redaction и собственные
лимиты результата. Fail-closed allowlist запрещает server-local selection,
raw `browser_send_command` и рекурсивный batch. Результат сохраняет упорядоченные
вложенные envelopes, явно сообщает об отсутствии транзакционного rollback и
покрыт тестами последовательности, продолжения после ошибки, общего deadline,
межбраузерной изоляции и повторного применения policy.

## 11. Этап 6 — CDP, console и network

### T-060 — Реализовать CDP Session Manager

- Приоритет: P1
- Зависимости: T-003, T-045, T-051
- Статус: `[x]`

Требования:

- attach/detach lifecycle;
- reference counting потребителей;
- одна управляемая сессия на target;
- обработка DevTools conflict и `onDetach`;
- child targets и frames по capability;
- автоматический detach;
- allowlisted domains;
- bounded event fan-out.

В расширении реализован единый `CDPSessionManager`: конкурентные consumers
одной вкладки разделяют один root `chrome.debugger.attach`, leases имеют
reference counting, последний release автоматически вызывает detach, а
disconnect MCP, отзыв Debug permission и browser `onDetach` принудительно
инвалидируют все consumers. Добавлены fail-closed domain allowlist, лимиты root
sessions/consumers/command payload/result и независимые bounded event queues с
учётом dropped events. DevTools conflict и Chrome API errors преобразуются в
стабильные безопасные ошибки. Chrome/Edge 116–124 поддерживают root session и
same-process frame contexts; flat child sessions/OOPIF включаются только с 125,
используют фиксированный recursive iframe-only `Target.setAutoAttach`, opaque
session IDs и очистку stale contexts. Менеджер подключён к service-worker
lifecycle, документирован в английском `docs/cdp-session-manager.md` и покрыт
unit-тестами attach sharing, refcount, auto/forced detach, conflict, onDetach,
allowlists, backpressure, cancellation, child/frame routing и cleanup. Новые
CDP-backed MCP capabilities этой инфраструктурной задачей не рекламируются.

### T-061 — Реализовать console и page error capture

- Приоритет: P0
- Зависимости: T-046
- Статус: `[~]`

Заменить некорректное разовое получение console API на event-driven capture. Базовый P0-вариант реализовать через упакованный main-world bridge и content-script bridge без обязательного `debugger` permission. При наличии CDP capability расширять его событиями Runtime, Log и Network.

Добавить:

- start/stop/clear/read;
- уровни и фильтры;
- exceptions/unhandled rejections;
- cursor и ring buffer;
- serialization объектов с лимитами;
- redaction.

Реализован базовый P0 backend без `debugger`: типизированные MCP tools
start/stop/clear/read адресуют конкретный browser/tab/frame/document, а
упакованные versioned bridges исполняются в MAIN и ISOLATED worlds. Сборщик
перехватывает console methods, JavaScript exceptions, unhandled rejections и
ошибки загрузки ресурсов. Per-document ring buffer ограничен числом записей и
двумя миллионами сериализованных символов; чтение поддерживает levels, kinds,
RFC 3339 time filter, cursor и limit, а также сообщает eviction/cursor expiry.
Сериализация ограничивает depth, breadth, nodes, строки и размер записи, не
вызывает getters, обрабатывает cycles и повторно редактирует credentials,
authorization values и чувствительные URL query parameters в обоих bridge
worlds. Capture явно document-scoped и после navigation запускается заново.
Добавлены негативные contract tests и сквозные тесты capture/filter/redaction,
exceptions, stop/clear и ring eviction. Расширение событиями CDP Runtime/Log
остаётся P1 после T-060, поэтому общий статус задачи частичный.

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
- Статус: `[x]`

Добавить full/partial tree, фильтры, frame association и связь узлов с locator/element reference.

Добавлен full-profile MCP tool `browser_get_accessibility_tree` и типизированная
extension-команда `accessibility.getTree`. Full mode использует ограниченный
`Accessibility.getFullAXTree`, partial mode — `getPartialAXTree` по ранее
полученному `backendNodeId`; `Page.getFrameTree` добавляет ограниченную связь с
frame. Поддержаны role/name/ignored фильтры, parent linkage, нормализованные
properties, locator hints и best-effort element references только для
однозначных role/name совпадений в root frame. Команда требует Observe, явно
выданный Debug permission и MCP-профиль `full`, повторно проверяет origin и
root document до/после CDP, не доступна через batch и использует exact method
allowlist общего CDP manager. Extension и Go независимо проверяют node/depth,
property/string/reference/frame и byte limits; protected/secret values
редактируются до передачи и повторно проходят server redaction. Добавлены
Go/extension tests для full/partial modes, filters, frame association,
locator/reference links, permission/target gates, invalid bounds и malformed
tree results; ограничения и release smoke требования описаны в
`docs/accessibility-tree.md`.

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
- Статус: `[x]`

Подготовить security design и browser matrix до реализации. Не добавлять обход user gesture или произвольный доступ к файловой системе.

Подготовлен английский `docs/clipboard-file-input-design.md` с актуальными
официальными источниками, browser matrix, threat model и обязательными
негативными тестами. MCP-вызов явно не считается user gesture. Для clipboard
условно разрешён только одноразовый popup flow с коротким TTL, без background,
batch, persistence и логирования содержимого; read требует отдельного focused
implementation review. File input разделён на ручной native chooser и будущий
типизированный CDP tool, принимающий только server-owned artifact URI. Параметры
локального пути, directory upload, filesystem discovery, raw-command и batch
bypass запрещены. Security review обновлён этими границами; runtime capability
в рамках исследовательской задачи не добавлялась.

### T-075 — Исследовать proxy/content settings/browsing data

- Приоритет: P2
- Зависимости: T-083
- Статус: `[x]`

Отдельный design review. Эти функции не должны входить в стандартную сборку без явного решения владельца продукта.

Подготовлен английский
`docs/proxy-content-settings-browsing-data-design.md` с browser matrix,
официальными источниками, threat analysis, rollout/recovery и обязательными
негативными и real-browser тестами. Proxy запрещён из-за browser-wide влияния,
неoptional permission и install warning; content settings запрещены даже для
read-only до нового продуктового и security review. Browsing-data cleanup
условно разрешён только после явного решения владельца продукта: отдельный
permission consent, выключенный по умолчанию feature flag, MCP `full`, точные
HTTP(S) origins, ограниченный allowlist типов и двухфазный одноразовый plan с
`confirm: true`. Global/exclusion scopes, protected/extension origins,
autofill/password/history/download deletion, batch/raw bypass и автоматический
retry запрещены. Runtime capability в рамках исследовательской задачи не
добавлялась.

## 13. Этап 8 — безопасность и ограничения

### T-080 — Закрыть сетевой контур

- Приоритет: P0
- Зависимости: T-015, T-022, T-030
- Статус: `[x]`

- loopback binding по умолчанию;
- Origin/Host validation;
- authentication MCP и extension endpoints;
- защита от DNS rebinding;
- WebSocket message size limit;
- rate limits;
- безопасные timeouts;
- отсутствие токенов в URL и логах.

Оба listener по умолчанию и при валидации конфигурации ограничены loopback;
HTTP и WebSocket проверяют Host/Origin, а endpoints требуют bearer token или
browser credential/pairing. Размеры request/message, deadlines, ping/read/write
timeouts и очереди ограничены. Добавлены настраиваемые token buckets: отдельный
для каждой MCP-сессии во всех transports и отдельный для каждого browser
WebSocket. Таблица session buckets имеет TTL и жёсткий предел, очищается при
завершении сессии; browser flood закрывается кодом 1008. Секреты не передаются
через URL и не записываются в обычные логи. Security tests покрывают forbidden
Origin, oversized frame, bounded event flood и превышение message rate.

### T-081 — Реализовать redaction и data limits

- Приоритет: P0
- Зависимости: T-013
- Статус: `[x]`

Редактировать:

- authorization/cookie headers;
- cookie values;
- password fields;
- form data;
- query/token patterns;
- clipboard;
- локальные paths.

Добавить лимиты HTML, text, DOM nodes, bodies, console args, events, artifacts и batch output.

Добавлен независимый server-side redaction boundary для всех browser tool
results, error details и JSON resources. Он повторно редактирует authorization
и cookie headers, cookie values, password/credential fields, form/request data,
clipboard, URL userinfo и чувствительные query-параметры, а также локальные
пути, даже если расширение вернуло их по ошибке. Traversal ограничен depth,
nodes и длиной строки; итоговый JSON ограничен настраиваемым
`mcpMaxResultBytes` и при превышении возвращает `PAYLOAD_TOO_LARGE`. HTML/text,
DOM snapshot, console entries/events, screenshots и artifact quota имеют более
узкие domain limits. Network bodies и batch не доступны до T-062/T-05A и при
реализации дополнительно останутся под общим result budget. Table-driven и
сквозные tests подтверждают redaction каждого класса секретов, resource/tool
границы, UTF-8 truncation и oversized response rejection.

### T-082 — Реализовать origin/action policy

- Приоритет: P0
- Зависимости: T-015, T-023
- Статус: `[x]`

- origin allowlist/denylist;
- restricted schemes;
- incognito policy;
- tool allowlist по профилю;
- `confirm: true` для destructive operations;
- запрет silent permission escalation;
- audit denied actions.

Добавлена server-side action policy с точными HTTP(S) allowlist/denylist
origin, безусловным запретом restricted schemes и Chrome/Edge extension stores,
а также preflight текущего URL и incognito state через `tabs.get` перед
адресованной командой. Destination URL проверяется до create/navigate, denylist
имеет приоритет, incognito по умолчанию запрещён и отклоняется до расходования
pairing code. `toolProfile` стал fail-closed allowlist для `tools/list` и прямых
вызовов; unknown tools не наследуют доступ. Закрытие окна требует
`confirm: true`. Optional permissions по-прежнему запрашиваются только явным
кликом в options UI, что закреплено security test. Все отказы журналируют
только action/tool, browserId, безопасный origin и reason без args, URL query и
result data. Политика настраивается через JSON, environment и flags; добавлены
table-driven, transport, middleware и extension contract tests.

### T-083 — Провести security review P2 функций

- Приоритет: P1 до начала P2
- Зависимости: T-003, T-080, T-081, T-082
- Статус: `[x]`

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

В `docs/security-review.md` зафиксированы assets, trust boundaries и восемь
категорий угроз: hostile page/bridge, local website, compromised MCP client,
credential replay, logs/artifacts, CDP/evaluate, personal data и resource
exhaustion. Для каждой категории описаны существующие controls, residual risk
и обязательные gates. Определены разрешённые только при условиях P1/P2
функции, начальный read-only raw CDP allowlist, запрещённые CDP domains/methods,
request interception, arbitrary local paths, silent permissions/proxy changes,
unbounded exports и clipboard read без безопасного user-gesture design.
Добавлена единая десятипунктовая checklist и решения по T-060–T-075; remote и
multi-user mode требуют повторного review.

## 14. Этап 9 — тестирование

### T-090 — Unit-тесты Go core

- Приоритет: P0
- Зависимости: T-020–T-025
- Статус: `[x]`

Покрыть registry, duplicate connection, router, selection, pending requests, timeout, cancellation, disconnect cleanup, error mapping, redaction и config.

Реализовано; суммарное statement coverage `internal/...` в atomic-режиме — 82,4%. Redaction дополнительно проверяется extension protocol tests.

### T-091 — Race и stress tests

- Приоритет: P0
- Зависимости: T-090
- Статус: `[x]`

Сценарии:

- одновременные connect/disconnect;
- 100+ parallel requests;
- duplicate и late responses;
- slow/dead connection;
- reconnect с тем же browserId;
- shutdown под нагрузкой;
- 10 000 команд без cross-routing.

`go test -race` покрывает одновременные registry connect/touch/list/disconnect
циклы, 128+ parallel pending requests, duplicate и late responses, timeout и
dead writer, reconnect с тем же browserId и отбрасывание stale connection,
WebSocket shutdown, а также закрытие Router с 256 активными командами. Отдельный
stress test маршрутизирует 10 000 команд через четыре браузера и 128 workers,
проверяя owner каждого ответа и нулевой pending count после завершения.

### T-092 — Protocol contract tests

- Приоритет: P0
- Зависимости: T-012, T-045
- Статус: `[x]`

Один набор JSON fixtures проверяется Go и extension тестами. Добавить malformed, wrong version, unknown field, oversized и invalid target cases.

Общий fixture corpus разделён на `valid`, schema-`invalid`,
`runtime-invalid` и `malformed` и одинаково исполняется Go и JavaScript
тестами. Он покрывает forward-compatible unknown envelope field, неизвестное
поле locator, неверную protocol version, malformed JSON, oversized display
name, отрицательный tab, frame без tab и несовпадение browserId envelope/target.

### T-093 — Integration tests с fake extensions

- Приоритет: P0
- Зависимости: T-024, T-030, T-031
- Статус: `[x]`

Поднять сервер и минимум два fake WebSocket browser clients. Проверить selection, routing, responses, timeouts, reconnect и несколько MCP sessions.

Integration harness поднимает настоящий WebSocket transport, Streamable HTTP
MCP server, две независимые MCP sessions и два fake Chromium clients с pairing.
Тест проверяет ambiguous selection, отдельные browser/tab selections,
параллельные адресные responses без cross-routing, tool timeout и protocol
cancel, disconnect, credential-authenticated reconnect с тем же browserId и
сохранение session selection после смены connectionId.

### T-094 — Extension unit tests

- Приоритет: P0
- Зависимости: T-045, T-046
- Статус: `[x]`

Mock Chrome APIs для identity, pairing, reconnect, capability detection, tab routing, content bridge, permission denied и command errors.

Unit suite использует mock Chrome APIs для identity и всех domain handlers,
проверяет capability detection, адресацию tab, content bridge, cancellation,
нормализованные command errors и отказ при отсутствии site permission. Отдельный
service-worker harness выполняет полный pairing handshake, сохраняет credential,
эмулирует разрыв WebSocket и подтверждает alarm-driven reconnect с credential без
повторной передачи pairing code.

### T-095 — Двухбраузерный E2E

- Приоритет: P0
- Зависимости: T-050–T-058, T-093, T-094
- Статус: `[x]`

Автоматизированный сценарий:

1. Запустить два изолированных Chromium profiles.
2. Загрузить extension.
3. Выполнить pairing обоих.
4. Открыть разные тестовые страницы.
5. Выбрать browser A и выполнить действия только в A.
6. Выбрать browser B и выполнить действия только в B.
7. Запустить параллельные команды.
8. Перезапустить service worker и проверить reconnect.

Build-tagged Go E2E поднимает настоящий WebSocket и Streamable HTTP MCP server,
локальный test site и два изолированных headless Chrome for Testing profiles с
unpacked MV3 extension. Через extension options context выполняются settings и
pairing, затем две MCP sessions выбирают разные browser/tab, проверяют чтение,
fill/click и параллельные команды без cross-routing. CDP используется только для
запуска profiles и остановки service worker; после его повторного запуска тест
проверяет тот же browserId, новый connectionId, credential reconnect и
сохранённый выбор MCP session. Test-only manifest выдаёт доступ только к
loopback HTTP, production optional permissions не изменяются. `make e2e`
запускается в CI на Chrome for Testing.

### T-096 — Security tests

- Приоритет: P0
- Зависимости: T-080–T-083
- Статус: `[x]`

Проверить bad Origin/Host, invalid token, replay, forbidden origin, restricted URL, missing confirm, secret redaction, message bomb и event flood.

Негативный набор проверяет реальные HTTP/WebSocket boundary: bad Host и Origin,
точный Bearer token, credential и pairing-code replay, а также фактический отказ
WebSocket upgrade для forbidden origin. WebSocket read limit закрывает oversized
frame с code 1009; flood из 5000 extension events не создаёт pending requests и
не нарушает liveness соединения. Extension tests блокируют restricted scheme и
missing site permission до Chrome API, требуют confirm для identity reset и
проверяют redaction секретов в DOM, console и server logs.

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
- Статус: `[x]`

На английском: unpacked installation для Chrome/Edge, permissions, pairing, update, revoke, diagnostics и uninstall.

`chrome-extension/INSTALL.md` описывает production build и source-development
варианты, отдельные Chrome/Edge flows, точную матрицу Core/Observe/Debug/Personal
data permissions, pairing одного и нескольких профилей, update/reload,
revocation и identity reset, поля diagnostics, безопасный uninstall и подробные
ошибки подключения, capability, permission и content bridge.

### T-102 — Сгенерировать Tool Reference

- Приоритет: P1
- Зависимости: T-033, core tools
- Статус: `[x]`

Для каждого tool: назначение, input schema, result, permissions, capabilities, errors и пример.

Добавлен детерминированный английский `docs/tool-reference.md` для всех 66
зарегистрированных tools. Генератор получает реальные описания и input schemas
через production-регистрацию `tools/list`, объединяет их с fail-closed MCP
profiles, extension capabilities, permission requirements, результатами,
ошибками и примерами. `make tool-reference` обновляет документ, а Go-тесты и
`make tool-reference-check` не позволяют добавить tool или изменить schema без
синхронного обновления reference.

### T-103 — Настроить CI

- Приоритет: P0
- Зависимости: T-040, T-090, T-094
- Статус: `[x]`

CI запускает:

- Go format/vet/test/race;
- JavaScript format/lint/test/build;
- JSON Schema contract tests;
- dependency/license scan;
- secret scan;
- build artifacts.

Локальные команды `fmt-check`, `vet`, `lint`, `test-race`, `coverage`, extension format/lint/test/build и security checks добавлены в `Makefile`. GitHub Actions запускает их для push, pull request, ручного запуска и еженедельной проверки. Отдельный security job выполняет `govulncheck`, npm audit, проверку лицензий Go/npm dependencies и полный git-history secret scan через Gitleaks. Успешная проверка публикует server binary, coverage profile и production extension как краткоживущие CI artifacts; внешние Actions закреплены на полных commit SHA и работают с минимальным `contents: read`.

### T-104 — Подготовить reproducible builds

- Приоритет: P1
- Зависимости: T-103
- Статус: `[x]`

- Go binaries для поддерживаемых ОС/архитектур;
- version metadata;
- checksums;
- extension ZIP;
- manifest version update;
- SBOM;
- release notes.

Добавлен `make release-check`, дважды собирающий и сравнивающий byte-identical
release bundle из version/commit/SOURCE_DATE_EPOCH. Bundle содержит статические
Go binaries для Linux/macOS/Windows на amd64/arm64 с embedded metadata,
deterministic extension ZIP версии `0.3.0`, SHA-256 checksums, CycloneDX 1.6
SBOM, release manifest и автоматически сгенерированные release notes. Версии
manifest/package/lock синхронизированы и проверяются тестом; `--version`
показывает metadata бинарника. CI собирает, повторно проверяет и публикует
release directory. Матрица, требования и verification flow описаны по-английски
в `docs/releasing.md`.

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
6. T-050–T-058 — core automation.
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
