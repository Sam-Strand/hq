FROM golang:1.27-alpine

RUN apk add --no-cache jq git make

WORKDIR /hq

# air для hot reload
RUN go install github.com/air-verse/air@latest

# Кэш зависимостей
COPY go.mod .
RUN go mod download

CMD ["sh"]