# 多阶段构建：静态二进制 + 极小运行镜像。
#
# 构建：docker build -t rainclass-reminder -f deploy/Dockerfile .
# 运行：cd deploy && docker compose up -d

FROM golang:1.23-alpine AS builder

WORKDIR /src

# 先只复制依赖描述，利用层缓存。
COPY go.mod go.sum ./
RUN go env -w GOPROXY=https://goproxy.cn,direct && go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X github.com/infinite-galaxy/rainclass-reminder/internal/cli.Version=$(cat VERSION 2>/dev/null || echo dev)" \
      -o /out/rainclass-reminder ./cmd/rainclass-reminder


FROM alpine:3.20

# ca-certificates 是访问 https 站点与飞书所必需；tzdata 决定"本地时间"。
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -h /app ykt

COPY --from=builder /out/rainclass-reminder /usr/local/bin/rainclass-reminder

# 以非 root 运行。
USER ykt
WORKDIR /app
ENV TZ=Asia/Shanghai \
    YKT_DATA_DIR=/app/data

# 面板在容器内需要监听所有接口才能被映射出来。
EXPOSE 8787

ENTRYPOINT ["rainclass-reminder"]
CMD ["run"]
