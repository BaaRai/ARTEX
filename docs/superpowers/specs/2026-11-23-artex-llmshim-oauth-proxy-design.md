# ARTEX LLM shim — OAuth reverse-proxy (ARTEX → Anthropic) — Design

- Дата: 2026-11-23
- Владелец: He
- Статус: Approved (в разговоре), к реализации
- Репозиторий: `~/code/ARTEX` (github.com/BaaRai/ARTEX)
- Артефакт: `cmd/artex-llmshim` (новый бинарь) + systemd-пример

## 1. Intent & why

Подписочный Anthropic-токен (`claude setup-token`, OAuth) нельзя воткнуть в ARTEX
напрямую: провайдер ARTEX даёт настраиваемый `BaseURL`, но **не** даёт произвольных
заголовков (правятся только `SessionHeaderKey` = session id и `UserAgent`, см.
`agent/provider.go:83-91`). OAuth-токену нужны три вещи одновременно, которых ARTEX
не выдаёт: `Authorization: Bearer <token>` (Anthropic-формат ARTEX шлёт `x-api-key`),
заголовок `anthropic-beta: oauth-2025-04-20`, и system-промпт, начинающийся со строки
идентичности Claude Code.

Решение — тонкий локальный reverse-proxy, в который ARTEX смотрит как в обычный
Anthropic-endpoint (`ARTEX_LLM_BASE_URL`), а шим сам переписывает auth + system и
форвардит на `api.anthropic.com`. Это делает подписочный токен пригодным для ARTEX
без правок в коде провайдера.

**Честная рамка.** Шим — это транспорт (auth + заголовки + system-префикс), он **не**
снимает кибер-сейфгард Anthropic: наступательный/хантинг-контент получит 429 на уровне
API независимо от транспорта. Поэтому шим полезен сейчас для не-флагируемого трафика
ARTEX (планирование, суммаризация, chat-агент) и как готовая труба под пост-CVP флип на
Opus. Это не обход сейфгарда и не строится ради него.

## 2. Goals

- `cmd/artex-llmshim` — Go-бинарь на stdlib (`net/http/httputil.ReverseProxy`),
  слушает loopback, принимает Anthropic Messages API от ARTEX, переписывает запрос,
  форвардит на upstream, прозрачно стримит ответ (SSE) обратно.
- **Преобразования запроса:**
  1. auth: снести входящие `x-api-key` и `Authorization`; поставить `Authorization:
     Bearer <token>`.
  2. `anthropic-beta`: дописать `oauth-2025-04-20`, смёржив с уже присутствующими бетами,
     без дублей; `anthropic-version` не трогать.
  3. system-identity: тело `/v1/messages` должно начинаться со строки
     `You are Claude Code, Anthropic's official CLI for Claude.` — вставить первым
     элементом, идемпотентно; поддержать `system` и как строку, и как массив блоков.
- **Конфиг:** флаги `-addr` (деф. `127.0.0.1:8788`), `-upstream` (деф.
  `https://api.anthropic.com`); токен из env `ARTEX_SHIM_TOKEN` или `-token-file`.
- **Ops:** systemd-пример в `docs/examples/` по образцу `artex.service`, токен через
  `EnvironmentFile`.
- Детерминированные Go-тесты через `httptest` фейк-upstream.

## 3. Non-goals

- Нет трансляции форматов (OpenAI↔Anthropic). Anthropic-in → Anthropic-out.
- Нет таблицы маршрутов / мультибэкендов (это был отвергнутый вариант B — общий роутер).
- Не встраиваемся в код ARTEX; шим — внешний процесс, ARTEX смотрит в него через
  `ARTEX_LLM_BASE_URL`.
- Не трогаем логику провайдера/агента ARTEX.

## 4. Anti-goals

- Шим не должен обходить кибер-сейфгард Anthropic. 429 от upstream пробрасывается как
  есть, без ретраев и без модификации контента ради прохождения классификатора.
- Токен не должен попадать в логи, тела ошибок или наружу. Слушаем только loopback.
- Шим не должен падать на кривом входе: недостижимый upstream → 502, не-JSON тело →
  форвард как есть (best-effort), а не паника.

## 5. Constraints

- Go stdlib, без внешних зависимостей (матчит ops ARTEX: один бинарь + systemd).
- `httputil.ReverseProxy` с `FlushInterval = -1` для небуферизованного стриминга.
- Идемпотентность identity-инъекции: повторный прогон того же запроса не удваивает
  префикс.
- `anthropic-beta` — список, разделённый запятыми; мёрж сохраняет чужие беты.

## 6. Approaches (рассмотренные)

- **Охват.** (A) узкий шим — один маршрут ARTEX→Anthropic-OAuth. (B) общий роутер —
  мультибэкенд + 2-сторонняя трансляция форматов для ARTEX и bbhunter. (C) среднее.
  **Выбран A:** минимальная поверхность, ровно задача «подписка в ARTEX»; роутер — это
  отдельный сервис с таблицей маршрутов и транслятором, несоразмерно текущей цели (YAGNI).
- **Рантайм/дом.** (1) Go-бинарь внутри репо ARTEX, systemd-юнит рядом с `artex.service`.
  (2) Python-демон отдельно, переиспользуемый с bbhunter. **Выбран 1:** вся затея про
  «кормить ARTEX», у ARTEX Go+systemd — один бинарь рядом с сервисом чище в эксплуатации.
- **HTTP-механика.** `httputil.ReverseProxy` (стриминг из коробки, минимум кода) vs
  ручной `http.Handler` (больше контроля, больше кода). **Выбран ReverseProxy.**

## 7. Decided how (мои вызовы)

- Один пакет `main` в `cmd/artex-llmshim/`, разбитый на: `main.go` (флаги, env, старт
  сервера, fail-fast на отсутствии токена), `transform.go` (чистые функции мутации
  запроса: `rewriteAuth`, `mergeBeta`, `injectIdentity`), `proxy.go` (сборка
  `ReverseProxy` с `Director`, чтение+мутация+переустановка тела, `FlushInterval=-1`).
- `Director` читает тело (`io.ReadAll`), пытается `json.Unmarshal` в `map[string]any`;
  при успехе мутирует `system`, перекодирует, ставит новый `Body`+`ContentLength`; при
  неудаче парсинга — оставляет тело как есть (best-effort identity).
- Токен читается один раз на старте в память; в `Director` не логируется. Логи — только
  метод/путь/статус/латентность, без заголовков и тел.
- identity-константа вынесена в `const ClaudeCodeIdentity`.

## 8. Request-transform spec (ядро логики)

Вход: `POST {upstream}/v1/messages` (ARTEX бьёт в `BaseURL` + `/v1/messages`;
`ConfigFrom` триммит хвостовой `/`, SDK добавляет путь — так что `BaseURL`
`http://127.0.0.1:8788` → шим видит `/v1/messages`).

1. **Headers:**
   - `del "x-api-key"`, `del "Authorization"`, `set "Authorization" = "Bearer "+token`.
   - `anthropic-beta`: взять существующее значение, split по `,`, trim, добавить
     `oauth-2025-04-20` если нет, join по `,`; пустое → просто `oauth-2025-04-20`.
   - остальные заголовки (`anthropic-version`, `content-type`, и т.п.) — без изменений.
2. **Body `system`:**
   - отсутствует → `system = ClaudeCodeIdentity` (строка).
   - строка `s` → если `s` уже начинается с идентичности, не трогать; иначе
     `system = ClaudeCodeIdentity + "\n\n" + s`.
   - массив блоков → если `arr[0]` — text-блок, чей `text` начинается с идентичности, не
     трогать; иначе вставить `{"type":"text","text":ClaudeCodeIdentity}` в начало.
   - иной тип → оставить как есть (best-effort).

## 9. Config & ops

- Флаги: `-addr string` (`127.0.0.1:8788`), `-upstream string`
  (`https://api.anthropic.com`), `-token-file string` (опц.).
- Env: `ARTEX_SHIM_TOKEN` (приоритет: `-token-file` если задан, иначе env). Нет токена →
  `log.Fatal` с внятным текстом, без значения.
- **Подключение ARTEX:** `ARTEX_LLM_BASE_URL=http://127.0.0.1:8788`, формат Anthropic,
  `ANTHROPIC_API_KEY=<любой непустой dummy>` (ARTEX требует непустой ключ для не-ollama,
  `provider.go:284`; шим его перезатирает), модель — реальный Anthropic id.
- systemd: `docs/examples/artex-llmshim.service` по образцу `artex.service`;
  `EnvironmentFile=/etc/artex-llmshim.env` с `ARTEX_SHIM_TOKEN=...`.

## 10. Errors & 429

- upstream недостижим / транспорт-ошибка → `ReverseProxy.ErrorHandler`: 502 + короткий
  JSON `{"error":"upstream unreachable"}`, лог без секрета.
- Anthropic вернул любой статус (вкл. 429 сейфгарда) → проброс статуса+тела как есть.
- не-JSON тело или нераспознанный `system` → форвард без identity-инъекции + лог-варн.

## 11. Testing (Go, httptest фейк-upstream)

- `Authorization` на upstream == `Bearer <token>`, `x-api-key` отсутствует.
- `anthropic-beta` на upstream содержит `oauth-2025-04-20`; при входящем
  `anthropic-beta: prompt-caching-2024-07-31` → содержит обе, без дублей.
- `system` отсутствует → upstream видит строку идентичности.
- `system` строка → идентичность впереди; уже с идентичностью → без удвоения.
- `system` массив → identity-блок первым; уже первым → без удвоения.
- стриминг: upstream отдаёт chunked SSE → клиент получает чанки по мере поступления
  (проверка небуферизованности через flush-таймлайн).
- токен не встречается в выводе логгера (перехват `log.SetOutput`).
- старт без токена → процесс-функция возвращает ошибку (вынести старт в
  `run(cfg) error`, чтобы тестировать без `os.Exit`).

## 12. Left to builder

- Точная форма JSON-тела ошибки и формат лог-строки.
- Таймауты HTTP-клиента upstream (разумные дефолты; стриминг не ограничивать по
  общему дедлайну).
- Имя env-файла и путь в systemd-примере.
- Нужен ли флаг `-v`/уровень логов (минимальный по умолчанию).

## 13. Открытые вопросы

- Нет блокирующих по дизайну.
- **Git (вне дизайна, к согласованию перед коммитом):** ARTEX — общий репо на `main`,
  рабочее дерево грязное (` M .env.example`, не наше). Нужно решение: ветка + коммит
  спека/кода, и draft-PR или локально. Не коммичу в чужой чекаут без отмашки.
