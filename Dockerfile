# Официальный образ Go
FROM golang:1.25.1 AS base

# Стадия для разработки
FROM base AS dev

# Установка Air для горячей перезагрузки
RUN go install github.com/air-verse/air@latest

# Установка рабочего каталога
WORKDIR /app

# Команда по умолчанию для разработки
CMD ["air", "-c", ".air.toml"]