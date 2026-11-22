# AGENTS.md — короткая памятка

Проект: **Janus**, Go-демон переворота текста между RU/EN раскладками.
Полный контекст: [AI_CONTEXT.md](AI_CONTEXT.md). Решения: [DECISIONS.md](DECISIONS.md).

## Перед изменением

1. Прочитай `AI_CONTEXT.md` и `DECISIONS.md`.
2. Не добавляй cgo и внешние GUI-библиотеки.
3. Не меняй переворот «по физическим клавишам» на транслитерацию.
4. Не трогай `e.run`-сериализацию движка и восстановление буфера без теста.
5. На Windows не переставляй `Preflight` после `ipc.Listen`.
6. Не запускай живой `janus action` и не затирай буфер пользователя без разрешения.

## Проверки

```bash
go build ./... && go vet ./... && go test -count=1 ./...
go test -race -count=1 ./...
GOOS=windows GOARCH=amd64 go build -o /tmp/janus.exe .
GOOS=windows GOARCH=amd64 go vet ./...
```

## После изменения

- тест на новую логику;
- запись в `CHANGELOG.md`;
- архитектурное решение — в `DECISIONS.md`;
- версия в `main.go`;
- зелёный CI перед релизом.

## Состояние

- Linux/GNOME: проверено вживую.
- Windows: alpha, живой проверки нет. Не выдавай сборку за работающую систему.
