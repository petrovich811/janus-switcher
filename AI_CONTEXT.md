# AI-контекст проекта Janus

Этот файл — точка входа для AI-агентов и людей, которые впервые открыли репозиторий.
Он описывает, **что это за программа, как она устроена, что нельзя ломать и как проверять
изменения**. Архитектурные компромиссы и их причины — в [DECISIONS.md](DECISIONS.md).

Обновлён: 2026-11-22, версия 1.1.2.

---

## 1. Что это

**Janus** — небольшой демон для Linux (GNOME/Wayland и X11) и Windows, который по горячей
клавише переворачивает текст между русской и английской раскладками. Облегчённый аналог
Punto Switcher: без словаря, без автопереворота по мере набора, без прав root.

Пример:

```
ghbdtn vjq lheu   →   привет мой друг
привет мой друг   →   ghbdtn vjq lheu
```

Репозиторий: <https://github.com/petrovich811/janus-switcher>
Последний релиз: <https://github.com/petrovich811/janus-switcher/releases>

### Состояние платформ

| Платформа | Состояние |
|---|---|
| Linux / GNOME (Wayland, X11) | проверено вживую: горячие клавиши, uinput, X11-буфер, раскладки, трей; `janus diag` — 5 из 5 |
| Windows / amd64 | **alpha**: кросс-компиляция, юнит-тесты, `go vet`, CI; на живой Windows не запускалось |
| Windows / arm64 | собирается, но не проверялось |
| Windows / 386 | не поддерживается |

### Не-цели

- Автопереворот по мере набора (нужен низкоуровневый хук клавиатуры).
- Работа в окнах, запущенных от администратора (ограничение UIPI).
- Словарь, морфология, «умные» правила языка.
- Кроссплатформенный GUI: настройки — локальная веб-страница, значок — трей.

---

## 2. Быстрый старт для агента

```bash
# сборка и проверки
go build ./...
go vet ./...
go test ./...
go test -race ./...                        # Linux
GOOS=windows GOARCH=amd64 go build -o dist/janus.exe .
GOOS=windows GOARCH=amd64 go vet ./...

# быстрая проверка таблиц без демона
go run . convert 'ghbdtn vjq lheu'

# диагностика установленного демона
janus diag
```

Требуется **Go 1.26+** (модуль `go 1.26.0`, `golang.org/x/sys v0.50.0` требует 1.26).

Перед изменением прочитай:

1. этот файл;
2. [DECISIONS.md](DECISIONS.md);
3. [CHANGELOG.md](CHANGELOG.md) — последние изменения;
4. `README.md` — пользовательское поведение и ограничения.

После изменения:

1. добавь тест, если меняется логика;
2. прогони команды выше;
3. допиши `CHANGELOG.md`;
4. если решение архитектурное — добавь запись в `DECISIONS.md`;
5. не публикуй релиз без зелёного CI.

---

## 3. Карта кода

| Путь | Назначение |
|---|---|
| `main.go` | CLI, демон, IPC-обработчик, `status`/`diag`, версия |
| `platform_linux.go` | Linux-обвязка: GNOME-сочетания, D-Bus-трей, X11-буфер, автозапуск, `status`/`diag` |
| `platform_windows.go` | Windows-обвязка: `Preflight`, скрытое окно, трей, реестр, `status`/`diag` |
| `internal/convert` | таблицы йцукен/qwerty, определение направления, `Swap` |
| `internal/inject` | ввод: uinput на Linux, `SendInput` на Windows; разбор сокращений |
| `internal/clipboard` | X11-буфер на чистом Go (INCR, проба) и Win32 `CF_UNICODETEXT` |
| `internal/hotkey` | GTK-акселераторы, записи GNOME в dconf |
| `internal/layout` | раскладки: `mru-sources` в GNOME, HKL на Windows |
| `internal/tray` | StatusNotifierItem и меню D-Bus на Linux; `.ico` для Windows |
| `internal/server` | локальный HTTP API и встроенная страница настроек |
| `internal/engine` | сам переворот: копирование, конвертация, вставка, журнал, восстановление буфера |
| `internal/winhost` | Win32: скрытое окно, `RegisterHotKey`, трей, цикл сообщений |
| `internal/config` | JSON-конфиг, атомарная запись, миграция из `punto` |
| `internal/ipc` | unix-сокет на Linux, loopback TCP с токеном на Windows |

### Поток Linux

```
нажатие Ctrl+Shift+Pause
  → GNOME запускает `janus action main`
  → CLI подключается к unix-сокету демона
  → engine.Run:
      WaitModifiersReleased
      → Ctrl+C (или выделение слова)
      → convert.Swap по физическим клавишам
      → X11 WriteText(результат)
      → Ctrl+V через /dev/uinput
      → переключение раскладки через mru-sources + системное сочетание
      → отложенное восстановление буфера (если включено)
```

### Поток Windows

```
RegisterHotKey (скрытое окно, отдельный залоченный поток)
  → wndProc(WM_HOTKEY)
  → engine.Run:
      WaitModifiersReleased (GetAsyncKeyState)
      → Ctrl+C / Ctrl+Shift+Left через SendInput
      → Win32 clipboard ReadText (CF_UNICODETEXT)
      → convert.Swap
      → SendInput Unicode / clipboard WriteText
      → Ctrl+V
      → WM_INPUTLANGCHANGEREQUEST + фолбэки Win+Space / Ctrl+Shift
```

---

## 4. Инварианты и «красные линии»

Это не стилистические пожелания: нарушение ломает поведение или пользовательские данные.

### 4.1. Без cgo и без внешних зависимостей

- Бинарник собирается с `CGO_ENABLED=0`.
- Нельзя добавлять GTK, Xlib, Win32-биндинги с cgo, внешние утилиты вместо кода.
- На Linux допустимы системные команды, без которых не обойтись: `gsettings`,
  `gnome-extensions`, `xdg-open`, `pgrep`. Это осознанная часть архитектуры.

### 4.2. Переворот — по физическим клавишам

`internal/convert/keymap.go` хранит таблицы «клавиша → символ» для US и RU.
Переворот идёт через физическую клавишу, а не через транслитерацию.

Следствие, которое нельзя «чинить» наивно: пунктуация тоже едет по клавише.
`привет, мир` → `ghbdtn? vbh` (запятая в RU живёт на клавише `/`, в US на ней `?`).
Это заявленное поведение; менять его — отдельное продуктовое решение.

### 4.3. Движок сериализован

- `Engine.Run` держит `e.run`.
- Отложенное восстановление буфера (`restoreTask`) живёт под тем же `e.run`.
- `Engine.Close` ставит `closed`, ждёт текущий `Run` и сбрасывает восстановление.
- Нельзя запускать `WriteText`/`ReadText` из фоновой горутины без этого мьютекса.

### 4.4. Буфер на Linux и Windows устроен по-разному

- **Linux:** `BeginCopy()` кладёт на CLIPBOARD служебную строку `probe`, чтобы отличить
  «скопировалось» от «не скопировалось». При неудаче движок вызывает
  `RestoreAfterFailedCopy(before)` — проба заменяется прежним текстом.
- **Windows:** `BeginCopy()` возвращает номер последовательности буфера
  (`GetClipboardSequenceNumber`), буфер не трогает. `RestoreAfterFailedCopy` не нужен.
- `RestoreClipboard` восстанавливает старое **только если в буфере всё ещё наш результат**;
  чужой текст, скопированный пользователем, не затирается.
- Восстановление не атомарно: между проверкой и записью другая программа может успеть
  записать своё. Это записанное ограничение, а не место для «оптимизации».

### 4.5. Windows: поток и окно буфера

- `OpenClipboard`/`CloseClipboard` идут на одном OS-потоке (`runtime.LockOSThread`).
- Окно-владелец буфера создаётся на отдельном залоченном потоке с циклом сообщений;
  нельзя возвращать создание окна на произвольный поток.
- `ptr()` — вынужденный хелпер для адреса из `GlobalLock`; он не должен превращаться
  в универсальный «обход vet».
- `WriteText`: `GlobalAlloc → GlobalLock → заполнить → GlobalUnlock → OpenClipboard →
  EmptyClipboard → SetClipboardData`. Нельзя очищать буфер до успешного выделения памяти.

### 4.6. Один экземпляр

- **Windows:** `windowsPlatform.Preflight()` захватывает mutex **до** `ipc.Listen`, иначе
  второй экземпляр перезапишет `ipc.port` и сломает IPC работающего демона. Нельзя
  переставлять `Preflight` после IPC.
- **Linux:** `ipc.Listen` сначала пробует подключиться к существующему сокету; живой демон
  не отбирается. Второй `janus daemon` завершается.

### 4.7. Конфиг

- Путь: `~/.config/janus/config.json` (или `JANUS_CONFIG_DIR`).
- Запись атомарная: temp-файл + `Rename`; при ошибке сохранения `Store.Update` откатывает
  память, чтобы диск и работающие настройки не расходились.
- `JANUS_CONFIG_DIR` отключает миграцию из `~/.config/punto`: тесты и эксперименты не должны
  подмешивать реальные настройки.

### 4.8. HTTP и безопасность

- Сервер слушает только `127.0.0.1` (по умолчанию `127.0.0.1:8791`).
- Все изменяющие запросы — POST с заголовком `X-Janus: 1`. Нельзя убирать проверку:
  это защита от случайного вызова со сторонней веб-страницы.
- IPC на Windows — loopback TCP со случайным токеном в `~/.config/janus/ipc.port`.
  Токен нельзя логировать или отдавать в UI.

### 4.9. Язык и интерфейс

- UI, README, CHANGELOG, DECISIONS — по-русски.
- Внутренние комментарии — по-английски (так уже принято в коде).
- Пользовательские сообщения об ошибках — по-русски.

---

## 5. Проверка изменений

### Обязательный минимум

```bash
go build ./...
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...                       # Linux
GOOS=windows GOARCH=amd64 go build -o /tmp/janus.exe .
GOOS=windows GOARCH=amd64 go vet ./...
```

### Полезные точечные проверки

```bash
go test -count=1 ./internal/engine -v
go test -count=1 ./internal/convert -v
go test -count=1 ./internal/tray -v
go run . convert 'руддщ'
go run . convert 'привет'
```

### Живая проверка Linux

```bash
go build -o /tmp/janus .
JANUS_CONFIG_DIR=/tmp/janus-cfg /tmp/janus diag
```

`diag` должен показать 5 из 5 на рабочей сессии: uinput, буфер, раскладки, трей,
горячие клавиши. Если `uinput` или `X11` недоступны — это среда, а не обязательно код.

### Экспериментальный демон

Не запускай второй демон против реального dconf: он перепишет горячие клавиши
пользователя. Для экспериментов:

- отдельный `JANUS_SOCKET`;
- отдельный `JANUS_CONFIG_DIR`;
- подставной `gsettings` первым в `PATH` (как в CI/ручных прогонах), чтобы не трогать
  настоящие настройки GNOME.

Живой `janus action` отправляет нажатия в активное окно и меняет буфер. Без явной просьбы
пользователя этого делать нельзя.

### Что считается доказательством

- «Собралось» — не значит «работает».
- «Windows собралась» — не значит «Windows проверена».
- Для Windows любое утверждение о поведении должно быть помечено как **непроверенное
  на живой системе**, если нет отдельного прогона.
- Нельзя выдавать оценку за измерение.

---

## 6. Известные ограничения

- **Windows alpha:** нет живой проверки; возможны сюрпризы в `SendInput`, `RegisterHotKey`,
  UIPI, HKL и трее.
- Только plain text в буфере; картинки/файлы не восстанавливаются.
- Восстановление буфера через 700 мс — эвристика; медленное приложение может прочитать
  старый текст.
- Восстановление не атомарно (см. 4.4).
- Пунктуация едет по физической клавише (см. 4.2).
- Только GNOME для автоматического назначения клавиш; в KDE/XFCE/Sway — вручную
  `janus action main`.
- Нужен модификатор: одиночная Pause в GNOME глобальным сочетанием не ставится.
- Нет автопереворота при наборе.
- X11-мост требует Xwayland; в чистом Sway буфер не работает.
- `/dev/uinput` должен быть доступен пользователю сессии.
- Windows: только amd64; работа в elevated-окнах невозможна.
- `DummyKey` (VK_PACKET) на Windows не проверен на живой системе.

---

## 7. Процесс изменений и релиза

1. Ветка/коммит с одним логическим изменением.
2. Тесты + `CHANGELOG.md`.
3. Если меняется архитектура, формат конфига, протокол или поведение буфера —
   запись в `DECISIONS.md`.
4. Push в `main`; GitHub Actions должен быть зелёным.
5. Версия — в `main.go` (`const Version`). Патч-релиз: `vX.Y.Z`.
6. Сборка Windows: `./scripts/build-windows.sh` (артефакт `dist/janus.exe`).
7. Релиз: тег + `gh release create` с `janus.exe`; в заметках честно указывать состояние
   Windows.
8. После установки: `janus version`, `janus diag`, проверить, что демон один.

---

## 8. Где что менять

| Задача | Файлы |
|---|---|
| Новая раскладка/символ | `internal/convert/keymap.go`, тесты в `convert_test.go` |
| Логика переворота | `internal/engine/engine.go`, `engine_test.go` |
| Горячие клавиши GNOME | `internal/hotkey/combo.go`, `gnome.go` |
| Ввод Linux | `internal/inject/uinput_linux.go`, `keys.go` |
| Ввод Windows | `internal/inject/keyboard_windows.go` |
| Буфер Linux | `internal/clipboard/x11.go` |
| Буфер Windows | `internal/clipboard/clipboard_windows.go` |
| Раскладки Linux | `internal/layout/layout_linux.go` |
| Раскладки Windows | `internal/layout/layout_windows.go` |
| Веб-интерфейс | `internal/server/ui/index.html`, `internal/server/server.go` |
| Трей Linux | `internal/tray/*.go` |
| Трей/окно Windows | `internal/winhost/*.go`, `platform_windows.go` |
| Конфиг | `internal/config/config.go` |
| IPC | `internal/ipc/*.go` |
| Установка | `scripts/`, `systemd/` |

---

## 9. Открытые вопросы

- Живой прогон Windows: clipboard round-trip, двойной запуск, `SendInput`, HKL, трей.
- Нужен ли fallback X11 на `TEXT`/`STRING`, если приложение не отдаёт `UTF8_STRING`.
- Можно ли сделать восстановление буфера атомарным (сравнение владельца/поколения).
- Нужна ли поддержка Windows arm64 Beyond «собирается».
- Нужен ли автопереворот через `/dev/input` как отдельная опция (пока нет).

---

## 10. Ссылки

- [README.md](README.md) — пользовательская документация.
- [DECISIONS.md](DECISIONS.md) — журнал решений.
- [CHANGELOG.md](CHANGELOG.md) — изменения по версиям.
- [AGENTS.md](AGENTS.md) — короткая памятка для агентов.
