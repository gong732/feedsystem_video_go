# ---- 阶段1：编译（builder）----
# golang 镜像带完整工具链，负责把源码编译成静态二进制
FROM golang:1.26-alpine AS builder
WORKDIR /app

# 先拷依赖清单，利用 Docker 层缓存：依赖没变就不会重新下载
COPY go.mod go.sum ./
RUN go mod download

# 拷源码并编译
# CGO_ENABLED=0：纯静态编译，不依赖 glibc，运行镜像可以更小
COPY . .
RUN CGO_ENABLED=0 go build -o video-feed .

# ---- 阶段2：运行（runtime）----
# alpine 只有几十 MB，只装运行需要的东西（时区数据）
FROM alpine:3.20
WORKDIR /app

# 时区数据：让容器内时间与本地一致（日志/created_at 展示）
RUN apk add --no-cache tzdata
ENV TZ=Asia/Shanghai

# 只拷编译产物 + 前端静态资源，源码和工具链都不进运行镜像
COPY --from=builder /app/video-feed .
COPY static ./static

EXPOSE 8081
CMD ["./video-feed"]
