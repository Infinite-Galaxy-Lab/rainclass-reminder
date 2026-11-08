# 多阶段构建：静态二进制 + 极小运行镜像。
#
# 构建：docker build -t ykt-reminder .
# 运行：docker compose up -d

FROM golang:1.23-alpine AS builder

WORKDIR /src

# 先只复制依赖描述，利用层缓存。
COPY go.mod go.sum ./
RUN go env -w GOPROXY=https://goproxy.cn,direct && go mod download

COPY . .

# 纯 Go 编译，关闭 cgo，静态链接。
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X github.com/2963609262/ykt-reminder/internal/cli.Version=$(cat VERSION 2>/dev/null || echo dev)" \
      -o /out/ykt-reminder ./cmd/ykt-reminder


FROM alpine:3.20

# ca-certificates 是访问 https 站点与飞书所必需的；tzdata 决定"本地时间"。
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -h /app ykt

COPY --from=builder /out/ykt-reminder /usr/local/bin/ykt-reminder

# 以非 root 运行；数据目录归该用户所有。
USER ykt
WORKDIR /app
ENV TZ=Asia/Shanghai \
    YKT_DATA_DIR=/app/data

# 面板在容器内需要监听所有接口才能被映射出来。
EXPOSE 8787

# 默认启动常驻提醒服务（面板随 run 一起启动）。
ENTRYPOINT ["ykt-reminder"]
CMD ["run"]
