# hq

`jq` для конфигов mkon: читает mkon, отдаёт его в `jq`, печатает результат как JSON. С `-i` записывает результат обратно в файл как mkon — с комментариями, датами и NaN/Infinity.

Один статический бинарник (~2.5 МБ, только стандартная библиотека Go, парсер mkon встроен — `src/mkon`). Из внешних зависимостей только `jq`.

## Установка

```sh
manager='dnf' # apt dnf
extension='rpm' # rpm deb apk
version='2.0.0'
curl -sL https://github.com/Sam-Strand/hq/releases/download/v$version/hq_${version}_linux_amd64.$extension -o /tmp/hq.$extension &&
sudo $manager install -y /tmp/hq.$extension &&
rm /tmp/hq.$extension
```

## Примеры

```sh
hq '.repos | keys[]' repos.mkon
hq -r '.repos.foo.url' repos.mkon
hq -r --arg k foo '.repos[$k].url' repos.mkon
hq -i '.repos.foo.branch = "dev"' repos.mkon
```

## Типы, которых нет в JSON

`jq` работает с JSON, поэтому:

| mkon | что видит jq |
|---|---|
| `2024-01-15`, `2024-01-15T10:30:00Z` | `"2024-01-15"`, `"2024-01-15T10:30:00Z"` |
| `NaN`, `Infinity`, `-Infinity` | `"NaN"`, `"Infinity"`, `"-Infinity"` |

При `-i` такие строки снова становятся датами и числами там, где они были в исходном файле. Комментарии переносятся по ключам и индексам, у новых ключей комментариев нет. Если в файле не было корневых `{}`, их не будет и после записи.

## Разработка

```sh
cd src
go test ./...                      # общие тесты mkon; нужны mkon-js рядом (или MKON_JS=путь) и node
CGO_ENABLED=0 go build -ldflags "-s -w" -o hq .
```

Пакет `src/mkon` — порт mkon-js (эталон). Он проходит общий набор тестов `mkon-js/test/conformance` и сверяется с живой JS-реализацией на случайных данных и случайных текстах (`MKON_CROSS_COUNT`, `MKON_CROSS_SEED`).
