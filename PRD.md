# PRD: MCP Browser Control

## 1. Сведения о документе

- Статус: черновик для согласования
- Версия: 0.1
- Язык продукта, кода и пользовательского интерфейса: английский
- Язык `AGENTS.md`, `TASKS.md` и `PRD.md`: русский
- Основной стек: Go, MCP, Chromium Extension Manifest V3, WebSocket

## 2. Краткое описание

Проект должен предоставлять MCP-сервер для управления одним или несколькими браузерами через устанавливаемое расширение. Расширение подключается к локальному серверу, регистрирует экземпляр браузера и выполняет адресованные ему команды. MCP-клиент может обнаружить подключённые браузеры, выбрать нужный экземпляр, выбрать окно, вкладку и frame, после чего читать состояние страницы и выполнять действия.

Продукт ориентирован прежде всего на Chrome и другие Chromium-браузеры, совместимые с Manifest V3. Поддержка Firefox рассматривается отдельно, поскольку часть расширенных возможностей основана на `chrome.debugger` и Chrome DevTools Protocol (CDP).

## 3. Проблема

Текущий прототип подтверждает возможность связать MCP-сервер и расширение по WebSocket, но не является надёжной мультибраузерной системой:

- сервер рассылает команды всем WebSocket-клиентам;
- постоянный идентификатор браузера отсутствует;
- при нескольких ответах невозможно гарантировать корректную корреляцию;
- выбор браузера, окна, вкладки и frame не формализован;
- протокол сообщений не версионирован;
- сетевые логи реализованы только как заглушка;
- нет модели разрешений, аутентификации, ограничений данных и подтверждения опасных действий;
- MCP использует устаревающий HTTP+SSE вместо основного Streamable HTTP транспорта;
- код сервера сосредоточен в одном файле и почти не покрыт тестами;
- расширение необходимо подготовить как полноценную часть проекта.

## 4. Цели

### 4.1. Основные цели

1. Надёжно управлять несколькими одновременно подключёнными браузерами.
2. Давать MCP-клиенту однозначный способ обнаружить и выбрать браузер.
3. Поддерживать адресацию до окна, вкладки, frame и элемента.
4. Покрыть наиболее полезные сценарии браузерной автоматизации типизированными MCP-инструментами.
5. Предоставить расширенный режим на базе CDP для возможностей, не покрытых типизированными инструментами.
6. Быть безопасным по умолчанию: localhost, pairing, минимальные разрешения, лимиты данных, аудит и явное включение опасных функций.
7. Корректно работать при параллельных запросах, переподключениях и нескольких MCP-клиентах.
8. Иметь воспроизводимые unit-, integration- и end-to-end тесты.

### 4.2. Не входит в цели первой версии

- обход CAPTCHA, антибот-защиты, DRM или политик сайтов;
- скрытие факта автоматизации и обход систем обнаружения;
- управление произвольными файлами операционной системы;
- удалённый доступ из интернета без отдельного защищённого gateway;
- гарантированная совместимость с Firefox в первой версии;
- автоматическое чтение или сохранение паролей, платёжных данных и других секретов;
- замена полноценного Selenium/Playwright-кластера для массового параллельного тестирования.

## 5. Пользователи и основные сценарии

### 5.1. Пользователи

- AI-агент, подключённый по MCP;
- разработчик, отлаживающий веб-приложение;
- тестировщик, автоматизирующий сценарии в уже авторизованном профиле;
- локальный пользователь, управляющий несколькими профилями или Chromium-браузерами.

### 5.2. Ключевые сценарии

1. Пользователь устанавливает расширение в Chrome и Edge, связывает оба экземпляра с сервером и задаёт им понятные имена.
2. MCP-клиент получает список браузеров с состоянием, версией, профилем, возможностями и разрешениями.
3. Клиент выбирает браузер, получает список вкладок и активирует нужную.
4. Агент открывает URL, ожидает загрузку, получает доступное представление страницы, находит элемент и взаимодействует с ним.
5. Агент подписывается на console/network события, воспроизводит ошибку и получает диагностические данные.
6. Агент делает скриншот, меняет viewport, эмулирует сеть и повторяет сценарий.
7. Два MCP-клиента одновременно работают с разными браузерами без пересечения команд и ответов.
8. После перезапуска service worker расширение переподключается, повторно регистрируется и не теряет свой постоянный идентификатор.

## 6. Термины и модель адресации

- **MCP client** — приложение или агент, подключённый к MCP-серверу.
- **MCP session** — отдельная сессия MCP-клиента.
- **Browser instance** — конкретная установка расширения в конкретном профиле браузера.
- **browserId** — постоянный UUID установки расширения, сохранённый в `chrome.storage.local`.
- **connectionId** — идентификатор текущего WebSocket-соединения; меняется после переподключения.
- **windowId** — идентификатор окна из Browser Extensions API.
- **tabId** — идентификатор вкладки из Browser Extensions API.
- **frameId/documentId** — идентификатор frame или документа.
- **target** — объект адресации `browserId → windowId → tabId → frameId`.
- **capability** — функция, которую конкретный браузер может выполнить при текущей версии и выданных разрешениях.

`tabId`, `windowId` и `frameId` имеют смысл только внутри конкретного `browserId`.

## 7. Принципы продукта

1. Никакой неявной рассылки команд всем браузерам.
2. Результат каждой команды должен быть привязан к исходному запросу и фактическому target.
3. Типизированные высокоуровневые инструменты предпочтительнее произвольного JavaScript или CDP.
4. Опасные и чувствительные возможности выключены по умолчанию.
5. Большие результаты должны возвращаться ограниченно, с пагинацией, фильтрацией или через временный artifact.
6. Ошибка должна быть машинно-читаемой и подсказывать способ восстановления.
7. Возможности должны определяться через capability negotiation, а не предположения о браузере.

## 8. Функциональные требования

### FR-1. Регистрация и жизненный цикл браузера

Расширение при подключении отправляет handshake со следующими данными:

- версия протокола;
- стабильный `browserId` и пользовательское имя; новый `connectionId` назначается сервером в `welcome`;
- название и версия браузера, ОС, версия расширения;
- incognito context, если он разрешён;
- список capabilities;
- выданные API- и host-разрешения;
- активные окно и вкладка;
- время запуска и heartbeat.

Сервер должен:

- аутентифицировать handshake;
- запрещать дублирующее активное соединение с тем же `browserId` либо атомарно заменять старое новым;
- хранить `connectedAt`, `lastSeen`, latency и причину последнего отключения;
- удалять или помечать просроченные соединения;
- публиковать события connect, reconnect, capability change и disconnect.

### FR-2. Обнаружение и выбор браузера

Обязательные MCP-инструменты:

- `browser_list` — список подключённых и недавно отключённых экземпляров;
- `browser_get` — подробная информация об экземпляре;
- `browser_select` — выбор браузера по умолчанию для текущей MCP-сессии;
- `browser_get_selected` — текущий выбор;
- `browser_rename` — изменение понятного имени;
- `browser_get_capabilities` — поддерживаемые функции и разрешения;
- `browser_ping` — проверка связи и задержки.

Правила выбора:

- любой target-инструмент принимает необязательный `browserId`;
- явно переданный `browserId` имеет приоритет над выбором сессии;
- если подключён ровно один браузер, он может быть выбран автоматически;
- если браузеров несколько и выбор неоднозначен, возвращается `AMBIGUOUS_BROWSER` со списком кандидатов;
- выбор хранится отдельно для каждой MCP-сессии;
- отключённый браузер никогда не заменяется другим неявно.

### FR-3. Маршрутизация и корреляция

- Команда отправляется ровно одному `browserId`.
- Ключ ожидающего ответа включает как минимум `browserId` и `requestId`.
- Request ID уникален, предпочтительно UUIDv7.
- Сервер поддерживает настраиваемый timeout и отмену запроса.
- Поздний, повторный или пришедший не от того браузера ответ игнорируется и журналируется.
- При отключении браузера все его ожидающие запросы завершаются ошибкой `BROWSER_DISCONNECTED`.
- Параллельные записи в один WebSocket сериализуются.
- Для несовместимых изменений протокола handshake отклоняется с понятной ошибкой версии.

### FR-4. Версионированный протокол Server ↔ Extension

Сообщения должны использовать единый JSON casing и иметь JSON Schema. Минимальный envelope:

```json
{
  "protocolVersion": "1.0",
  "type": "request",
  "requestId": "019...",
  "browserId": "9c6...",
  "command": "tabs.navigate",
  "target": {
    "tabId": 123,
    "frameId": 0
  },
  "params": {},
  "timeoutMs": 15000
}
```

Типы сообщений:

- `hello`, `welcome`, `auth_error`, `revoke`;
- `request`, `response`, `cancel`;
- `event`;
- `ping`, `pong`;
- `capabilities_changed`.

Отдельный `pair` envelope не используется: первый `hello` содержит одноразовый `pairingCode`, последующие — выданный `credential`. Такой handshake не создаёт промежуточное неаутентифицированное состояние соединения; успех подтверждается `welcome`, ошибка — `auth_error`, отзыв credential — подтверждаемым `revoke` exchange.

Ответ содержит `success`, `result` либо структурированную `error`, фактический target, timestamps и при необходимости предупреждения.

### FR-5. Окна, вкладки и группы

Типизированные инструменты должны покрывать:

- список, получение, создание, обновление, фокусировку и закрытие окон;
- список, получение, создание, активацию, перемещение, дублирование и закрытие вкладок;
- переход по URL, reload, stop, back и forward;
- закрепление и открепление вкладки;
- mute/unmute;
- изменение zoom;
- группировку, разгруппировку и управление tab groups;
- получение недавно закрытых сессий и восстановление вкладки/окна;
- выбор вкладки по умолчанию внутри MCP-сессии.

Массовое закрытие окон или вкладок требует явного `confirm: true`.

### FR-6. Получение состояния страницы

Инструменты чтения:

- `browser_page_info` — URL, title, readiness, viewport, scroll, frame tree;
- `browser_snapshot` — компактное семантическое или accessibility-представление страницы;
- `browser_get_html` — HTML документа или элемента с лимитом размера;
- `browser_get_text` — видимый текст с нормализацией;
- `browser_query` — поиск элементов и краткие сведения о них;
- `browser_get_element` — атрибуты, свойства, текст, bounding box и состояния;
- `browser_screenshot` — viewport, full page или элемент;
- `browser_print_pdf` — PDF страницы при наличии capability;
- `browser_get_accessibility_tree` — accessibility tree или его фрагмент.

Стратегии locator:

- CSS;
- XPath;
- текст;
- ARIA role + accessible name;
- label, placeholder, alt, title и test id;
- координаты;
- `nth` и режим strict;
- явный frame;
- опциональный проход через open shadow roots.

Ответ locator-инструмента должен включать число совпадений и диагностические данные при неоднозначности.

### FR-7. Взаимодействие со страницей

Инструменты действий:

- click, double click и context click;
- hover, focus и blur;
- fill, type и clear;
- press key и key chord;
- select option;
- check, uncheck и toggle;
- scroll страницы или элемента;
- drag and drop;
- dispatch event;
- submit form;
- set file input — только в отдельном явно включённом режиме и после оценки безопасности;
- работа через trusted CDP input при наличии debugger capability, иначе через content script.

После действия инструмент может опционально ждать навигацию или указанное условие.

### FR-8. Ожидания и синхронизация

Инструмент `browser_wait` должен поддерживать:

- timeout;
- delay;
- document ready state;
- URL или шаблон URL;
- появление, исчезновение, видимость, enabled/disabled элемента;
- текст или значение;
- число совпадений;
- navigation complete;
- network idle при активном network observer;
- пользовательское безопасное условие.

Ожидание должно отменяться MCP-клиентом и не удерживать глобальные блокировки.

### FR-9. Frames, документы и shadow DOM

- Получение frame tree.
- Явный выбор `frameId` или `documentId`.
- Корректная маршрутизация в same-process и out-of-process iframe.
- Обнаружение смены документа после navigation.
- Ошибка `STALE_TARGET` при использовании устаревшего document/element reference.
- Поддержка open shadow DOM; closed shadow DOM только в режиме CDP, если доступно.

### FR-10. JavaScript и CDP

- `browser_evaluate` выполняет JavaScript в isolated world по умолчанию.
- Выполнение в main world требует отдельной настройки.
- Результат сериализуется с ограничениями по глубине и размеру.
- `browser_cdp_command` предоставляет доступ только к доменам, разрешённым `chrome.debugger`, и выключен по умолчанию.
- Должны поддерживаться allowlist/denylist CDP-методов и полный аудит.
- Нельзя допускать удалённую загрузку исполняемого кода в пакет расширения.

### FR-11. Console, ошибки и диагностика

- start/stop/clear console capture;
- чтение console messages по уровню, времени и frame;
- JavaScript exceptions и unhandled rejections;
- page errors и failed resources;
- performance metrics;
- bounded ring buffer на вкладку;
- cursor/pagination для чтения событий;
- подписка на новые события при поддержке MCP-клиентом.

### FR-12. Network

- start/stop network capture;
- request/response events;
- URL, method, status, type, timing, initiator и redirect chain;
- request/response headers с redaction;
- request/response body по отдельному запросу и с лимитами;
- failed requests;
- экспорт HAR-подобного результата;
- cache enable/disable и clear;
- offline, latency, download/upload throttling;
- user-agent, locale, timezone, geolocation, color scheme и device/viewport emulation;
- request blocking и interception только в явно включённом debug-режиме.

Тела запросов, cookies, authorization headers и form data по умолчанию редактируются.

### FR-13. Cookies и web storage

Опциональный permission profile:

- list/get/set/remove cookies с фильтрами по домену и имени;
- localStorage и sessionStorage для выбранного origin;
- Cache Storage и IndexedDB metadata;
- очистка storage выбранного origin;
- маскирование значений по умолчанию;
- чтение значений cookies и массовая очистка требуют явной настройки.

### FR-14. Downloads, clipboard и файлы

Опциональный permission profile:

- создание download по URL;
- список и статус downloads;
- pause, resume и cancel;
- удаление записи из download history;
- получение только безопасных метаданных пути;
- clipboard read/write только после отдельного разрешения и пользовательского жеста, если он требуется платформой.

Сервер не должен произвольно читать локальные файлы через расширение.

### FR-15. История, bookmarks и browser sessions

Отдельный чувствительный permission profile:

- поиск и удаление history entries;
- чтение, создание, перемещение и удаление bookmarks;
- чтение и восстановление recently closed sessions;
- чтение и изменение reading list при наличии capability.

Массовое удаление требует `confirm: true`, а значения должны редактироваться в логах.

### FR-16. Batch и сценарии

`browser_batch` выполняет ограниченный список типизированных команд:

- строго на одном `browserId`;
- последовательно по умолчанию;
- с `stopOnError`;
- с общим deadline;
- с лимитом количества шагов и размера результата;
- без обещания транзакционного rollback.

Batch не должен обходить проверки разрешений отдельных команд.

### FR-17. MCP-интерфейс

Обязательные транспорты:

- STDIO;
- Streamable HTTP на едином endpoint, например `/mcp`.

Устаревший HTTP+SSE допускается только как временный совместимый режим.

Рекомендуемые MCP resources:

- `browser://instances`;
- `browser://instances/{browserId}`;
- `browser://instances/{browserId}/tabs`;
- `browser://instances/{browserId}/capabilities`.

Сервер должен возвращать структурированный content, а бинарные и большие результаты — через resource/artifact URI либо ограниченное base64-представление.

### FR-18. Расширение

Manifest V3 расширение включает:

- service worker;
- content script;
- popup или side panel для pairing, статуса и разрешений;
- страницу настроек;
- хранилище постоянного `browserId`, имени и конфигурации;
- reconnect с exponential backoff и jitter;
- heartbeat, совместимый с жизненным циклом service worker;
- badge состояния;
- capability detection;
- обработчики команд по отдельным модулям;
- English UI, сообщения и исходный код.

Расширение не должно содержать remotely hosted code.

## 9. Каталог MCP-инструментов по приоритету

### P0 — обязательный рабочий продукт

- Browser: list, get, select, get selected, rename, ping, capabilities.
- Windows: list, get, create, update, focus, close.
- Tabs: list, get, create, activate, navigate, reload, back, forward, move, duplicate, close.
- Page: info, snapshot, HTML, text, query, element details, screenshot.
- Actions: click, hover, focus, fill, type, clear, press, select, check, scroll.
- Wait: delay, URL, load state, selector, text, visibility.
- Diagnostics: console capture/read, page errors.

### P1 — расширенная автоматизация и отладка

- Frames и shadow DOM.
- Accessibility tree.
- Full-page/element screenshot и print to PDF.
- Network capture, bodies и HAR.
- Device, viewport, network, locale, timezone и geolocation emulation.
- Cookies, localStorage, sessionStorage и origin storage.
- Tab groups, sessions и downloads.
- Batch.
- Безопасный JavaScript evaluation.

### P2 — чувствительные и экспертные функции

- Raw allowlisted CDP.
- Request interception и modification.
- Bookmarks, history, reading list и browsing data.
- Clipboard.
- File input.
- Performance tracing, profiler, coverage и audits.
- Управление proxy/content settings только после отдельного security review.

## 10. Модель разрешений

Фиксируются четыре профиля:

| Профиль | Состав | Установка | Связанные tools | Предупреждение и redaction |
| --- | --- | --- | --- | --- |
| **Core** | `alarms`, `scripting`, `storage`, `tabs`, `webNavigation`; `chrome.windows` не требует отдельного permission | install-time | pairing/status, browser, windows, tabs, connection lifecycle | Браузер может показать предупреждение о доступе к вкладкам/истории навигации. В логах редактируются URL query/fragment, заголовки вкладок и введённые значения. |
| **Observe** | host access `http://*/*`, `https://*/*` | optional | page inspection/actions, `tabs.stop`, console/page errors и ограниченный network metadata capture | Системный prompt сообщает о чтении и изменении данных на посещаемых сайтах. DOM, form values, console arguments и URL secrets не журналируются без redaction. |
| **Debug** | `debugger` | optional | CDP-backed console/network bodies, accessibility, emulation, performance и allowlisted evaluation | Системный prompt сообщает о доступе к debugger backend. Headers, cookies, authorization data, bodies и evaluation results считаются чувствительными и редактируются или выносятся в ограниченные artifacts. |
| **Personal data** | `bookmarks`, `browsingData`, `clipboardRead`, `clipboardWrite`, `cookies`, `downloads`, `history`, `sessions`, `tabGroups`; для origin-scoped cookies/storage также требуется Observe | optional | cookies/storage, downloads, recently closed sessions, tab-group presentation, bookmarks/history, clipboard и browsing-data operations | UI перечисляет категории персональных данных и предупреждение Chrome «View and manage your tab groups» до prompt. Cookie values, history queries, bookmark titles/URLs, download paths и clipboard contents не попадают в обычные логи. Массовое удаление дополнительно требует `confirm: true`. |

Core объявляется в `permissions`. Observe объявляется в `optional_host_permissions`, Debug и Personal data — в `optional_permissions`. Включение Personal data также запрашивает Observe; отключение Observe оставляет Personal data в partial state до восстановления host access. Optional profiles включаются и выключаются только явным действием пользователя в extension UI. Permission changes отправляют `capabilities_changed`, поэтому reload вкладок и reconnect не требуются.

Каждый инструмент проверяет capability и разрешение до отправки команды. При отсутствии разрешения возвращается `PERMISSION_REQUIRED` с названием permission и инструкцией открыть UI расширения. MCP-команда не должна пытаться незаметно подтвердить системный permission prompt.

## 11. Безопасность

### 11.1. Сетевой контур

- По умолчанию сервер слушает только `127.0.0.1` и `::1`.
- WebSocket и Streamable HTTP проверяют `Origin` и `Host`.
- Первичное подключение требует одноразового pairing code или локального токена.
- Долгоживущий credential хранится с минимальными правами доступа.
- Remote mode отсутствует в MVP.

### 11.2. Политика действий

- Настраиваемые allowlist/denylist origins.
- Запрет `chrome://`, Chrome Web Store и других restricted pages обрабатывается явно.
- Incognito выключен по умолчанию.
- Опасные операции требуют `confirm: true`.
- Arbitrary evaluate, raw CDP, cookie values и request interception выключены по умолчанию.
- Логи редактируют cookies, tokens, authorization headers, password fields и значения, похожие на секреты.

### 11.3. Ограничение ресурсов

- Максимальный размер команды и ответа.
- Лимит HTML, текста, body, screenshot и event buffers.
- Deadline для каждой команды.
- Rate limit на MCP-сессию и браузер.
- Ограничение параллельных CDP attachment и автоматический detach.

## 12. Нефункциональные требования

### NFR-1. Надёжность

- Отсутствие data races под `go test -race`.
- Graceful shutdown сервера и расширения.
- Reconnect без потери постоянной идентичности.
- Не менее 100 параллельных ожидающих запросов суммарно без ошибочной корреляции.
- Один медленный браузер не блокирует остальные.

### NFR-2. Производительность

- Накладная задержка маршрутизации p95 менее 50 мс на localhost, без учёта браузерной операции.
- `browser_list` p95 менее 100 мс при 50 зарегистрированных экземплярах.
- Поток событий использует bounded buffers и backpressure.

### NFR-3. Совместимость

- Первая версия: актуальные Chrome и Chromium-браузеры с необходимыми MV3 API.
- Минимальная версия браузера фиксируется после прототипа жизненного цикла WebSocket service worker.
- Возможности, зависящие от версии, объявляются через capabilities.

### NFR-4. Поддерживаемость

- Go-код разделён на transport, registry, router, protocol, tools и config.
- Extension-код разделён на transport, command handlers, content bridge, debugger sessions и UI.
- JSON Schema и генерируемые типы используются как единый контракт.
- Все пользовательские сообщения и код — на английском.

### NFR-5. Наблюдаемость

- Структурированные логи с requestId, browserId, tool, duration и result status.
- Секреты и большие payload не журналируются.
- Метрики подключений, reconnect, latency, timeout и ошибок.
- Debug logging включается отдельно.

## 13. Модель ошибок

Минимальные коды:

- `NO_BROWSER_CONNECTED`
- `AMBIGUOUS_BROWSER`
- `BROWSER_NOT_FOUND`
- `BROWSER_DISCONNECTED`
- `WINDOW_NOT_FOUND`
- `TAB_NOT_FOUND`
- `TAB_GROUP_NOT_FOUND`
- `SESSION_NOT_FOUND`
- `FRAME_NOT_FOUND`
- `STALE_TARGET`
- `ELEMENT_NOT_FOUND`
- `STRICT_MODE_VIOLATION`
- `PERMISSION_REQUIRED`
- `CAPABILITY_UNAVAILABLE`
- `PAIRING_REQUIRED`
- `UNSUPPORTED_PROTOCOL_VERSION`
- `TIMEOUT`
- `CANCELLED`
- `PAYLOAD_TOO_LARGE`
- `RESTRICTED_URL`
- `CONFIRMATION_REQUIRED`
- `INTERNAL_ERROR`

Ошибка содержит `code`, безопасное `message`, `retryable`, фактический target и опциональные `details`.

## 14. Тестовая стратегия

- Unit-тесты registry, selection, router, protocol, timeout, cancellation и redaction.
- Race-тесты конкурентных connect/disconnect/send/response.
- Integration-тесты MCP transport и fake extension WebSocket clients.
- Contract-тесты JSON Schema между Go и JavaScript.
- Extension unit-тесты command handlers с mock Chrome APIs.
- E2E-тесты с двумя Chromium profiles и тестовым сайтом.
- Негативные E2E: disconnect во время команды, duplicate response, stale tab, permission denied, service worker restart.
- Security-тесты Origin/Host validation, pairing, oversized payload и secret redaction.

## 15. Критерии готовности MVP

MVP считается готовым, когда:

1. Два одновременно подключённых Chromium-профиля имеют стабильные разные `browserId`.
2. MCP-клиент видит оба браузера и может выбрать каждый из них.
3. Команда без выбора при двух браузерах возвращает `AMBIGUOUS_BROWSER`.
4. Команды никогда не исполняются в невыбранном браузере.
5. Для выбранного браузера работают list/activate/create/close tab, navigate, snapshot/HTML, click, fill, wait и screenshot.
6. Параллельные запросы корректно сопоставляются со своими ответами.
7. Перезапуск service worker приводит к автоматическому переподключению.
8. STDIO и Streamable HTTP проходят integration tests.
9. Сервер по умолчанию доступен только на loopback и требует pairing/authentication.
10. `go test -race ./...`, extension tests и двухбраузерный E2E проходят.
11. README содержит актуальные инструкции установки, pairing и использования.

## 16. Метрики успеха

- 0 ошибочно маршрутизированных команд в stress-тесте из 10 000 запросов.
- Не менее 99,5% успешных reconnect в локальном soak-тесте.
- Не менее 90% типовых browser automation сценариев выполняются без raw JavaScript/CDP.
- Все ошибки разрешений содержат понятное действие для пользователя.
- Нет секретов в логах тестового набора redaction.

## 17. Этапы поставки

### Этап A. Foundation

Протокол, registry, pairing, targeted routing, STDIO/Streamable HTTP, extension skeleton и два браузера.

### Этап B. Core Automation

Окна, вкладки, page inspection, locators, interactions, waits, screenshots и базовые console errors.

### Этап C. Debugging

CDP session manager, network, console, accessibility, PDF, emulation и performance.

### Этап D. Optional Data Domains

Cookies/storage, downloads, sessions, bookmarks/history и другие чувствительные API.

### Этап E. Hardening

Race/security/E2E тесты, лимиты, telemetry, packaging и release documentation.

## 18. Допущения

- Сервер и браузеры в MVP работают на одной машине.
- Основная целевая платформа — Chromium + Manifest V3.
- Один экземпляр расширения соответствует одному browser profile.
- Пользователь вручную подтверждает pairing и чувствительные permissions.
- Типизированный API покрывает частые операции, raw CDP остаётся экспертным escape hatch.
- Старый SSE сохраняется только при реальной необходимости совместимости.

## 19. Открытые вопросы для владельца продукта

Эти вопросы не блокируют Foundation, но должны быть решены до соответствующих этапов:

1. Нужно ли поддерживать удалённые браузеры в локальной сети или только localhost?
2. Нужна ли Firefox-совместимость, и если да, в какой версии продукта?
3. Допустим ли raw CDP и arbitrary JavaScript в production-сборке?
4. Нужны ли history, bookmarks, cookies и downloads или их следует вынести в отдельную сборку расширения?
5. Нужна ли публикация в Chrome Web Store либо достаточно unpacked/enterprise installation?
6. Должны ли несколько MCP-клиентов иметь право одновременно управлять одной вкладкой?
7. Нужны ли подтверждения пользователя в UI для navigation, download и отправки форм, а не только для удаления данных?

## 20. Официальные технические ориентиры

- MCP transports: <https://modelcontextprotocol.io/specification/2025-11-25/basic/transports>
- Chrome Extensions API: <https://developer.chrome.com/docs/extensions/reference/api>
- Manifest V3: <https://developer.chrome.com/docs/extensions/develop/migrate/what-is-mv3>
- Extension service worker и WebSocket: <https://developer.chrome.com/docs/extensions/how-to/web-platform/websockets>
- Permissions: <https://developer.chrome.com/docs/extensions/develop/concepts/declare-permissions>
- `chrome.debugger`: <https://developer.chrome.com/docs/extensions/reference/api/debugger>
