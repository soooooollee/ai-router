# syntax=docker/dockerfile:1
FROM node:26-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
COPY internal/admin/webdist/ /src/internal/admin/webdist/
RUN npm run build

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/admin/webdist/ ./internal/admin/webdist/
ARG VERSION=dev
ARG COMMIT=none
ARG BUILT_AT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.builtAt=${BUILT_AT}" -o /air ./cmd/airoute

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S airoute && adduser -S -G airoute airoute
WORKDIR /data
COPY --from=build /air /usr/local/bin/air
USER airoute
EXPOSE 12666 12667
ENTRYPOINT ["air"]
CMD ["start", "--config", "/data/airoute.yaml"]
