# 多阶段构建：编译期打二进制，运行期仅静态二进制
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /erp .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 erp
WORKDIR /opt/erp
COPY --from=build /erp /opt/erp/erp
USER erp
EXPOSE 8080
ENTRYPOINT ["/opt/erp/erp", "-config", "/opt/erp/config.toml"]
